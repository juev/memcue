// Package tailnet owns Memcue's optional persistent userspace transport.
package tailnet

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"syscall"
	"time"
)

type Config struct {
	Dir, Hostname, ControlURL, Host, Port, KeyFile string
	Timeout                                        time.Duration
	// Server: Host and Port name a memcue server, not a PostgreSQL backend (#714). The profile records it, so one
	// identity never serves both.
	Server bool
	// Version is the binary's version, which status reports (R10).
	Version string
	// Worker, when set, runs for the helper's life with a dialer to Host:Port; status reports its state.
	Worker Worker
}
type State struct {
	NodeID  string `json:"node_id,omitempty"`
	Running bool   `json:"running"`
	State   string `json:"state"`
	AuthURL string `json:"auth_url,omitempty"`
	// Version is the helper's binary version; Sync the state of its Worker.
	Version string          `json:"version,omitempty"`
	Sync    json.RawMessage `json:"sync,omitempty"`
}

// Worker is work a helper runs beside its node: the replica sync of memcue server mode.
type Worker interface {
	// Run works until ctx ends; dial opens a connection to the configured destination over the tailnet.
	Run(ctx context.Context, dial func(context.Context) (net.Conn, error))
	// Status is the state of the work, reported by the helper's status as JSON.
	Status() any
}
type node interface {
	Status(context.Context) (State, error)
	Login(context.Context) error
	Lookup(context.Context, string) ([]string, error)
	Dial(context.Context, string) (net.Conn, error)
	Close() error
}
type message struct {
	Version int    `json:"version"`
	Op      string `json:"op"`
	Host    string `json:"host,omitempty"`
	Port    string `json:"port,omitempty"`
}
type reply struct {
	State
	Error string   `json:"error,omitempty"`
	IPs   []string `json:"ips,omitempty"`
}

// ErrOwned: another helper owns the tailnet identity.
var ErrOwned = errors.New("tailnet identity already has an owner; use the existing helper")

var errHelper = errors.New("embedded transport unavailable; start memcue connect serve, then memcue connect login")

func socketPath(c Config) (string, func(), error) {
	path := filepath.Join(c.Dir, "helper.sock")
	if len(path) < 104 {
		return path, func() {}, nil
	}
	if runtime.GOOS != "linux" {
		return "", nil, errors.New("tailnet state path is too long for Unix IPC")
	}
	dir, err := os.Open(c.Dir)
	if err != nil {
		return "", nil, errHelper
	}
	return fmt.Sprintf("/proc/self/fd/%d/helper.sock", dir.Fd()), func() { _ = dir.Close() }, nil
}
func timeout(c Config) time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return 5 * time.Second
}
func privateDir(dir string) error {
	fi, err := os.Lstat(dir)
	if err != nil || !fi.IsDir() || fi.Mode().Perm() != 0700 {
		return errors.New("tailnet state directory must be owned by this user with mode 0700 and no symlink")
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != uint32(os.Geteuid()) {
		return errors.New("tailnet state directory has the wrong owner")
	}
	return nil
}
func profileBytes(c Config) []byte {
	// Server is left out for a PostgreSQL profile, so the profile of an identity made before #714 stays the same.
	profile := struct {
		Version                          int
		Hostname, ControlURL, Host, Port string
		Server                           bool `json:",omitempty"`
	}{1, c.Hostname, c.ControlURL, c.Host, c.Port, c.Server}
	data, _ := json.Marshal(profile)
	return data
}

// ValidateExistingProfile refuses unsafe or mismatched retained identities before
// a convenience launcher starts another process. An absent identity is allowed.
func ValidateExistingProfile(c Config) error {
	if _, err := os.Lstat(c.Dir); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return errHelper
	}
	if err := privateDir(c.Dir); err != nil {
		return err
	}
	if _, err := os.Lstat(filepath.Join(c.Dir, "profile.json")); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return errHelper
	}
	return checkProfile(c)
}

func checkProfile(c Config) error {
	if err := privateDir(c.Dir); err != nil {
		return err
	}
	fd, err := syscall.Open(filepath.Join(c.Dir, "profile.json"), syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return errHelper
	}
	f := os.NewFile(uintptr(fd), "profile.json")
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm() != 0600 {
		return errHelper
	}
	owner, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) {
		return errHelper
	}
	data, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil || !bytes.Equal(data, profileBytes(c)) {
		return errors.New("tailnet helper profile differs; use a separate state directory or matching configuration")
	}
	return nil
}

func validateIdentityEntry(info os.FileInfo) error {
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || owner == nil || owner.Uid != uint32(os.Geteuid()) {
		return errors.New("tailnet identity entries must belong to the effective user")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("tailnet identity entries must not be symlinks")
	}
	if (info.Mode().IsRegular() || info.IsDir()) && info.Mode().Perm()&0077 != 0 {
		return errors.New("tailnet identity entries must be private")
	}
	return nil
}

