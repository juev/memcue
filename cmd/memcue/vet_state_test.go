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
