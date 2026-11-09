package main

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/juev/memcue/internal/tailnet"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"
)

// This fixture implements only private administration IPC, not a tailnet or DB.
func connectUpFixture(t *testing.T, state string, delay ...time.Duration) (string, func() []string) {
	t.Helper()
	return connectUpStates(t, []string{state}, delay...)
}

// connectUpStates answers the n-th status with states[n], and the last state after them, as a backend that moves
// from startup to its decided state does.
func connectUpStates(t *testing.T, states []string, delay ...time.Duration) (string, func() []string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MEMCUE_POSTGRES_DSN", "postgres://test:test@db.example.ts.net:5432/test?sslmode=disable")
	t.Setenv("MEMCUE_TAILSCALE_MODE", "embedded")
	t.Setenv("MEMCUE_TAILSCALE_STATE_DIR", dir)
	t.Setenv("MEMCUE_TAILSCALE_HOSTNAME", "memcue")
	t.Setenv("MEMCUE_TAILSCALE_CONTROL_URL", "https://controlplane.tailscale.com")
	profile := `{"Version":1,"Hostname":"memcue","ControlURL":"https://controlplane.tailscale.com","Host":"db.example.ts.net","Port":"5432"}`
	if err := os.WriteFile(filepath.Join(dir, "profile.json"), []byte(profile), 0600); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("unix", filepath.Join(dir, "helper.sock"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(dir, "helper.sock"), 0600); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var ops []string
	statuses := 0
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			var m struct {
				Op string `json:"op"`
			}
			_ = json.NewDecoder(c).Decode(&m)
			mu.Lock()
			ops = append(ops, m.Op)
			state := states[min(statuses, len(states)-1)]
			if m.Op == "status" {
				statuses++
			}
			mu.Unlock()
			response := map[string]any{"running": true, "state": state, "node_id": "fixture-node"}
			if m.Op == "login" {
				response["auth_url"] = "https://login.example.test/authorize"
			}
			if len(delay) > 0 {
				time.Sleep(delay[0])
			}
			_ = json.NewEncoder(c).Encode(response)
			_ = c.Close()
		}
	}()
	t.Cleanup(func() { _ = l.Close(); <-done })
	return dir, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), ops...) }
}

func TestConnectUpShowsLoginWithoutClaimingReadiness(t *testing.T) {
	_, ops := connectUpFixture(t, "NeedsLogin")
	var out bytes.Buffer
	if err := runConnect([]string{"up"}, &out); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte("https://login.example.test/authorize")) || bytes.Contains(out.Bytes(), []byte("Tailnet ready")) {
		t.Fatalf("pending login must show its URL, not claim readiness: %q", out.String())
	}
	got := ops()
	if len(got) != 2 || got[0] != "status" || got[1] != "login" {
		t.Fatalf("unexpected operations: %v", got)
	}
}

func TestConnectUpPendingApprovalDoesNotReauthorize(t *testing.T) {
	_, ops := connectUpFixture(t, "NeedsMachineAuth")
	var out bytes.Buffer
	if err := runConnect([]string{"up"}, &out); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte("NeedsMachineAuth")) || bytes.Contains(out.Bytes(), []byte("Tailnet ready")) {
		t.Fatalf("pending approval must not claim readiness: %q", out.String())
	}
	if got := ops(); len(got) != 1 || got[0] != "status" {
		t.Fatalf("pending approval must not reauthorize: %v", got)
	}
}

func TestConnectUpRejectsMismatchedOwner(t *testing.T) {
	_, ops := connectUpFixture(t, "Running")
	t.Setenv("MEMCUE_TAILSCALE_HOSTNAME", "other-profile")
	var out bytes.Buffer
	if err := runConnect([]string{"up"}, &out); err == nil {
		t.Fatal("up reused a different identity profile")
	}
	if len(ops()) != 0 {
		t.Fatalf("mismatched owner must not be contacted: %v", ops())
	}
}

