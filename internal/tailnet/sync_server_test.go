package tailnet_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/juev/memcue/internal/store"
	"tailscale.com/derp/derpserver"
	"tailscale.com/types/key"
)

// TestSyncServerOverTheTailnet runs memcue server and two client machines built from the tree on a disposable
// Headscale tailnet of its own, never a live one (#714, R1–R3, R6, R7, R10, R16). Each client is a machine of its own:
// its own tailnet identity, state home and helper.
func TestSyncServerOverTheTailnet(t *testing.T) {
	hs, binary := os.Getenv("MEMCUE_FINAL_HEADSCALE_BINARY"), os.Getenv("MEMCUE_TEST_BIN")
	if hs == "" || binary == "" {
		t.Skip("requires an owned Headscale binary (MEMCUE_FINAL_HEADSCALE_BINARY) and a built CLI (MEMCUE_TEST_BIN)")
	}
	// Cleanups run after the test returns and stop the helpers through the CLI, so the context ends last.
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	t.Cleanup(cancel)
	root, err := os.MkdirTemp(os.TempDir(), "sync-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("fixture kept for inspection: %s", root)
			return
		}
		_ = os.RemoveAll(root)
	})
	control, keyfile := syncControl(t, ctx, hs, root)
	common := []string{
		"MEMCUE_TAILSCALE_CONTROL_URL=" + control,
		"MEMCUE_TAILSCALE_ALLOW_LOOPBACK_CONTROL=1",
		"MEMCUE_TAILSCALE_AUTH_KEY_FILE=" + keyfile,
		"MEMCUE_DREAM_BACKGROUND=0",
		// A first login on this fixture takes about five seconds, the default limit of connect up.
		"MEMCUE_TAILSCALE_CONNECT_TIMEOUT=20s",
		"GIT_CEILING_DIRECTORIES=" + root,
	}
	srv := newSyncMachine(t, ctx, binary, filepath.Join(root, "server"), "memcue-server", common)
	srv.env = append(srv.env, "MEMCUE_FACTS="+filepath.Join(srv.dir, "facts.db"))
	a := newSyncMachine(t, ctx, binary, filepath.Join(root, "a"), "client-a", common)
	b := newSyncMachine(t, ctx, binary, filepath.Join(root, "b"), "client-b", common)
	for _, c := range []*syncMachine{a, b} {
		c.env = append(c.env, "MEMCUE_SERVER=memcue-server")
	}

	stopServer := srv.serve(true)
	for _, c := range []*syncMachine{a, b} {
		t.Cleanup(func() { c.off() })
		if out, err := c.run("connect", "up", "-bootstrap"); err != nil || !strings.Contains(out, "Tailnet ready") {
			t.Fatalf("%s: connect up: %v\n%s", c.name, err, out)
		}
		syncUntil(t, ctx, c.name+" first sync", 30*time.Second, func() bool {
			if s := c.status(); s.Sync.LastSync.IsZero() {
				c.last = s
				return false
			}
			return true
		}, func() string { return fmt.Sprintf("%+v", c.last) })
	}

	// 1. A write on A reaches the search of B within seconds (R2).
	start := time.Now()
	id := a.add("Prefer quasarfox fixtures for the sync scenarios.")
	var seen time.Duration
	syncUntil(t, ctx, "B finds A's write", 15*time.Second, func() bool {
		if b.finds("quasarfox") {
			seen = time.Since(start)
			return true
		}
		return false
	})
	t.Logf("R2: a write on A was found by search on B %v after the add returned", seen.Round(time.Millisecond))

	// 2. With the server down a write is stored, found at once and queued; it reaches B once the server is back (R1, R3).
	stopServer()
	offline := a.add("Prefer ravenkite fixtures while the server is down.")
	if !a.finds("ravenkite") {
		t.Fatal("R3: A's offline write is not found by A's own search")
	}
	syncUntil(t, ctx, "A queues the offline write", 10*time.Second, func() bool { return a.status().Sync.Outbox > 0 })
	srv.serve(false)
	back := time.Now()
	syncUntil(t, ctx, "A's outbox empties", 60*time.Second, func() bool { return a.status().Sync.Outbox == 0 })
	t.Logf("R3: A's outbox emptied %v after the server came back", time.Since(back).Round(time.Millisecond))
	// B's long poll of the stopped server may hang until the poll's own deadline, 30 s.
	syncUntil(t, ctx, "B finds the offline write after the server is back", 40*time.Second, func() bool { return b.finds("ravenkite") },
		func() string { return fmt.Sprintf("A %+v, B %+v", a.status(), b.status()) })
	t.Logf("R2 after a server restart: B found the offline write %v after the server came back", time.Since(back).Round(time.Millisecond))
	if f := b.fact(offline); f.Content != "Prefer ravenkite fixtures while the server is down." {
		t.Fatalf("R3: B holds %q for the offline write", f.Content)
	}

	// 3. Concurrent changes of different fields merge without loss (R5).
	a.off()
	b.off()
	a.must("correct", id, "Prefer quasarfox fixtures with reasons for the sync scenarios.")
	b.must("correct", "-expires", "2030-01-01", id, "Prefer quasarfox fixtures for the sync scenarios.")
	a.up()
	b.up()
	syncUntil(t, ctx, "both replicas hold both fields", 30*time.Second, func() bool {
		for _, c := range []*syncMachine{a, b} {
			f := c.fact(id)
			if f.Content != "Prefer quasarfox fixtures with reasons for the sync scenarios." || f.Expires != "2030-01-01" {
				return false
			}
		}
		return true
	})

	// 4. Concurrent corrections of content keep the losing text as a new fact (R6).
	textA, textB := "Prefer nebulamoth fixtures for content conflicts.", "Prefer orbitwren fixtures for content conflicts."
	conflict := a.add("Prefer plain fixtures for content conflicts.")
	syncUntil(t, ctx, "B has the conflict fact", 15*time.Second, func() bool { return b.finds("plain") })
	a.off()
	b.off()
	a.must("correct", conflict, textA)
	b.must("correct", conflict, textB)
	a.up()
	b.up()
	syncUntil(t, ctx, "both texts on both replicas", 30*time.Second, func() bool {
		for _, c := range []*syncMachine{a, b} {
			if !c.finds("nebulamoth") || !c.finds("orbitwren") {
				return false
			}
		}
		return true
	})
	winner := a.fact(conflict).Content
	if winner != textA && winner != textB {
		t.Fatalf("R6: the fact holds %q, neither of the concurrent texts", winner)
	}
	if got := b.fact(conflict).Content; got != winner {
		t.Fatalf("R6: the replicas disagree: A %q, B %q", winner, got)
	}

	// 5. Retire against a correction: the fact stays retired, and reopen brings back the corrected text (R7).
	retired := a.add("Prefer lumenhare fixtures for retire conflicts.")
	syncUntil(t, ctx, "B has the retire fact", 15*time.Second, func() bool { return b.finds("lumenhare") })
	a.off()
	b.off()
	a.must("retire", retired)
	b.must("correct", retired, "Prefer lumenhare fixtures with reasons for retire conflicts.")
	a.up()
	b.up()
	syncUntil(t, ctx, "the fact is retired on both replicas", 30*time.Second, func() bool {
		return a.fact(retired).Status == store.Retired && b.fact(retired).Status == store.Retired
	})
	a.must("reopen", retired)
	syncUntil(t, ctx, "B sees the reopened, corrected fact", 15*time.Second, func() bool {
		f := b.fact(retired)
		return f.Status == store.Active && f.Content == "Prefer lumenhare fixtures with reasons for retire conflicts."
	})

	// 6. session-start without a helper starts one and answers without waiting for the network (R1, R10).
	a.off()
	start = time.Now()
	if out, err := a.run("session-start"); err != nil {
		t.Fatalf("R10: session-start without a helper: %v\n%s", err, out)
	}
	took := time.Since(start)
	t.Logf("R10: session-start without a helper answered in %v", took.Round(time.Millisecond))
	if took > 2*time.Second {
		t.Fatalf("R10: session-start without a helper took %v", took)
	}
	syncUntil(t, ctx, "session-start started a helper that syncs", 30*time.Second, func() bool {
		s := a.status()
		return s.State == "Running" && s.Sync.Error == "" && !s.Sync.LastSync.IsZero()
	})
	b.add("Prefer cobaltlynx fixtures after the helper restart.")
	syncUntil(t, ctx, "A's new helper brings B's write", 15*time.Second, func() bool { return a.finds("cobaltlynx") })
}

