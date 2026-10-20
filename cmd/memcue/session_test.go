package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/juev/memcue/internal/extract"
	"github.com/juev/memcue/internal/store"
)

func TestSessionStartTellsWhereMemoryIs(t *testing.T) {
	var out bytes.Buffer
	if err := runSessionStart(strings.NewReader(`{"source":"startup"}`), &out); err != nil {
		t.Fatal(err)
	}
	var resp struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.HookSpecificOutput.HookEventName != "SessionStart" ||
		!strings.Contains(resp.HookSpecificOutput.AdditionalContext, "memcue add") {
		t.Errorf("got %+v", resp)
	}
}

func TestHookExtractRejectsPathInSessionID(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	for _, id := range []string{"../x", "a/b", "", ".."} {
		in := fmt.Sprintf(`{"session_id":%q,"transcript_path":"/t.jsonl","hook_event_name":"SessionEnd"}`, id)
		if err := runHookExtract(strings.NewReader(in)); err == nil {
			t.Errorf("session id %q accepted", id)
		}
	}
}

func TestHookExtractOffDoesNothing(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	t.Setenv("MEMCUE_EXTRACT", "0")
	if err := runHookExtract(strings.NewReader(`not even json`)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(state, "memcue")); !os.IsNotExist(err) {
		t.Errorf("state created: %v", err)
	}
}

// transcriptLines writes n user lines of about 300 characters each and returns the file.
func transcriptLines(t *testing.T, path string, n int) {
	transcriptLinesIn(t, path, "/src/app", n)
}

func transcriptLinesIn(t *testing.T, path, cwd string, n int) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	for i := 0; i < n; i++ {
		line := fmt.Sprintf(`{"type":"user","cwd":%q,"timestamp":"2026-10-19T10:00:00Z","message":{"content":"реплика %d %s"}}`,
			cwd, i, strings.Repeat("слово ", 50))
		if _, err := fmt.Fprintln(f, line); err != nil {
			t.Fatal(err)
		}
	}
}

func readLog(t *testing.T, state string) []extractRecord {
	t.Helper()
	f, err := os.Open(filepath.Join(state, "memcue", "extract.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var recs []extractRecord
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var r extractRecord
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		recs = append(recs, r)
	}
	return recs
}

func TestExtractSessionReadsOnlyNewPartAndStoresFacts(t *testing.T) {
	dir := env(t)
	state := filepath.Join(dir, "state")
	t.Setenv("XDG_STATE_HOME", state)
	var seen []string
	extractFn = func(origin, text string) (extracted, error) {
		seen = append(seen, text)
		return extracted{Facts: []extract.Fact{{Content: fmt.Sprintf("факт %d", len(seen)), Keywords: []string{"k"},
			Kind: "pitfall", Source: "user", Scope: "host:db1", Evidence: "реплика 0"}}, CostUSD: 0.1}, nil
	}
	t.Cleanup(func() { extractFn = extractText })
	tr := filepath.Join(dir, "s.jsonl")
	args := []string{"-session", "sess-1", "-transcript", tr, "-event", "PreCompact"}
	repo := filepath.Join(dir, "app")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}

	transcriptLinesIn(t, tr, repo, 3) // ~1700 chars: below minNew, waits
	if err := runExtractSession(args); err != nil {
		t.Fatal(err)
	}
	transcriptLinesIn(t, tr, repo, 10)
	if err := runExtractSession(args); err != nil {
		t.Fatal(err)
	}
	transcriptLinesIn(t, tr, repo, 10)
	if err := runExtractSession(args); err != nil {
		t.Fatal(err)
	}

	if len(seen) != 2 || !strings.Contains(seen[0], "реплика 0 ") || strings.Contains(seen[1], "реплика 12 ") ||
		!strings.Contains(seen[1], "реплика 0 ") {
		// the second batch of 10 restarts numbering at 0; it must not hold the first batch's last line
		t.Fatalf("model saw %d texts", len(seen))
	}
	recs := readLog(t, state)
	if len(recs) != 3 || recs[0].Skipped == "" || recs[1].From != 0 || recs[1].To != 13 || recs[2].From != 13 ||
		recs[2].To != 23 || recs[2].Origin != "app 2026-10-19" {
		t.Errorf("log %+v", recs)
	}
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	fs, err := s.Active()
	if err != nil || len(fs) != 2 || fs[0].Origin != "app 2026-10-19" {
		t.Errorf("facts %+v %v", fs, err)
	}
}