func TestConnectUpStartsDetachedRealOwner(t *testing.T) {
	bin := buildBinary(t)
	dir := filepath.Join(t.TempDir(), "identity")
	t.Setenv("MEMCUE_POSTGRES_DSN", "postgres://test:test@db.example.ts.net:5432/test?sslmode=disable")
	t.Setenv("MEMCUE_TAILSCALE_MODE", "embedded")
	t.Setenv("MEMCUE_TAILSCALE_STATE_DIR", dir)
	t.Setenv("MEMCUE_TAILSCALE_HOSTNAME", "memcue")
	t.Setenv("MEMCUE_TAILSCALE_CONTROL_URL", "http://127.0.0.1:1")
	t.Setenv("MEMCUE_TAILSCALE_ALLOW_LOOPBACK_CONTROL", "1")
	t.Setenv("MEMCUE_TAILSCALE_CONNECT_TIMEOUT", "1s")
	// An ordinary up must not read a bootstrap file, even if configured.
	t.Setenv("MEMCUE_TAILSCALE_AUTH_KEY_FILE", filepath.Join(dir, "does-not-exist"))
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	t.Cleanup(func() {
		stop, done := context.WithTimeout(context.Background(), 3*time.Second)
		defer done()
		_, _ = tailnet.Admin(stop, tailnet.Config{Dir: dir}, "off")
		waitConnectOwnerStopped(t, dir)
	})
	cmd := exec.CommandContext(ctx, bin, "connect", "up")
	output, _ := cmd.CombinedOutput() // The unreachable disposable control cannot produce a login URL.
	state, err := tailnet.Admin(ctx, tailnet.Config{Dir: dir}, "status")
	if err != nil || !state.Running {
		t.Fatalf("up must leave a detached owner despite unavailable login: state=%+v err=%v output=%s", state, err, output)
	}
	if ctx.Err() != nil {
		t.Fatal("up retained caller output pipes or exceeded its startup deadline")
	}
	if _, err := os.Stat(filepath.Join(dir, "profile.json")); err != nil {
		t.Fatal("private durable profile missing")
	}
}

func TestConnectUpStartupRefusalsLeaveNoOwner(t *testing.T) {
	bin := buildBinary(t)
	for _, scenario := range []string{"bootstrap-timeout", "unsafe-directory", "mode-off", "invalid-dsn", "missing-dsn", "bad-argument"} {
		t.Run(scenario, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "identity")
			t.Setenv("MEMCUE_POSTGRES_DSN", "postgres://test:test@db.example.ts.net:5432/test?sslmode=disable")
			t.Setenv("MEMCUE_TAILSCALE_MODE", "embedded")
			t.Setenv("MEMCUE_TAILSCALE_STATE_DIR", dir)
			t.Setenv("MEMCUE_TAILSCALE_CONTROL_URL", "http://127.0.0.1:1")
			t.Setenv("MEMCUE_TAILSCALE_ALLOW_LOOPBACK_CONTROL", "1")
			t.Setenv("MEMCUE_TAILSCALE_CONNECT_TIMEOUT", "1s")
			facts := filepath.Join(t.TempDir(), "untouched.db")
			t.Setenv("MEMCUE_FACTS", facts)
			args := []string{"connect", "up"}
			switch scenario {
			case "bootstrap-timeout":
				key := filepath.Join(t.TempDir(), "bootstrap.key")
				if err := os.WriteFile(key, []byte("synthetic-not-a-real-key"), 0600); err != nil {
					t.Fatal(err)
				}
				t.Setenv("MEMCUE_TAILSCALE_AUTH_KEY_FILE", key)
				args = append(args, "-bootstrap")
			case "unsafe-directory":
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(dir, 0755); err != nil {
					t.Fatal(err)
				}
			case "mode-off":
				t.Setenv("MEMCUE_TAILSCALE_MODE", "off")
			case "invalid-dsn":
				t.Setenv("MEMCUE_POSTGRES_DSN", "invalid-secret-sentinel")
			case "missing-dsn":
				t.Setenv("MEMCUE_POSTGRES_DSN", "")
			case "bad-argument":
				args = append(args, "unexpected")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			output, err := exec.CommandContext(ctx, bin, args...).CombinedOutput()
			if err == nil || ctx.Err() != nil {
				t.Fatalf("expected bounded refusal, err=%v deadline=%v", err, ctx.Err())
			}
			if bytes.Contains(output, []byte("invalid-secret-sentinel")) || bytes.Contains(output, []byte("synthetic-not-a-real-key")) {
				t.Fatal("startup diagnostics leaked a configured secret")
			}
			if state, err := tailnet.Admin(ctx, tailnet.Config{Dir: dir}, "status"); err == nil && state.Running {
				_, _ = tailnet.Admin(ctx, tailnet.Config{Dir: dir}, "off")
				t.Fatal("refused startup left a live owner")
			}
			waitConnectOwnerStopped(t, dir)
			if _, err := os.Stat(facts); !os.IsNotExist(err) {
				t.Fatal("connect touched the local fact base")
			}
		})
	}
}