// privateIdentityEntries checks metadata without opening node identity files.
func privateIdentityEntries(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		info, err := os.Lstat(filepath.Join(dir, entry.Name()))
		if err != nil {
			return err
		}
		if err := validateIdentityEntry(info); err != nil {
			return err
		}
		if info.IsDir() {
			if err := privateIdentityEntries(filepath.Join(dir, entry.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}
func acquire(c Config) (func(), error) {
	if err := os.MkdirAll(c.Dir, 0700); err != nil {
		return nil, errHelper
	}
	if err := privateDir(c.Dir); err != nil {
		return nil, err
	}
	if err := privateIdentityEntries(c.Dir); err != nil {
		return nil, err
	}
	fd, err := syscall.Open(filepath.Join(c.Dir, "owner.lock"), syscall.O_RDWR|syscall.O_CREAT|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, errors.New("cannot open private tailnet ownership lock")
	}
	f := os.NewFile(uintptr(fd), "owner.lock")
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm() != 0600 {
		_ = f.Close()
		return nil, errors.New("tailnet ownership lock must be a private regular file")
	}
	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, ErrOwned
	}
	release := func() { _ = syscall.Flock(fd, syscall.LOCK_UN); _ = f.Close() }
	data := profileBytes(c)
	profilePath := filepath.Join(c.Dir, "profile.json")
	pfd, e := syscall.Open(profilePath, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if os.IsNotExist(e) {
		pfd, e = syscall.Open(profilePath, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL|syscall.O_NOFOLLOW, 0600)
		if e != nil {
			release()
			return nil, errHelper
		}
		pf := os.NewFile(uintptr(pfd), "profile.json")
		_, e = pf.Write(data)
		if e == nil {
			e = pf.Sync()
		}
		_ = pf.Close()
	} else if e == nil {
		pf := os.NewFile(uintptr(pfd), "profile.json")
		info, se := pf.Stat()
		existing, re := io.ReadAll(io.LimitReader(pf, 4097))
		_ = pf.Close()
		if se != nil || re != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || !bytes.Equal(existing, data) {
			e = errors.New("tailnet profile differs or has unsafe permissions; select a separate private state directory")
		}
	}
	if e != nil {
		release()
		return nil, errors.New("tailnet profile differs or has unsafe permissions; select a separate private state directory")
	}
	return release, nil
}
func serve(ctx context.Context, c Config, n node, ready chan<- struct{}) error {
	release, err := acquire(c)
	if err != nil {
		return err
	}
	defer release()
	return serveOwned(ctx, c, n, ready)
}
func serveOwned(ctx context.Context, c Config, n node, ready chan<- struct{}) error {
	defer func() { _ = n.Close() }()
	ctx, stopServe := context.WithCancel(ctx)
	defer stopServe()
	if fi, err := os.Lstat(filepath.Join(c.Dir, "helper.sock")); err == nil {
		if fi.Mode()&os.ModeSocket == 0 {
			return errors.New("tailnet IPC path is not a socket")
		}
		if err := os.Remove(filepath.Join(c.Dir, "helper.sock")); err != nil {
			return errHelper
		}
	} else if !os.IsNotExist(err) {
		return errHelper
	}
	path, closeDir, err := socketPath(c)
	if err != nil {
		return err
	}
	defer closeDir()
	l, err := net.Listen("unix", path)
	if err != nil {
		return errHelper
	}
	if err := os.Chmod(filepath.Join(c.Dir, "helper.sock"), 0600); err != nil {
		_ = l.Close()
		return errHelper
	}
	defer func() { _ = l.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = l.Close() })
	defer stop()
	close(ready)
	var wg sync.WaitGroup
	defer wg.Wait()
	if c.Worker != nil {
		workCtx, stopWork := context.WithCancel(ctx)
		worked := make(chan struct{})
		dial := func(ctx context.Context) (net.Conn, error) { return dialPeer(ctx, c, n) }
		go func() { defer close(worked); c.Worker.Run(workCtx, dial) }()
		defer func() { stopWork(); <-worked }()
	}
	for {
		conn, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return errHelper
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { _ = conn.Close() }()
			closeOnCancel := context.AfterFunc(ctx, func() { _ = conn.Close() })
			defer closeOnCancel()
			_ = conn.SetDeadline(time.Now().Add(timeout(c)))
			var m message
			data, err := bufio.NewReaderSize(conn, 4096).ReadSlice('\n')
			if err != nil || json.Unmarshal(data, &m) != nil {
				return
			}
			r := reply{}
			requestCtx, cancel := context.WithTimeout(ctx, timeout(c))
			defer cancel()
			if m.Version != 1 {
				r.Error = "unsupported helper protocol"
			} else if m.Op == "off" {
				r.State = State{State: "Stopping"}
				defer stopServe()
			} else {
				state, err := n.Status(requestCtx)
				if err != nil {
					r.Error = "tailnet status unavailable"
				} else {
					state.AuthURL = ""
					r.State = state
					switch m.Op {
					case "status":
					case "login":
						if err := n.Login(requestCtx); err != nil {
							r.Error = "tailnet login could not start"
							break
						}
						logged, err := n.Status(requestCtx)
						if err != nil {
							r.Error = "tailnet login status unavailable"
						} else {
							r.State = logged
						}
					case "lookup", "dial":
						if state.State != "Running" {
							r.Error = "tailnet login or device approval required; run memcue connect login"
							break
						}
						ips, err := n.Lookup(requestCtx, c.Host)
						if err != nil || len(ips) == 0 {
							r.Error = "backend is not an approved tailnet peer"
							break
						}
						if m.Op == "lookup" {
							if m.Host != c.Host {
								r.Error = "unconfigured DNS destination"
							} else {
								r.IPs = ips
							}
						} else {
							if m.Port != c.Port || !slices.Contains(ips, m.Host) {
								r.Error = "unconfigured backend destination"
								break
							}
							upstream, err := n.Dial(requestCtx, net.JoinHostPort(m.Host, c.Port))
							if err != nil {
								r.Error = "tailnet backend unavailable"
								break
							}
							defer func() { _ = upstream.Close() }()
							stopUpstream := context.AfterFunc(ctx, func() { _ = upstream.Close() })
							defer stopUpstream()
							if json.NewEncoder(conn).Encode(r) != nil {
								return
							}
							_ = conn.SetDeadline(time.Now().Add(5 * time.Minute))
							_ = upstream.SetDeadline(time.Now().Add(5 * time.Minute))
							copied := make(chan struct{})
							go func() { _, _ = io.Copy(upstream, conn); _ = upstream.Close(); close(copied) }()
							_, _ = io.Copy(conn, upstream)
							_ = conn.Close()
							_ = upstream.Close()
							<-copied
							return
						}
					default:
						r.Error = "unsupported helper request"
					}
				}
			}
			if r.Error == "" && m.Op == "status" && c.Worker != nil {
				r.Sync, _ = json.Marshal(c.Worker.Status())
			}
			r.Version = c.Version
			_ = json.NewEncoder(conn).Encode(r)
		}()
	}
}

// dialPeer connects to Host:Port of c through the node, once the node runs and Host is one approved peer.
func dialPeer(ctx context.Context, c Config, n node) (net.Conn, error) {
	state, err := n.Status(ctx)
	if err != nil {
		return nil, errHelper
	}
	if state.State != "Running" {
		return nil, errors.New("tailnet login or device approval required; run memcue connect up")
	}
	ips, err := n.Lookup(ctx, c.Host)
	if err != nil || len(ips) == 0 {
		return nil, errors.New("server is not an approved tailnet peer")
	}
	return n.Dial(ctx, net.JoinHostPort(ips[0], c.Port))
}
func exchange(ctx context.Context, c Config, m message) (net.Conn, reply, error) {
	if err := privateDir(c.Dir); err != nil {
		return nil, reply{}, err
	}
	fi, err := os.Lstat(filepath.Join(c.Dir, "helper.sock"))
	if err != nil || fi.Mode()&os.ModeSocket == 0 || fi.Mode().Perm() != 0600 {
		return nil, reply{}, errHelper
	}
	owner, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) {
		return nil, reply{}, errHelper
	}
	d := net.Dialer{Timeout: timeout(c)}
	path, closeDir, err := socketPath(c)
	if err != nil {
		return nil, reply{}, err
	}
	defer closeDir()
	conn, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return nil, reply{}, errHelper
	}
	deadline := time.Now().Add(timeout(c))
	if until, ok := ctx.Deadline(); ok && until.Before(deadline) {
		deadline = until
	}
	_ = conn.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if json.NewEncoder(conn).Encode(m) != nil {
		_ = conn.Close()
		return nil, reply{}, errHelper
	}
	data, err := bufio.NewReaderSize(conn, 4096).ReadSlice('\n')
	var r reply
	if err != nil || json.Unmarshal(data, &r) != nil {
		_ = conn.Close()
		return nil, reply{}, errHelper
	}
	if r.Error != "" {
		_ = conn.Close()
		return nil, r, errors.New(r.Error)
	}
	return conn, r, nil
}
func request(ctx context.Context, c Config, op string) (State, error) {
	conn, r, err := exchange(ctx, c, message{Version: 1, Op: op})
	if err != nil {
		return State{}, err
	}
	_ = conn.Close()
	return r.State, nil
}

// Admin performs an explicit local helper administration operation.
func Admin(ctx context.Context, c Config, op string) (State, error) {
	if op != "status" && op != "login" && op != "off" {
		return State{}, errors.New("unsupported administration operation")
	}
	if op != "off" && c.Host != "" {
		if err := checkProfile(c); err != nil {
			return State{}, err
		}
	}
	return request(ctx, c, op)
}
