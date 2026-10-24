package extract

import (
	"strings"
	"testing"
)

// The instructions of a packet carry the three tasks with their rules and the answer form with the packet's id, so
// the session's model needs nothing else to answer it.
func TestDreamInstructionsCarryTheTasksAndThePacket(t *testing.T) {
	got := DreamInstructions("2026-10-24T18:00:00Z")
	for _, want := range []string{splitPrompt, expandPrompt, dreamPrompt, `"packet": "2026-10-24T18:00:00Z"`,
		"memcue dream -apply"} {
		if !strings.Contains(got, want) {
			t.Errorf("instructions lack %.60q", want)
		}
	}
	if strings.Contains(got, "%s") {
		t.Error("an unfilled placeholder is left")
	}
}
