package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/juev/memcue/internal/store"
)

// vet retires by rule a live fact of a kind the base no longer keeps and installation state of anything, not only of
// memcue; a rule, a decision and an extracted pitfall stay (#475).
func TestVetRetiresLegacyKindsAndState(t *testing.T) {
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
	event := add("Шлюз перезапущен командой gateway run --replace, после рестарта создана новая сессия.", "event", "")
	repo := add("Репозиторий держит плагин в plugins/hermes/memcue.", "repo", "")
	restart := add("The gateway was restarted 2026-10-19 23:30:02 with run --replace.", "environment", "agent")
	version := add("The Claude Code CLI on /srv is version 2.1.301.", "pitfall", "")
	rule := add("We stay on tool 1.2.3 until the schema migration lands.", "rule", "user")
	decision := add("Restart the gateway at 23:30 only, after the nightly job.", "decision", "user")
	pitfall := add("The CLI 2.1.301 on /srv drops the session on a revoked token; log in again.", "pitfall", "tool")
	_ = s.Close()

	var out bytes.Buffer
	if err := runVet([]string{"-no-model", "-dry-run"}, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"больше не хранит, 2", "о состоянии установки 2"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("dry run report lacks %q:\n%s", want, out.String())
		}
	}
	if f := factByID(t, event); f.Status != store.Active {
		t.Errorf("a dry run retired %s", event)
	}
	if err := runVet([]string{"-no-model"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{event: store.Retired, repo: store.Retired, restart: store.Retired,
		version: store.Retired, rule: store.Active, decision: store.Active, pitfall: store.Active} {
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