// syncMachine is one machine of the fixture: memcue run with its own homes, tailnet identity and node name.
type syncMachine struct {
	t    *testing.T
	ctx  context.Context
	bin  string
	name string
	dir  string
	env  []string
	last syncStatus
}

func newSyncMachine(t *testing.T, ctx context.Context, bin, dir, name string, common []string) *syncMachine {
	t.Helper()
	for _, d := range []string{dir, filepath.Join(dir, "work"), filepath.Join(dir, "claude")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	var env []string
	for _, e := range os.Environ() {
		k, _, _ := strings.Cut(e, "=")
		if strings.HasPrefix(k, "MEMCUE_") || strings.HasPrefix(k, "TS_") || k == "XDG_STATE_HOME" || k == "CLAUDE_CONFIG_DIR" {
			continue
		}
		env = append(env, e)
	}
	env = append(env, common...)
	env = append(env,
		"XDG_STATE_HOME="+filepath.Join(dir, "state"),
		"CLAUDE_CONFIG_DIR="+filepath.Join(dir, "claude"),
		"MEMCUE_STATE_DIR="+filepath.Join(dir, "memcue-state"),
		"MEMCUE_TAILSCALE_STATE_DIR="+filepath.Join(dir, "ts"),
		"MEMCUE_TAILSCALE_HOSTNAME="+name,
	)
	return &syncMachine{t: t, ctx: ctx, bin: bin, name: name, dir: dir, env: env}
}

func (m *syncMachine) command(args ...string) *exec.Cmd {
	cmd := exec.CommandContext(m.ctx, m.bin, args...)
	cmd.Env, cmd.Dir = m.env, filepath.Join(m.dir, "work")
	return cmd
}

func (m *syncMachine) run(args ...string) (string, error) {
	out, err := m.command(args...).CombinedOutput()
	return string(out), err
}

func (m *syncMachine) must(args ...string) string {
	m.t.Helper()
	out, err := m.run(args...)
	if err != nil {
		m.t.Fatalf("%s: memcue %s: %v\n%s", m.name, strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(out)
}

func (m *syncMachine) add(text string) string {
	m.t.Helper()
	return m.must("add", "-scope", "global", "-source", "user", "--", text)
}

// finds reports whether search on this machine returns a fact with word.
func (m *syncMachine) finds(word string) bool {
	out, err := m.run("search", "-n", "20", word)
	return err == nil && strings.Contains(out, word)
}

// fact is the fact id as this machine's replica holds it; the zero Fact when it has none.
func (m *syncMachine) fact(id string) store.Fact {
	out, err := m.command("history", "-json", id).Output()
	if err != nil {
		return store.Fact{}
	}
	var versions []store.Fact
	if json.Unmarshal(out, &versions) != nil || len(versions) == 0 {
		return store.Fact{}
	}
	return versions[len(versions)-1]
}

type syncStatus struct {
	State   string `json:"state"`
	Version string `json:"version"`
	Sync    struct {
		LastSync time.Time `json:"last_sync"`
		Outbox   int       `json:"outbox"`
		Error    string    `json:"error"`
	} `json:"sync"`
}

func (m *syncMachine) status() syncStatus {
	var s syncStatus
	if out, err := m.command("connect", "status").Output(); err == nil {
		_ = json.Unmarshal(out, &s)
	}
	return s
}

// off stops this machine's helper and waits until its socket is gone.
func (m *syncMachine) off() {
	m.t.Helper()
	_, _ = m.run("connect", "off")
	syncUntil(m.t, m.ctx, m.name+" helper stops", 15*time.Second, func() bool {
		s := m.status()
		return s.State == "Stopped" || s.State == "Unavailable"
	})
}

// up starts this machine's helper again with the identity it already has.
func (m *syncMachine) up() {
	m.t.Helper()
	syncUntil(m.t, m.ctx, m.name+" connect up", 20*time.Second, func() bool {
		out, err := m.run("connect", "up")
		return err == nil && strings.Contains(out, "Tailnet ready")
	})
}

// serve runs memcue server on this machine and returns what stops it; stopping twice does nothing.
func (m *syncMachine) serve(bootstrap bool) func() {
	m.t.Helper()
	args := []string{"server"}
	if bootstrap {
		args = append(args, "-bootstrap")
	}
	log, err := os.OpenFile(filepath.Join(m.dir, "server.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		m.t.Fatal(err)
	}
	cmd := m.command(args...)
	cmd.Stdout, cmd.Stderr = log, log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		_ = log.Close()
		m.t.Fatalf("memcue server: %v", err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); _ = log.Close(); close(done) }()
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			<-done
		}
	}
	m.t.Cleanup(stop)
	return stop
}

func syncUntil(t *testing.T, ctx context.Context, label string, limit time.Duration, check func() bool, detail ...func() string) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("deadline of the test: %s", label)
		case <-time.After(50 * time.Millisecond):
		}
	}
	for _, d := range detail {
		label += ": " + d()
	}
	t.Fatalf("not within %v: %s", limit, label)
}

