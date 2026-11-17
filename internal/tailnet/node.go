package tailnet

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"syscall"
	"time"

	"tailscale.com/client/local"
	"tailscale.com/tsnet"
)

const (
	// controlPoll is how often the helper asks its node whether it holds a control connection, and
	// controlGiveUp how long it waits for one before it exits (#777).
	controlPoll   = 30 * time.Second
	controlGiveUp = 15 * time.Minute
)

// errNoControl ends a helper that waited controlGiveUp without a control connection (#777).
var errNoControl = errors.New("control connection is gone; the helper exits, and the next session start or memcue connect up starts a new one")

type realNode struct {
	server *tsnet.Server
	client *local.Client
}

func (n *realNode) Status(ctx context.Context) (State, error) {
	st, err := n.client.Status(ctx)
	if err != nil {
		return State{}, errHelper
	}
	id := ""
	online := false
	if st.Self != nil {
		id = string(st.Self.ID)
		online = st.Self.Online
	}
	return State{Running: true, State: st.BackendState, AuthURL: st.AuthURL, NodeID: id, Online: online}, nil
}
func (n *realNode) Login(ctx context.Context) error {
	if err := n.client.StartLoginInteractive(ctx); err != nil {
		return errHelper
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		st, err := n.Status(ctx)
		if err != nil {
			return errHelper
		}
		if st.AuthURL != "" || st.State == "Running" {
			return nil
		}
		select {
		case <-ctx.Done():
			return errHelper
		case <-ticker.C:
		}
	}
}
func (n *realNode) Lookup(ctx context.Context, host string) ([]string, error) {
	st, err := n.client.Status(ctx)
	if err != nil {
		return nil, errHelper
	}
	return resolvePeer(st, host)
}
func (n *realNode) Dial(ctx context.Context, address string) (net.Conn, error) {
	return n.server.Dial(ctx, "tcp", address)
}
func (n *realNode) Close() error { return n.server.Close() }

// bootstrapKey reads the auth key of a first login from an owner-only regular file.
func bootstrapKey(path string) (string, error) {
	fd, e := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return "", errors.New("cannot open bootstrap key file")
	}
	f := os.NewFile(uintptr(fd), "bootstrap key")
	fi, e := f.Stat()
	if e != nil || !fi.Mode().IsRegular() || (fi.Mode().Perm() != 0600 && fi.Mode().Perm() != 0400) {
		_ = f.Close()
		return "", errors.New("bootstrap key must be an owner-only regular file (0400 or 0600)")
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != uint32(os.Geteuid()) {
		_ = f.Close()
		return "", errors.New("bootstrap key has the wrong owner")
	}
	data, e := io.ReadAll(io.LimitReader(f, 4097))
	_ = f.Close()
	authKey := strings.TrimSpace(string(data))
	if e != nil || len(data) > 4096 || authKey == "" || strings.ContainsAny(authKey, " \t\n\r") {
		return "", errors.New("invalid bootstrap key file")
	}
	return authKey, nil
}

// Run owns one durable embedded node until ctx is cancelled or connect off is called.
func Run(ctx context.Context, c Config, bootstrap bool, ready chan<- struct{}) error {
	if err := rejectAmbient(); err != nil {
		return err
	}
	release, err := acquire(c)
	if err != nil {
		return err
	}
	defer release()
	authKey := ""
	if bootstrap {
		if authKey, err = bootstrapKey(c.KeyFile); err != nil {
			return err
		}
	}
	// The pinned release routes log uploads through a noop transport in this supported mode.
	// This affects only the explicitly launched helper process, never host VPN configuration.
	prepareProcess()
	diag, closeLog, err := helperDiagnostics(c.LogPath)
	if err != nil {
		return err
	}
	defer func() { _ = closeLog() }()
	// Whatever this process logs through the standard logger is bounded as well: a build that turns on tsnet's
	// loggers writes there too, and that is the output that filled a disk (#777).
	log.SetOutput(diag)
	log.SetFlags(0)
	srv := &tsnet.Server{Dir: c.Dir, Hostname: c.Hostname, ControlURL: c.ControlURL, Ephemeral: false, AuthKey: authKey, UserLogf: diag.Logf, Logf: diag.Logf}
	if err := srv.Start(); err != nil {
		return errors.New("embedded node could not start; inspect state permissions and configuration")
	}
	client, err := srv.LocalClient()
	if err != nil {
		_ = srv.Close()
		return errHelper
	}
	if bootstrap {
		startup, cancel := context.WithTimeout(ctx, timeout(c))
		_, err = srv.Up(startup)
		cancel()
		if err != nil {
			_ = srv.Close()
			return errors.New("tailnet bootstrap did not become ready before deadline; check key, approval and control plane")
		}
	}
	node := &realNode{srv, client}
	served, stopServe := context.WithCancel(ctx)
	defer stopServe()
	gaveUp := make(chan struct{})
	watched := make(chan struct{})
	go func() {
		defer close(watched)
		watchControl(served, node, controlPoll, controlGiveUp, diag.always, func() { close(gaveUp); stopServe() })
	}()
	err = serveOwned(served, c, node, ready)
	stopServe()
	<-watched
	if err != nil {
		return err
	}
	select {
	case <-gaveUp:
		return errNoControl
	default:
		return nil
	}
}

// watchControl ends the helper when its node has had no control connection for giveUp (#777). A helper that
// retries for hours is one nobody sees: it says so once, through note, which the throttle does not hold back, and
// the next session start or memcue connect up starts a new one. A node that never gets its first connection counts
// as down from the start, so a hopeless key or an unreachable control server ends the same way.
func watchControl(ctx context.Context, n node, every, giveUp time.Duration, note func(string, ...any), end func()) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	down := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		state, err := statusWithin(ctx, n, every)
		if err == nil && state.Online {
			down = time.Now()
			continue
		}
		if wait := time.Since(down); wait >= giveUp {
			note("no control connection for %s: the helper exits; the next session start or memcue connect up starts a new one", wait.Round(time.Second))
			end()
			return
		}
	}
}

// statusWithin asks the node about itself no longer than wait: a status call that hangs must not keep a helper
// that has lost its control connection alive.
func statusWithin(ctx context.Context, n node, wait time.Duration) (State, error) {
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	return n.Status(ctx)
}
