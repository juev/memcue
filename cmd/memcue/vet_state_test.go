package main

import (
	"bytes"
	"strings"
	"testing"
)

// The agent's add is live at once and skips vet.Rules, so add refuses installation state from the agent; the user's
// add and the agent's rule that quotes a version are stored (#475).
func TestAddRefusesAgentState(t *testing.T) {
	env(t)
	// What the agent saw in a tool's output is checked like what it concluded (#494).
	for _, source := range []string{"agent", "tool"} {
		err := runAdd([]string{"-source", source, "-kind", "environment", "--",
			"The gateway was restarted 2026-10-19 23:30:02 (PID 46005)."}, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "installation state (restart)") {
			t.Fatalf("%s state: err = %v, want a refusal naming the restart", source, err)
		}
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

// A shelf fact says what an outside source states about another system, so the agent's finding of a config default
// goes on the shelf, while the same text in an ordinary place is still installation state (#565). Hermes and DSH pass
// their shelf parameter as -shelf.
func TestAddKeepsShelfFindingOfAConfigDefault(t *testing.T) {
	env(t)
	const finding = "The vendor API's poller.interval defaults to 30 s, with a 10 s floor (API reference, 2026-10)."
	err := runAdd([]string{"-source", "agent", "--", finding}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "installation state (config default)") {
		t.Fatalf("ordinary place: err = %v, want a refusal naming the config default", err)
	}
	for _, place := range [][]string{{"-shelf", "research"}, {"-scope", "shelf:research"}} {
		for _, source := range []string{"agent", "tool"} {
			args := append([]string{"-source", source}, place...)
			var out bytes.Buffer
			if err := runAdd(append(args, "--", finding), &out); err != nil {
				t.Errorf("add %v: %v", args, err)
			} else if strings.TrimSpace(out.String()) == "" {
				t.Errorf("add %v printed no id", args)
			}
		}
	}
}