// syncControl runs a disposable Headscale with its own relay on loopback, its policy open to the server port, and
// returns its URL and the file of a reusable auth key.
func syncControl(t *testing.T, ctx context.Context, hs, root string) (string, string) {
	t.Helper()
	writeJSON := func(path string, value any) {
		t.Helper()
		b, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	port := func() string {
		t.Helper()
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = l.Close() }()
		return strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
	}
	controlPort := port()
	control := "http://127.0.0.1:" + controlPort
	relay := derpserver.New(key.NewNode(), func(string, ...any) {})
	relayHTTP := httptest.NewTLSServer(derpserver.Handler(relay))
	t.Cleanup(func() { relayHTTP.Close(); _ = relay.Close() })
	derpMap := filepath.Join(root, "derp.yaml")
	if err := os.WriteFile(derpMap, []byte(fmt.Sprintf(`regions:
  901:
    regionid: 901
    regioncode: owned
    regionname: Owned local relay
    nodes:
      - name: 901a
        regionid: 901
        hostname: 127.0.0.1
        ipv4: 127.0.0.1
        ipv6: none
        stunport: -1
        derpport: %d
        insecurefortests: true
`, relayHTTP.Listener.Addr().(*net.TCPAddr).Port)), 0o600); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(root, "headscale.json")
	writeJSON(config, map[string]any{
		"server_url": control, "listen_addr": "127.0.0.1:" + controlPort, "metrics_listen_addr": "127.0.0.1:" + port(), "grpc_listen_addr": "127.0.0.1:" + port(),
		"noise": map[string]any{"private_key_path": filepath.Join(root, "noise.key")}, "prefixes": map[string]any{"v4": "100.64.0.0/24", "v6": "fd7a:115c:a1e0::/64"},
		"database":    map[string]any{"type": "sqlite", "sqlite": map[string]any{"path": filepath.Join(root, "headscale.db")}},
		"unix_socket": filepath.Join(root, "headscale.sock"), "unix_socket_permission": "0600", "policy": map[string]any{"mode": "database"},
		"derp":                  map[string]any{"server": map[string]any{"enabled": false}, "urls": []string{}, "paths": []string{derpMap}, "auto_update_enabled": false},
		"dns":                   map[string]any{"magic_dns": true, "base_domain": "fixture.invalid", "override_local_dns": false, "nameservers": map[string]any{"global": []string{}}},
		"disable_check_updates": true, "logtail": map[string]any{"enabled": false}, "log": map[string]any{"level": "warn"},
	})
	log, err := os.OpenFile(filepath.Join(root, "headscale.log"), os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, hs, "-c", config, "serve")
	cmd.Stdout, cmd.Stderr = log, log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err = cmd.Start(); err != nil {
		_ = log.Close()
		t.Fatalf("headscale: %v", err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); _ = log.Close(); close(done) }()
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			<-done
		}
	})
	client := &http.Client{Timeout: time.Second}
	syncUntil(t, ctx, "headscale health", 20*time.Second, func() bool {
		res, err := client.Get(control + "/health")
		if err != nil {
			return false
		}
		_ = res.Body.Close()
		return res.StatusCode == http.StatusOK
	})
	call := func(args ...string) []byte {
		t.Helper()
		out, err := exec.CommandContext(ctx, hs, append([]string{"-c", config, "-o", "json"}, args...)...).Output()
		if err != nil {
			t.Fatalf("headscale %s: %v", args[0], err)
		}
		return out
	}
	var user struct {
		ID uint64 `json:"id"`
	}
	if err = json.Unmarshal(call("users", "create", "fixture"), &user); err != nil || user.ID == 0 {
		t.Fatalf("headscale user: %v", err)
	}
	var auth struct {
		Key string `json:"key"`
	}
	if err = json.Unmarshal(call("preauthkeys", "create", "--user", strconv.FormatUint(user.ID, 10), "--reusable", "--expiration", "1h"), &auth); err != nil || auth.Key == "" {
		t.Fatalf("headscale auth key: %v", err)
	}
	keyfile := filepath.Join(root, "auth.key")
	if err = os.WriteFile(keyfile, []byte(auth.Key), 0o600); err != nil {
		t.Fatal(err)
	}
	policy := filepath.Join(root, "policy.json")
	writeJSON(policy, map[string]any{"acls": []any{map[string]any{"action": "accept", "src": []string{"*"}, "dst": []string{"*:80"}}}})
	call("policy", "set", "--file", policy)
	return control, keyfile
}
