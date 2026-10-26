package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/juev/memcue/internal/store"
)

// vet retires by rule a live fact that records the moment of a process or a file, whatever its kind but a rule or a
// decision; a version, a path and a fact of an old kind are left alone, since on a live base they also hold real
// pitfalls and procedures (#475).
func TestVetRetiresMoments(t *testing.T) {
	env(t)
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	add := func(content, kind, source string) string {
		t.Helper()
		f, err := s.AddWith(content, nil, "x 2026-10-20", "", store.Meta{Kind: kind, Scope: "global", Source: source})
		if err != nil {
			t.Fatal(err)
		}
		return f.ID
	}
	restart := add("Шлюз перезапущен 2026-10-19 в 23:30:02 командой gateway run --replace (PID 46005).", "event", "")
	lines := add("File /srv/app/plugin.py (419 lines) is identical to main.", "repo", "")
	mtime := add("File /srv/app/store.db has not changed since 2026-10-19 16:04.", "environment", "agent")
	procedure := add("Create a GPU instance with `tool create instance <offer> --disk 32`.", "procedure", "")
	version := add("The Claude Code CLI on /srv is version 2.1.301.", "pitfall", "")
	decision := add("Restart the gateway only after the nightly job; it was restarted 2026-10-19 23:30:02 last.", "decision", "user")
	_ = s.Close()

	var out bytes.Buffer
	if err := runVet([]string{"-no-model", "-dry-run"}, &out); err != nil {
		t.Fatal(err)
	}
	if want := "mtime файла 3"; !strings.Contains(out.String(), want) {
		t.Errorf("dry run report lacks %q:\n%s", want, out.String())
	}
	if f := factByID(t, restart); f.Status != store.Active {
		t.Errorf("a dry run retired %s", restart)
	}
	if err := runVet([]string{"-no-model"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{restart: store.Retired, lines: store.Retired, mtime: store.Retired,
		procedure: store.Active, version: store.Active, decision: store.Active} {
		if f := factByID(t, id); f.Status != want {
			t.Errorf("%q: %s, want %s", f.Content, f.Status, want)
		}
	}
}

// The agent's add is live at once and skips vet.Rules, so add refuses installation state from the agent; the user's
// add and the agent's rule that quotes a version are stored (#475).
func TestAddRefusesAgentState(t *testing.T) {
	env(t)
	err := runAdd([]string{"-source", "agent", "-kind", "environment", "--",
		"The gateway was restarted 2026-10-19 23:30:02 (PID 46005)."}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "installation state (restart)") {
		t.Fatalf("agent state: err = %v, want a refusal naming the restart", err)
	}
	for _, args := range [][]string{
		{"-source", "agent", "-kind", "rule", "--", "We stay on tool 1.2.3 until the schema migration lands."},
		{"-source", "user", "-kind", "environment", "--", "The gateway was restarted 2026-10-19 23:30:02 (PID 46005)."},
	} {
		if err := runAdd(args, &bytes.Buffer{}); err != nil {
			t.Errorf("add %v: %v", args, err)
		}
	}
}
