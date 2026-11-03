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

var t0 = time.Date(2027, 1, 15, 12, 0, 0, 0, time.UTC)

// entry is one transcript line: a message of kind with content (a string or blocks) at t0 plus offset seconds.
func entry(kind string, content any, offset int, extra map[string]any) string {
	rec := map[string]any{"type": kind, "sessionId": "s1", "uuid": kind + "-" + time.Duration(offset).String(),
		"message": map[string]any{"role": kind, "content": content}, "cwd": "/w"}
	if offset >= 0 {
		rec["timestamp"] = t0.Add(time.Duration(offset) * time.Second).Format(time.RFC3339Nano)
	}
	for k, v := range extra {
		rec[k] = v
	}
	b, _ := json.Marshal(rec)
	return string(b)
}

func write(t *testing.T, root, name string, lines ...string) {
	t.Helper()
	path := filepath.Join(root, "p", name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestTermsLowercaseDistinctWithoutStopWords(t *testing.T) {
	if got := Terms("The Widget widget and the gizmo-tool v2.1"); !slices.Equal(got, []string{"widget", "gizmo-tool", "v2.1"}) {
		t.Errorf("terms %q", got)
	}
	if got := Terms("Как настроить ШЛЮЗ для виджета"); !slices.Equal(got, []string{"настроить", "шлюз", "виджета"}) {
		t.Errorf("terms %q", got)
	}
	if got := Terms("a - the"); len(got) != 0 {
		t.Errorf("terms of stop words alone %q", got)
	}
}

func TestMinMatch(t *testing.T) {
	var got []int
	for n := 1; n <= 8; n++ {
		got = append(got, MinMatch(n))
	}
	if want := []int{1, 2, 2, 2, 3, 3, 4, 4}; !slices.Equal(got, want) {
		t.Errorf("min match %v, want %v", got, want)
	}
}

func TestWindowIsBoundedAndCentredOnTheDensestSpot(t *testing.T) {
	text := "alpha " + strings.Repeat("x", 1000) + " widget gizmo " + strings.Repeat("y", 1000)
	w := Window(text, []string{"widget", "gizmo"}, 50)
	if !strings.Contains(w, "widget gizmo") || len([]rune(w)) > 2*50+2 || !strings.HasPrefix(w, "…") || !strings.HasSuffix(w, "…") {
		t.Errorf("window %q", w)
	}
	if got := Window("a widget here", []string{"widget"}, 50); got != "a widget here" {
		t.Errorf("short text %q", got)
	}
	// Characters, not bytes: a Cyrillic text of 60 characters is cut at 2*half characters.
	cyr := strings.Repeat("ю", 30) + " виджет " + strings.Repeat("я", 30)
	if got := Window(cyr, []string{"виджет"}, 20); len([]rune(got)) != 42 || !strings.Contains(got, "виджет") {
		t.Errorf("cyrillic window %q (%d characters)", got, len([]rune(got)))
	}
}

// The reading keeps the user's and the agent's text and leaves out what a program wrote: tool results, thinking,
// compaction summaries, skill bodies, command echoes, system reminders, sidechain briefs and the prompts of claude -p
// sessions; an entry without a time, a repeated entry and an oversized line are counted and left out.
func TestReadClaudeKeepsTextAndLeavesOutMachineText(t *testing.T) {
	root := t.TempDir()
	write(t, root, "s1.jsonl",
		entry("user", "Как настроить виджет?", 1, nil),
		entry("assistant", []map[string]any{{"type": "thinking", "thinking": "secret widget plan"},
			{"type": "text", "text": "Виджет настраивается через gizmo."}}, 2, nil),
		entry("user", []map[string]any{{"type": "tool_result", "content": "TOKEN=widget-secret"}}, 3, nil),
		entry("user", "<command-name>/widget</command-name>", 4, nil),
		entry("user", "This session is being continued from a previous conversation about the widget", 5, map[string]any{"isCompactSummary": true}),
		entry("user", "skill body about the widget", 6, map[string]any{"isMeta": true}),
		entry("user", "<system-reminder>widget reminder</system-reminder>", 7, nil),
		entry("user", "a sidechain brief about the widget", 8, map[string]any{"isSidechain": true}),
		entry("user", "a widget message without a time", -1, nil),
		entry("user", "Как настроить виджет?", 1, nil),
		"not json at all",
	)
	write(t, root, "s2.jsonl",
		`{"type":"summary","entrypoint":"sdk-cli"}`,
		entry("user", "the prompt a delegating agent wrote about the widget", 9, map[string]any{"sessionId": "s2", "uuid": "p9"}),
		entry("assistant", "the answer of the delegated run about the widget", 10, map[string]any{"sessionId": "s2", "uuid": "a10"}),
	)
	big := entry("user", strings.Repeat("widget ", MaxLine/7+1), 11, map[string]any{"uuid": "big"})
	write(t, root, "s3.jsonl", big, entry("user", "a small widget line after the big one", 12, map[string]any{"sessionId": "s3", "uuid": "small"}))
	msgs, st, err := ReadClaude(root)
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, m := range msgs {
		texts = append(texts, m.Role+": "+m.Text)
	}
	want := []string{"user: Как настроить виджет?", "assistant: Виджет настраивается через gizmo.",
		"assistant: the answer of the delegated run about the widget", "user: a small widget line after the big one"}
	if !slices.Equal(texts, want) {
		t.Errorf("kept %q\nwant %q", texts, want)
	}
	if st.Files != 3 || st.Oversized != 1 || st.BadLines != 1 || st.NoTime != 1 || st.Duplicates != 1 || st.Machine != 5 || st.Kept != 4 {
		t.Errorf("stats %+v", st)
	}
	if msgs[0].Session != "s1" || !msgs[0].Time.Equal(t0.Add(time.Second)) || msgs[0].CWD != "/w" {
		t.Errorf("message %+v", msgs[0])
	}
}

// A missing projects directory is an empty history, not an error.
func TestReadClaudeWithoutTranscripts(t *testing.T) {
	msgs, st, err := ReadClaude(filepath.Join(t.TempDir(), "none"))
	if err != nil || len(msgs) != 0 || st.Files != 0 {
		t.Errorf("%v %+v %v", msgs, st, err)
	}
}

// Search takes the newest messages that hold enough of the words, before the time, outside the sessions left out,
// one window each, and skips a window that repeats a newer one.
func TestSearchNewestFirstWithinTheTime(t *testing.T) {
	at := func(offset int) time.Time { return t0.Add(time.Duration(offset) * time.Second) }
	msgs := []Message{
		{UID: "a", Session: "s1", Role: "user", Text: "the widget port is 8080", Time: at(1)},
		{UID: "b", Session: "s2", Role: "assistant", Text: "The widget port is 8080", Time: at(2)},
		{UID: "c", Session: "s3", Role: "user", Text: "widget port moved to 9090", Time: at(3)},
		{UID: "d", Session: "cur", Role: "user", Text: "widget port of the current session", Time: at(4)},
		{UID: "e", Session: "s4", Role: "user", Text: "widget port written after the query", Time: at(10)},
		{UID: "f", Session: "s5", Role: "user", Text: "only widget here", Time: at(5)},
	}
	res := Search(msgs, "Widget PORT", at(10), map[string]bool{"cur": true}, 5, 200)
	if !slices.Equal(res.Terms, []string{"widget", "port"}) || res.MinMatch != 2 {
		t.Fatalf("terms %q min %d", res.Terms, res.MinMatch)
	}
	var uids []string
	for _, h := range res.Hits {
		uids = append(uids, h.Message.UID)
	}
	if want := []string{"c", "b"}; !slices.Equal(uids, want) {
		t.Errorf("hits %v, want %v", uids, want)
	}
	if res.Candidates != 3 || res.Future != 1 || res.Excluded != 1 || res.Repeats != 1 {
		t.Errorf("result %+v", res)
	}
	if res := Search(msgs, "widget", at(10), nil, 1, 200); len(res.Hits) != 1 || res.Hits[0].Message.UID != "f" {
		t.Errorf("one word, one hit: %+v", res.Hits)
	}
	if res := Search(msgs, "the", at(10), nil, 5, 200); len(res.Hits) != 0 || len(res.Terms) != 0 {
		t.Errorf("stop words alone found %+v", res.Hits)
	}
}

// A transcript deleted between the walk and its reading is no error: Claude Code drops old transcripts at its own
// start, and a search that meets the gap goes on with the rest.
func TestReadClaudeSkipsAFileDeletedSinceTheWalk(t *testing.T) {
	root := t.TempDir()
	write(t, root, "s1.jsonl", entry("user", "a widget message", 1, nil))
	write(t, root, "s2.jsonl", entry("user", "another widget message", 2, map[string]any{"uuid": "u2"}))
	if err := os.Remove(filepath.Join(root, "p", "s2.jsonl")); err != nil {
		t.Fatal(err)
	}
	// A dangling symlink reads as a file that is not there once the walk listed it.
	if err := os.Symlink(filepath.Join(root, "p", "s2.jsonl"), filepath.Join(root, "p", "s3.jsonl")); err != nil {
		t.Fatal(err)
	}
	msgs, st, err := ReadClaude(root)
	if err != nil || len(msgs) != 1 || st.Files != 1 {
		t.Errorf("messages %+v, stats %+v, err %v", msgs, st, err)
	}
}