func TestExtractSessionKeepsOffsetOnFailure(t *testing.T) {
	dir := env(t)
	state := filepath.Join(dir, "state")
	t.Setenv("XDG_STATE_HOME", state)
	extractFn = func(string, string) (extracted, error) {
		return extracted{CostUSD: 0.2, Tokens: extract.Tokens{Input: 7}}, fmt.Errorf("rate limited")
	}
	t.Cleanup(func() { extractFn = extractText })
	tr := filepath.Join(dir, "s.jsonl")
	transcriptLines(t, tr, 10)
	args := []string{"-session", "sess-2", "-transcript", tr}
	if err := runExtractSession(args); err == nil {
		t.Fatal("want error")
	}
	extractFn = func(string, string) (extracted, error) { return extracted{}, nil }
	if err := runExtractSession(args); err != nil {
		t.Fatal(err)
	}
	recs := readLog(t, state)
	if len(recs) != 2 || recs[0].Error == "" || recs[0].CostUSD != 0.2 || recs[0].Tokens.Input != 7 || recs[1].From != 0 {
		t.Errorf("log %+v", recs)
	}
}

// The policy keeps what may be trusted and logs the rest with the reason; the model is told the host of the session.
func TestExtractedFactsPassThePolicy(t *testing.T) {
	dir := env(t)
	state := filepath.Join(dir, "state")
	t.Setenv("XDG_STATE_HOME", state)
	t.Setenv("MEMCUE_HOST", "db1")
	var where string
	extractFn = func(origin, text string) (extracted, error) {
		where = origin
		return extracted{Facts: []extract.Fact{
			{Content: "Пользователь пишет коммиты по-английски.", Kind: "rule", Source: "user", Scope: "global",
				Evidence: "порт базы demo"},
			{Content: "Обновили demo до 1.2.", Kind: "event", Source: "tool", Evidence: "порт базы demo"},
			{Content: "Агент решил держать базу на db1.", Kind: "decision", Source: "agent", Evidence: "порт базы demo"},
			{Content: "Порт demo 5433.", Kind: "environment", Source: "tool", Scope: "service:demo",
				Evidence: "порт 5433 открыт"},
		}}, nil
	}
	t.Cleanup(func() { extractFn = extractText })
	msgs := fmt.Sprintf(`[{"role":"user","content":%q}]`, strings.Repeat("порт базы demo ", 200))
	if err := runExtractMessages([]string{"-origin", "hermes 2026-10-20"}, strings.NewReader(msgs)); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(where, "hermes 2026-10-20; хост db1, ") {
		t.Errorf("model told %q", where)
	}
	recs := readLog(t, state)
	if len(recs) != 1 || recs[0].Facts != 1 || len(recs[0].Rejected) != 3 ||
		!strings.HasPrefix(recs[0].Rejected[0], "kind event is not kept: Обновили demo") ||
		!strings.HasPrefix(recs[0].Rejected[1], "a decision needs the user's words") ||
		!strings.HasPrefix(recs[0].Rejected[2], "evidence is not in a RESULT block") {
		t.Errorf("log %+v", recs)
	}
}

func TestExtractMessagesStoresFactsWithOrigin(t *testing.T) {
	dir := env(t)
	state := filepath.Join(dir, "state")
	t.Setenv("XDG_STATE_HOME", state)
	var seen string
	extractFn = func(origin, text string) (extracted, error) {
		seen = text
		return extracted{Facts: []extract.Fact{{Content: "Порт базы demo 55432.", Keywords: []string{"порт"},
			Kind: "environment", Source: "user", Scope: "service:demo", Evidence: "порт базы demo"}}}, nil
	}
	t.Cleanup(func() { extractFn = extractText })
	msgs := fmt.Sprintf(`[{"role":"user","content":%q},{"role":"assistant","content":"ок"}]`, strings.Repeat("порт базы demo ", 200))
	if err := runExtractMessages([]string{"-origin", "hermes 2026-10-19", "-session", "h1"}, strings.NewReader(msgs)); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(seen, "USER: порт базы demo") {
		t.Errorf("model saw %.40q", seen)
	}
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	fs, err := s.Active()
	if err != nil || len(fs) != 1 || fs[0].Origin != "hermes 2026-10-19" ||
		fs[0].Meta != (store.Meta{Kind: "environment", Scope: "service:demo", Source: "user", Evidence: "порт базы demo"}) {
		t.Errorf("facts %+v %v", fs, err)
	}
	if recs := readLog(t, state); len(recs) != 1 || recs[0].Session != "h1" || recs[0].Facts != 1 {
		t.Errorf("log %+v", recs)
	}
	if err := runExtractMessages(nil, strings.NewReader(`{"not":"a list"}`)); err == nil {
		t.Error("want error for bad input")
	}
}