func waitConnectOwnerStopped(t *testing.T, dir string) {
	t.Helper()
	lock, err := os.OpenFile(filepath.Join(dir, "owner.lock"), os.O_RDWR, 0)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Close() }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
			_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("helper failed to release identity ownership")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestConnectUpHonorsLifecycleOutputDeadline(t *testing.T) {
	bin := buildBinary(t)
	connectUpFixture(t, "Running", 4200*time.Millisecond)
	t.Setenv("MEMCUE_TAILSCALE_CONNECT_TIMEOUT", "6s")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, bin, "connect", "up").CombinedOutput()
	if err != nil || !bytes.Contains(output, []byte("Tailnet ready")) {
		t.Fatalf("configured lifecycle deadline must allow a slow successful status: err=%v output=%s", err, output)
	}
}

func TestConnectUpReusesReadyHelperWithoutLogin(t *testing.T) {
	_, ops := connectUpFixture(t, "Running")
	var out bytes.Buffer
	if err := runConnect([]string{"up"}, &out); err != nil {
		t.Fatalf("up must reuse the ready helper: %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("Tailnet ready")) {
		t.Fatalf("readiness missing: %q", out.String())
	}
	got := ops()
	if len(got) != 1 || got[0] != "status" {
		t.Fatalf("up must not reauthorize or restart a running node: %v", got)
	}
}

// A helper restoring an approved identity passes NoState and Starting before Running: up waits for it instead of
// starting a login, which would regenerate the node key and void the approval (#697).
func TestConnectUpWaitsForAStartingHelperWithoutLogin(t *testing.T) {
	_, ops := connectUpStates(t, []string{"NoState", "Starting", "Starting", "Running"})
	var out bytes.Buffer
	if err := runConnect([]string{"up"}, &out); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte("Tailnet ready")) {
		t.Fatalf("a helper that becomes ready must be reported ready: %q", out.String())
	}
	for _, op := range ops() {
		if op != "status" {
			t.Fatalf("up must not log in while the helper is starting: %v", ops())
		}
	}
}

// A fresh identity settles on NeedsLogin: only then does up start a login and show its URL.
func TestConnectUpLogsInOnlyAfterTheHelperSettles(t *testing.T) {
	_, ops := connectUpStates(t, []string{"NoState", "NeedsLogin"})
	var out bytes.Buffer
	if err := runConnect([]string{"up"}, &out); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte("https://login.example.test/authorize")) {
		t.Fatalf("login URL missing: %q", out.String())
	}
	got := ops()
	if len(got) != 3 || got[0] != "status" || got[1] != "status" || got[2] != "login" {
		t.Fatalf("unexpected operations: %v", got)
	}
}

// A helper still starting at the deadline is reported as starting, with the control server to check, never logged in.
func TestConnectUpReportsAHelperStillStartingAsPending(t *testing.T) {
	_, ops := connectUpStates(t, []string{"Starting"})
	t.Setenv("MEMCUE_TAILSCALE_CONNECT_TIMEOUT", "1s")
	var out bytes.Buffer
	if err := runConnect([]string{"up"}, &out); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte("still starting (Starting)")) || bytes.Contains(out.Bytes(), []byte("Tailnet ready")) || bytes.Contains(out.Bytes(), []byte("device approval")) {
		t.Fatalf("a starting helper must be pending: %q", out.String())
	}
	for _, op := range ops() {
		if op != "status" {
			t.Fatalf("up must not log in while the helper is starting: %v", ops())
		}
	}
}

// Cancelling up while it waits for a starting helper reports the cancellation, not a pending state.
func TestConnectUpCancelledWhileStartingFails(t *testing.T) {
	_, ops := connectUpStates(t, []string{"Starting"})
	t.Setenv("MEMCUE_TAILSCALE_CONNECT_TIMEOUT", "10s")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	time.AfterFunc(300*time.Millisecond, cancel) // as SIGINT does
	var out bytes.Buffer
	start := time.Now()
	err := runConnectContext(ctx, []string{"up"}, &out)
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte("cancelled")) {
		t.Fatalf("err %v, output %q: want the cancellation", err, out.String())
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("up took %v after cancellation", took)
	}
	for _, op := range ops() {
		if op != "status" {
			t.Fatalf("up must not log in while the helper is starting: %v", ops())
		}
	}
}
