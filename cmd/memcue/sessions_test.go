package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// transcripts writes invented Claude Code transcripts under the config directory of the test: two sessions of one
// repository and one of another, with a tool result and a compaction summary that hold the words too.
func transcripts(t *testing.T, dir string) (older, newer, other, current time.Time) {
	t.Helper()
	root := filepath.Join(dir, "claude", "projects")
	older = time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	newer = older.Add(48 * time.Hour)
	other = older.Add(24 * time.Hour)
	current = older.Add(72 * time.Hour)
	line := func(kind, session, uuid, text, cwd string, at time.Time, extra string) string {
		b, _ := json.Marshal(text)
		return `{"type":"` + kind + `","sessionId":"` + session + `","uuid":"` + uuid + `","cwd":"` + cwd +
			`","timestamp":"` + at.Format(time.RFC3339Nano) + `","message":{"role":"` + kind + `","content":` + string(b) + `}` + extra + "}"
	}
	write := func(project, name string, lines ...string) {
		t.Helper()
		path := filepath.Join(root, project, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("-w-shop", "s-old.jsonl",
		line("user", "s-old", "u1", "Какой порт у прокси в shop?", "/w/shop", older, ""),
		line("assistant", "s-old", "a1", "Прокси shop слушает порт 8080, как в nginx.conf.", "/w/shop", older.Add(time.Minute), ""),
		`{"type":"user","sessionId":"s-old","uuid":"t1","cwd":"/w/shop","timestamp":"`+older.Add(2*time.Minute).Format(time.RFC3339Nano)+
			`","message":{"role":"user","content":[{"type":"tool_result","content":"PROXY_TOKEN=secret порт прокси 8080"}]}}`,
	)
	write("-w-shop", "s-new.jsonl",
		line("assistant", "s-new", "a2", "Порт прокси в shop переехал на 9090 после переезда nginx.", "/w/shop", newer, ""),
		line("user", "s-new", "u2", "This session is being continued: порт прокси 9090", "/w/shop", newer.Add(time.Minute), `,"isCompactSummary":true`),
	)
	write("-w-lists", "s-other.jsonl",
		line("user", "s-other", "u3", "В lists прокси нет, порт только у базы.", "/w/lists", other, ""),
	)
	write("-w-shop", "s-cur.jsonl",
		line("user", "s-cur", "u4", "Напомни порт прокси в shop", "/w/shop", current, ""),
	)
	return older, newer, other, current
}

// memcue sessions prints the newest passages of earlier sessions that hold the words, with their date and session,
// under a header that says they are history: the current session, a message at or after -before, tool output and a
// compaction summary stay out; -cwd keeps one repository's sessions; -json carries the same passages.
func TestSessionsFindsPassagesNewestFirst(t *testing.T) {
	dir := env(t)
	older, newer, other, current := transcripts(t, dir)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "s-cur")
	var out bytes.Buffer
	if err := runSessions([]string{"порт", "прокси"}, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) != 5 || !strings.HasPrefix(lines[0], "memcue sessions: passages of earlier sessions") ||
		!strings.Contains(lines[0], "history, not facts") {
		t.Fatalf("output:\n%s", got)
	}
	for i, want := range []string{
		newer.Local().Format("2006-01-02 15:04") + ", assistant in session s-new: Порт прокси в shop переехал на 9090",
		other.Local().Format("2006-01-02 15:04") + ", user in session s-other: В lists прокси нет",
		older.Add(time.Minute).Local().Format("2006-01-02 15:04") + ", assistant in session s-old: Прокси shop слушает порт 8080",
		older.Local().Format("2006-01-02 15:04") + ", user in session s-old: Какой порт у прокси в shop?",
	} {
		if !strings.Contains(lines[i+1], want) {
			t.Errorf("line %d %q, want it to hold %q", i+1, lines[i+1], want)
		}
	}
	for _, leak := range []string{"secret", "continued", "Напомни"} {
		if strings.Contains(got, leak) {
			t.Errorf("output holds %q:\n%s", leak, got)
		}
	}
	_ = current

	out.Reset()
	if err := runSessions([]string{"-before", newer.Format(time.RFC3339), "-cwd", "/w/shop", "-n", "1", "--", "порт прокси"}, &out); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); strings.Count(got, "\n- ") != 1 || !strings.Contains(got, "session s-old: Прокси shop слушает") {
		t.Errorf("-before and -cwd:\n%s", got)
	}

	out.Reset()
	if err := runSessions([]string{"-json", "-session", "s-cur,s-new", "порт", "прокси"}, &out); err != nil {
		t.Fatal(err)
	}
	var j struct {
		Passages []struct {
			Time, Session, Role, Text string
			Matched                   int
		}
		Terms    []string
		MinMatch int `json:"min_match"`
		Header   string
	}
	if err := json.Unmarshal(out.Bytes(), &j); err != nil {
		t.Fatal(err, out.String())
	}
	if len(j.Passages) != 3 || j.Passages[0].Session != "s-other" || j.Passages[1].Session != "s-old" || j.Passages[2].Role != "user" ||
		j.Passages[1].Role != "assistant" || j.Passages[1].Matched != 2 || j.Passages[1].Time != older.Add(time.Minute).Format(time.RFC3339) ||
		strings.Join(j.Terms, " ") != "порт прокси" || j.MinMatch != 2 || !strings.Contains(j.Header, "history, not facts") {
		t.Errorf("json %+v", j)
	}
}

// Nothing found, no words, and a bad bound are said plainly.
func TestSessionsSaysWhenNothingFits(t *testing.T) {
	dir := env(t)
	transcripts(t, dir)
	var out bytes.Buffer
	if err := runSessions([]string{"kubernetes", "ingress"}, &out); err != nil ||
		!strings.Contains(out.String(), `nothing in the history holds 2 of the words kubernetes, ingress`) {
		t.Errorf("%v %q", err, out.String())
	}
	out.Reset()
	if err := runSessions([]string{"the", "and"}, &out); err != nil || !strings.Contains(out.String(), "no words to look for") {
		t.Errorf("%v %q", err, out.String())
	}
	if err := runSessions([]string{"-before", "yesterday", "прокси"}, &out); err == nil || !strings.Contains(err.Error(), "-before") {
		t.Errorf("a bad -before: %v", err)
	}
	if err := runSessions(nil, &out); err == nil {
		t.Error("no words: no error")
	}
	// No transcripts at all is an empty history, not an error.
	env(t)
	out.Reset()
	if err := runSessions([]string{"прокси"}, &out); err != nil || !strings.Contains(out.String(), "nothing in the history") {
		t.Errorf("%v %q", err, out.String())
	}
}

// The note of the session start sends an empty search to memcue sessions and says a passage is history (#621).
func TestSessionNoteNamesTheHistorySearch(t *testing.T) {
	for _, want := range []string{"`memcue sessions \"<words>\"`", "history, not a fact", "store what holds with `memcue add`"} {
		if !strings.Contains(sessionNote, want) {
			t.Errorf("note lacks %q", want)
		}
	}
}
