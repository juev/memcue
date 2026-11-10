package main

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/juev/memcue/internal/replica"
	"github.com/juev/memcue/internal/store"
)

const serverProfile = `{"Version":1,"Hostname":"memcue","ControlURL":"https://controlplane.tailscale.com","Host":"memcue-server","Port":"80","Server":true}`

// serverEnv is env in server mode with a private, empty tailnet identity. The helper is never really started: the
// starts are recorded.
func serverEnv(t *testing.T) (identity string, starts func() []bool) {
	t.Helper()
	env(t)
	identity = t.TempDir()
	if err := os.Chmod(identity, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MEMCUE_SERVER", "memcue-server")
	t.Setenv("MEMCUE_POSTGRES_DSN", "")
	t.Setenv("MEMCUE_TAILSCALE_MODE", "")
	t.Setenv("MEMCUE_TAILSCALE_STATE_DIR", identity)
	t.Setenv("MEMCUE_TAILSCALE_HOSTNAME", "memcue")
	t.Setenv("MEMCUE_TAILSCALE_CONTROL_URL", "https://controlplane.tailscale.com")
	var mu sync.Mutex
	var started []bool
	old := startHelper
	startHelper = func(takeover bool) error {
		mu.Lock()
		started = append(started, takeover)
		mu.Unlock()
		return nil
	}
	t.Cleanup(func() { startHelper = old })
	return identity, func() []bool { mu.Lock(); defer mu.Unlock(); return append([]bool(nil), started...) }
}

// fakeHelper answers the helper protocol in identity with reply and records the operations it was asked for.
func fakeHelper(t *testing.T, identity string, reply map[string]any) func() []string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(identity, "profile.json"), []byte(serverProfile), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("unix", filepath.Join(identity, "helper.sock"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(identity, "helper.sock"), 0o600); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var ops []string
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
			mu.Unlock()
			_ = json.NewEncoder(c).Encode(reply)
			_ = c.Close()
		}
	}()
	t.Cleanup(func() { _ = l.Close(); <-done })
	return func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), ops...) }
}

func sessionContext(t *testing.T, card bool) (string, time.Duration) {
	t.Helper()
	var out bytes.Buffer
	start := time.Now()
	run := runSessionStart
	if card {
		run = runSessionCard
	}
	if err := run(strings.NewReader(`{"cwd":""}`), &out); err != nil {
		t.Fatal(err)
	}
	took := time.Since(start)
	if out.Len() == 0 {
		return "", took
	}
	var resp struct {
		HookSpecificOutput struct {
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("session output %q: %v", out.String(), err)
	}
	return resp.HookSpecificOutput.AdditionalContext, took
}

func TestServerModeKeepsOneReplicaPerMachine(t *testing.T) {
	serverEnv(t)
	var out bytes.Buffer
	if err := runAdd([]string{"the replica is shared by every agent of the machine"}, &out); err != nil {
		t.Fatal(err)
	}
	// Another agent: another base and state directory, the same server.
	other := t.TempDir()
	t.Setenv("MEMCUE_FACTS", filepath.Join(other, "facts.db"))
	t.Setenv("MEMCUE_STATE_DIR", filepath.Join(other, "state"))
	out.Reset()
	if err := runSearch([]string{"replica shared every agent"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "the replica is shared by every agent of the machine") {
		t.Fatalf("another agent's search: %q", out.String())
	}
	path, err := replica.Path("memcue-server")
	if err != nil {
		t.Fatal(err)
	}
	r, err := store.OpenReplica(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	if out, err := r.Outbox(); err != nil || len(out) != 1 {
		t.Fatalf("the write waits in the machine's outbox: %d %v", len(out), err)
	}
	for _, p := range []string{filepath.Join(other, "facts.db"), os.Getenv("MEMCUE_FACTS")} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("server mode wrote a local base at %s: %v", p, err)
		}
	}
}

func TestServerModeRefusesLocalCommands(t *testing.T) {
	serverEnv(t)
	for _, args := range [][]string{{"dream", "-run"}, {"install"}, {"merge", "a", "b", "c"}, {"dump", "f"},
		{"migrate", "-text"}, {"migrate", "-repos"}} {
		if err := serverCommandAllowed(args); err == nil {
			t.Errorf("%v allowed in server mode", args)
		}
	}
	for _, args := range [][]string{{"migrate", "-server"}, {"import", "f.json"}, {"notes"}, {"reindex"}, {"search", "x"},
		{"connect", "up"}, {"server"}} {
		if err := serverCommandAllowed(args); err != nil {
			t.Errorf("%v refused in server mode: %v", args, err)
		}
	}
	t.Setenv("MEMCUE_POSTGRES_DSN", "postgres://u@db/memcue")
	if err := serverCommandAllowed([]string{"search", "x"}); err == nil || !strings.Contains(err.Error(), "not both") {
		t.Errorf("both backends: %v", err)
	}
}

func TestSessionStartWithoutAHelperStartsOneAndAnswersAtOnce(t *testing.T) {
	_, starts := serverEnv(t)
	t.Setenv("MEMCUE_DREAM_BACKGROUND", "1")
	lookClaude = func() bool { return true } // a dream could run here: server mode must still not start one
	spawned := false
	spawnDream = func(string) error { spawned = true; return nil }
	for _, card := range []bool{false, true} {
		text, took := sessionContext(t, card)
		if took > time.Second {
			t.Errorf("card=%v: session took %s without a helper", card, took)
		}
		if !strings.Contains(text, "replica of memcue-server has never synced") {
			t.Errorf("card=%v: no hint of a replica that never synced in %q", card, text)
		}
		if strings.Contains(text, "dream") {
			t.Errorf("card=%v: a dream hint in server mode: %q", card, text)
		}
	}
	if got := starts(); len(got) != 2 || got[0] || got[1] {
		t.Fatalf("helper starts %v, want two plain starts", got)
	}
	if spawned {
		t.Fatal("a client started a background dream in server mode")
	}
}

func TestSessionStartReplacesAHelperOfAnotherVersion(t *testing.T) {
	identity, starts := serverEnv(t)
	ops := fakeHelper(t, identity, map[string]any{"running": true, "state": "Running", "version": "0.0.1-old"})
	sessionContext(t, false)
	if got := ops(); len(got) != 2 || got[0] != "status" || got[1] != "off" {
		t.Fatalf("helper was asked %v, want status then off", got)
	}
	if got := starts(); len(got) != 1 || !got[0] {
		t.Fatalf("helper starts %v, want one takeover", got)
	}
}

func TestSessionStartReportsLoginAndRefusedChanges(t *testing.T) {
	identity, starts := serverEnv(t)
	sync := replica.Status{Outbox: 2, Rejected: []replica.Rejected{
		{OpID: "a", ID: "F1", Reason: "ahead"}, {OpID: "b", ID: "F2", Reason: "ahead"}}}
	fakeHelper(t, identity, map[string]any{"running": true, "state": "NeedsLogin", "version": version, "sync": sync})
	text, _ := sessionContext(t, true)
	for _, want := range []string{"waits for a login (NeedsLogin)", "memcue connect up",
		"the server refused 2 changes of this replica (2 ahead)"} {
		if !strings.Contains(text, want) {
			t.Errorf("no %q in %q", want, text)
		}
	}
	if got := starts(); len(got) != 0 {
		t.Fatalf("a running helper of this version was started again: %v", got)
	}
}
