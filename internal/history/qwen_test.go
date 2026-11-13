package history

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// qwenEntry is one Qwen Code transcript line: a message of kind with parts at t0 plus offset seconds. The
// provenance is the one Qwen writes for the kind; extra overrides it (an empty string removes the field).
func qwenEntry(kind string, parts []map[string]any, offset int, extra map[string]any) string {
	rec := map[string]any{"type": kind, "sessionId": "q1", "uuid": kind + "-" + time.Duration(offset).String(),
		"provenance": map[string]string{"user": "real_user", "assistant": "assistant_output",
			"tool_result": "tool_result", "system": "system"}[kind],
		"message": map[string]any{"role": kind, "parts": parts}, "cwd": "/w"}
	if offset >= 0 {
		rec["timestamp"] = t0.Add(time.Duration(offset) * time.Second).Format(time.RFC3339Nano)
	}
	for k, v := range extra {
		if v == "" {
			delete(rec, k)
		} else {
			rec[k] = v
		}
	}
	b, _ := json.Marshal(rec)
	return string(b)
}

func qwenText(kind, text string, offset int, extra map[string]any) string {
	return qwenEntry(kind, []map[string]any{{"text": text}}, offset, extra)
}

// qwenWrite puts a transcript where Qwen keeps it: <project>/chats/<session>.jsonl under the projects directory.
func qwenWrite(t *testing.T, root, name string, lines ...string) {
	t.Helper()
	path := filepath.Join(root, "-w-shop", "chats", name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The reading keeps the user's and the agent's text and leaves out what a program wrote: tool results, thinking
// parts, system records, hook context and system reminders inside a message, user text of a provenance other than
// real_user, machine openings when the provenance is absent; an entry without a time, a repeated entry and an
// oversized line are counted and left out. Sidecar files are not transcripts.
func TestReadQwenKeepsTextAndLeavesOutMachineText(t *testing.T) {
	root := t.TempDir()
	qwenWrite(t, root, "q1.jsonl",
		qwenText("user", "Как настроить виджет?", 1, nil),
		qwenEntry("assistant", []map[string]any{{"text": "secret widget plan", "thought": true},
			{"text": "Виджет настраивается через gizmo."}}, 2, nil),
		qwenEntry("tool_result", []map[string]any{{"functionResponse": map[string]any{"response": "TOKEN=widget-secret"}}}, 3, nil),
		qwenText("system", "system widget note", 4, nil),
		qwenEntry("user", []map[string]any{{"text": "виджет prompt"},
			{"text": "<qwen:user-prompt-submit-context>\nhook widget context\n</qwen:user-prompt-submit-context>"}}, 5, nil),
		qwenText("user", "hook widget text", 6, map[string]any{"provenance": "hook"}),
		qwenText("user", "<command-name>/widget</command-name>", 7, map[string]any{"provenance": ""}),
		qwenText("user", "plain widget text without provenance", 8, map[string]any{"provenance": ""}),
		qwenText("user", "<system-reminder>widget reminder</system-reminder>", 9, nil),
		qwenText("user", "виджет без времени", -1, nil),
		qwenText("user", "Как настроить виджет?", 1, nil),
		qwenEntry("assistant", []map[string]any{{"text": "only a thought", "thought": true}}, 10, nil),
		qwenText("user", "", 11, nil),
		qwenEntry("user", []map[string]any{{"text": "first widget part"}, {"text": "second widget part"}}, 12, nil),
		"not json at all",
	)
	qwenWrite(t, root, "q2.jsonl",
		qwenText("user", "widget fallback session", 20, map[string]any{"sessionId": "", "uuid": ""}),
	)
	sidecar := filepath.Join(root, "-w-shop", "chats", "q2.title.json")
	if err := os.WriteFile(sidecar, []byte(`{"title":"widget sidecar"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	big := qwenText("user", strings.Repeat("widget ", MaxLine/7+1), 21, map[string]any{"uuid": "big"})
	qwenWrite(t, root, "q3.jsonl", big,
		qwenText("user", "small widget line after the big one", 22, map[string]any{"sessionId": "q3", "uuid": "small"}))

	msgs, st, err := ReadQwen(root)
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, m := range msgs {
		texts = append(texts, m.Role+": "+m.Text)
	}
	want := []string{"user: Как настроить виджет?", "assistant: Виджет настраивается через gizmo.",
		"user: виджет prompt", "user: plain widget text without provenance",
		"user: first widget part\nsecond widget part", "user: widget fallback session",
		"user: small widget line after the big one"}
	if !slices.Equal(texts, want) {
		t.Errorf("kept %q\nwant %q", texts, want)
	}
	if st.Files != 3 || st.Lines != 18 || st.Oversized != 1 || st.BadLines != 1 || st.NoTime != 1 ||
		st.Duplicates != 1 || st.Machine != 2 || st.Kept != 7 {
		t.Errorf("stats %+v", st)
	}
	if msgs[0].Session != "q1" || !msgs[0].Time.Equal(t0.Add(time.Second)) || msgs[0].CWD != "/w" {
		t.Errorf("fields %+v", msgs[0])
	}
	if msgs[5].Session != "q2" || !strings.HasPrefix(msgs[5].UID, "q2:") {
		t.Errorf("fallback fields %+v", msgs[5])
	}
	for _, m := range msgs {
		if strings.Contains(m.Text, "secret") || strings.Contains(m.Text, "hook widget context") {
			t.Errorf("machine text kept: %q", m.Text)
		}
	}
}

// No transcripts yet is an empty history, not an error.
func TestReadQwenMissingRootIsEmpty(t *testing.T) {
	msgs, st, err := ReadQwen(filepath.Join(t.TempDir(), "absent"))
	if err != nil || len(msgs) != 0 || st.Files != 0 {
		t.Errorf("msgs %d, stats %+v, err %v", len(msgs), st, err)
	}
}
