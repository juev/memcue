package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/juev/memcue/internal/extract"
)

// dreamDueNow leaves the base of env with one fact fresh since a dream an hour ago, so a dream is due.
func dreamDueNow(t *testing.T, dir string) string {
	t.Helper()
	state := filepath.Join(dir, "state", "memcue")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	dreamBase(t)
	if _, _, _, err := dreamDue(state, time.Hour); err != nil { // the first check marks the base
		t.Fatal(err)
	}
	if err := markDream(state, time.Now().Add(-61*time.Minute)); err != nil {
		t.Fatal(err)
	}
	addFact(t, "Workflow nightly-sync.yml в example/nightly-sync ночью подписывает bot на все свои репозитории, кроме форков.",
		"nightly-sync", "workflow")
	return state
}

// countSpawns replaces the start of the background dream with a counter.
func countSpawns(t *testing.T) *int {
	t.Helper()
	n := 0
	spawnDream = func(string) error { n++; return nil }
	return &n
}

func sessionStartPart(t *testing.T, dir string, part int) string {
	t.Helper()
	var out bytes.Buffer
	if err := runSessionStartPart(strings.NewReader(`{"cwd":"`+dir+`"}`), &out, part); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// With claude on PATH a due dream goes to the background (#569): one part of the session start starts it, no part
// says the dream is due, and the next session start within MEMCUE_DREAM_EVERY starts none, even when the dream it
// started failed.
func TestSessionStartRunsADueDreamInTheBackground(t *testing.T) {
	dir := env(t)
	state := dreamDueNow(t, dir)
	lookClaude = func() bool { return true }
	spawns := countSpawns(t)

	for _, part := range []int{2, 3} {
		if got := sessionStartPart(t, dir, part); strings.Contains(got, "wait for the dream") {
			t.Errorf("part %d names the dream: %s", part, got)
		}
	}
	if *spawns != 0 {
		t.Fatalf("parts 2 and 3 started %d dreams", *spawns)
	}
	if got := sessionStartPart(t, dir, 1); strings.Contains(got, "wait for the dream") || *spawns != 1 {
		t.Fatalf("part 1: %d dreams started, out %s", *spawns, got)
	}
	if _, err := os.Stat(filepath.Join(state, "dream.started")); err != nil {
		t.Errorf("the start is not stamped: %v", err)
	}
	sessionStartPart(t, dir, 1)
	if *spawns != 1 {
		t.Errorf("a second session start within the interval started a dream: %d", *spawns)
	}

	// Turned off, the session start says the dream is due again and starts nothing.
	t.Setenv("MEMCUE_DREAM_BACKGROUND", "0")
	if got := sessionStartPart(t, dir, 1); !strings.Contains(got, "1 facts wait for the dream") || *spawns != 1 {
		t.Errorf("MEMCUE_DREAM_BACKGROUND=0: %d dreams started, out %s", *spawns, got)
	}
}

// Without an endpoint and without claude nothing can run the dream: the hint stays and nothing starts. An endpoint is
// enough without claude.
func TestBackgroundDreamNeedsAModel(t *testing.T) {
	dir := env(t)
	dreamDueNow(t, dir)
	spawns := countSpawns(t)
	if got := sessionStartPart(t, dir, 1); !strings.Contains(got, "1 facts wait for the dream") || *spawns != 0 {
		t.Fatalf("no model: %d dreams started, out %s", *spawns, got)
	}
	withAPI(t, "https://api.example.test")
	if got := sessionStartPart(t, dir, 1); strings.Contains(got, "wait for the dream") || *spawns != 1 {
		t.Errorf("endpoint: %d dreams started, out %s", *spawns, got)
	}
}

// Nothing due, nothing starts.
func TestBackgroundDreamOnlyWhenDue(t *testing.T) {
	dir := env(t)
	state := dreamDueNow(t, dir)
	if err := markDream(state, time.Now()); err != nil {
		t.Fatal(err)
	}
	lookClaude = func() bool { return true }
	spawns := countSpawns(t)
	sessionStartPart(t, dir, 1)
	if *spawns != 0 {
		t.Errorf("%d dreams started with nothing due", *spawns)
	}
}

// session-card, the call of Hermes and dsh, starts the background dream too and leaves out the line of the dream
// tools.
func TestSessionCardRunsADueDreamInTheBackground(t *testing.T) {
	dir := env(t)
	dreamDueNow(t, dir)
	withAPI(t, "https://api.example.test")
	spawns := countSpawns(t)
	var out bytes.Buffer
	if err := runSessionCardWith(strings.NewReader(`{"cwd":"`+dir+`"}`), &out, true); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "memcue_dream_prepare") || *spawns != 1 {
		t.Errorf("%d dreams started, card %s", *spawns, out.String())
	}
}

// dream -run -background runs on claude -p without an endpoint, which a plain -run refuses, and leaves quietly when
// another dream holds the lock.
func TestDreamRunBackground(t *testing.T) {
	dir := env(t)
	dreamBase(t)
	dreamedHoursAgo(t, dir)
	translateAll(t)
	calls := fakeDreamRun(t, nil)

	unlock, err := lockDream(filepath.Join(dir, "state", "memcue"))
	if err != nil {
		t.Fatal(err)
	}
	if err := runDream([]string{"-run", "-background"}, &bytes.Buffer{}); err != nil || *calls != 0 {
		t.Fatalf("under a held lock: err %v, %d calls", err, *calls)
	}
	if err := runDream([]string{"-run"}, &bytes.Buffer{}); !errors.Is(err, extract.ErrNoEndpoint) {
		t.Fatalf("plain -run without an endpoint: %v", err)
	}
	unlock()

	if err := runDream([]string{"-run", "-background"}, &bytes.Buffer{}); err != nil || *calls == 0 {
		t.Fatalf("err %v, %d calls", err, *calls)
	}
	for _, args := range [][]string{{"-background"}, {"-run", "-all", "-background"}} {
		if err := runDream(args, &bytes.Buffer{}); err == nil {
			t.Errorf("%v was accepted", args)
		}
	}
}

// The started dream is detached from the caller: spawnDream returns while the child still runs, and the child runs
// dream -run -background.
func TestSpawnDreamDetaches(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "args")
	// A stand-in for the binary: it records its arguments and outlives the call by a second.
	stub := filepath.Join(dir, "memcue")
	script := "#!/bin/sh\nsleep 1\necho \"$@\" > " + marker + "\n"
	if err := os.WriteFile(stub, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	old := dreamExecutable
	dreamExecutable = func() (string, error) { return stub, nil }
	t.Cleanup(func() { dreamExecutable = old })

	start := time.Now()
	if err := spawnDream(dir); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Fatalf("spawnDream waited %v for the dream", d)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if b, err := os.ReadFile(marker); err == nil && strings.TrimSpace(string(b)) == "dream -run -background" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the dream did not run with dream -run -background")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
