package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/juev/memcue/internal/extract"
)

// lookClaude finds the `claude` command a background dream runs on without an endpoint, replaced in tests.
var lookClaude = func() bool {
	_, err := exec.LookPath("claude")
	return err == nil
}

// dreamExecutable is the binary a background dream runs, replaced in tests.
var dreamExecutable = os.Executable

// spawnDream starts `memcue dream -run -background` and does not wait for it, replaced in tests.
var spawnDream = func(dir string) error {
	exe, err := dreamExecutable()
	if err != nil {
		return err
	}
	// The child writes to a file, not to the caller's pipes: a host that reads the output of session-start or
	// session-card to its end (Hermes, dsh) would otherwise wait for the whole dream.
	out, err := os.OpenFile(filepath.Join(dir, "dream.out"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	cmd := exec.Command(exe, "dream", "-run", "-background")
	cmd.Stdout, cmd.Stderr = out, out // stdin stays /dev/null
	// Its own session, so it outlives the hook that started it and no signal to the host's process group reaches it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// dreamBackground reports whether a session start hands a due dream to a background `memcue dream -run` (#569): on
// unless MEMCUE_DREAM_BACKGROUND is false, and only with a model to run it on, the endpoint of MEMCUE_API_URL or
// `claude -p`. The session start then says nothing of the dream; without either the host's model keeps the hint.
func dreamBackground() bool {
	if on, err := strconv.ParseBool(strings.TrimSpace(os.Getenv("MEMCUE_DREAM_BACKGROUND"))); err == nil && !on {
		return false
	}
	return extract.API.URL != "" || lookClaude()
}

// startDream starts a background dream when one is due and none was started within MEMCUE_DREAM_EVERY, and returns at
// once. The start is stamped before the dream runs, so a dream that fails is tried again an interval later, not at
// every session start. A failure to start says nothing: the session must start either way, and dream.out keeps
// what the dream said.
func startDream() {
	if !dreamBackground() || !factsBase().exists() {
		return
	}
	dir, err := stateDir()
	if err != nil {
		return
	}
	every := dreamEveryFromEnv(dreamEvery)
	if b, err := os.ReadFile(filepath.Join(dir, "dream.started")); err == nil {
		if t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(b))); err == nil && time.Since(t) < every {
			return
		}
	}
	fresh, owed, _, err := dreamDue(dir, every)
	if err != nil || fresh == 0 && owed == 0 {
		return
	}
	stamp := []byte(time.Now().UTC().Format(time.RFC3339Nano) + "\n")
	if os.WriteFile(filepath.Join(dir, "dream.started"), stamp, 0o600) != nil {
		return
	}
	_ = spawnDream(dir)
}
