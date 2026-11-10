package main

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/juev/memcue/internal/tailnet"
)

func buildVersion(t *testing.T, v string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "memcue")
	if out, err := exec.Command("go", "build", "-p", "2", "-ldflags", "-X main.version="+v, "-o", bin, ".").
		CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

func helperVersion(dir string) string {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	st, err := tailnet.Admin(ctx, tailnet.Config{Dir: dir}, "status")
	if err != nil {
		return ""
	}
	return st.Version
}

func sessionStartOf(t *testing.T, bin string) time.Duration {
	t.Helper()
	cmd := exec.Command(bin, "session-start")
	cmd.Stdin = strings.NewReader(`{"cwd":""}`)
	start := time.Now()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s session-start: %v\n%s", bin, err, out)
	}
	return time.Since(start)
}

// R10 with real binaries: a session start without a helper starts one detached, and one of another version replaces
// the helper it finds.
func TestSessionStartStartsAndReplacesTheRealHelper(t *testing.T) {
	old, current := buildVersion(t, "1.0.0"), buildVersion(t, "2.0.0")
	env(t)
	identity := filepath.Join(t.TempDir(), "id")
	t.Setenv("MEMCUE_SERVER", "memcue-server")
	t.Setenv("MEMCUE_POSTGRES_DSN", "")
	t.Setenv("MEMCUE_TAILSCALE_MODE", "")
	t.Setenv("MEMCUE_TAILSCALE_STATE_DIR", identity)
	t.Setenv("MEMCUE_TAILSCALE_HOSTNAME", "memcue")
	t.Setenv("MEMCUE_TAILSCALE_CONTROL_URL", "http://127.0.0.1:1")
	t.Setenv("MEMCUE_TAILSCALE_ALLOW_LOOPBACK_CONTROL", "1")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, _ = tailnet.Admin(ctx, tailnet.Config{Dir: identity}, "off")
		waitConnectOwnerStopped(t, identity)
	})
	if took := sessionStartOf(t, old); took > 3*time.Second {
		t.Fatalf("session-start without a helper took %s", took)
	}
	waitFor(t, "the helper of 1.0.0", func() bool { return helperVersion(identity) == "1.0.0" })
	if took := sessionStartOf(t, current); took > 3*time.Second {
		t.Fatalf("session-start replacing a helper took %s", took)
	}
	waitFor(t, "the helper of 2.0.0", func() bool { return helperVersion(identity) == "2.0.0" })
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
