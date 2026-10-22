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
	"time"

	"github.com/juev/memcue/internal/extract"
	"github.com/juev/memcue/internal/store"
)

func TestSessionStartTellsWhereMemoryIs(t *testing.T) {
	env(t) // no base: the note must not depend on the user's real memory
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

// sessionStartContext runs the SessionStart hook and returns the context it injected.
func sessionStartContext(t *testing.T, dir, source, session string) string {
	t.Helper()
	in, _ := json.Marshal(map[string]string{"session_id": session, "source": source, "cwd": dir})
	var out bytes.Buffer
	if err := runSessionStart(bytes.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	var resp struct {
		HookSpecificOutput struct {
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("output %q: %v", out.String(), err)
	}
	return resp.HookSpecificOutput.AdditionalContext
}

func TestSessionCardOnlyReturnsTheCard(t *testing.T) {
	dir := env(t)
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	f, err := s.AddWith("Правило: card only без note.", nil, "test 2026-10-20", "", store.Meta{Kind: "rule",
		Scope: "global", Source: "user"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	in, _ := json.Marshal(map[string]string{"session_id": "s-card", "source": "startup", "cwd": dir})
	var out bytes.Buffer
	if err := runSessionCard(bytes.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	var resp struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("output %q: %v", out.String(), err)
	}
	if resp.HookSpecificOutput.HookEventName != "SessionCard" || !strings.Contains(resp.HookSpecificOutput.AdditionalContext, f.Content) {
		t.Errorf("got %+v", resp)
	}
	if strings.Contains(resp.HookSpecificOutput.AdditionalContext, "memcue add") {
		t.Errorf("session-card leaked the session note: %q", resp.HookSpecificOutput.AdditionalContext)
	}
}

func TestSessionCardResetsSeenOnCompact(t *testing.T) {
	env(t)
	path, err := seenPath("s-card-compact")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("01ARZ3NDEKTSV4RRFFQ69G5FAV\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	in, _ := json.Marshal(map[string]string{"session_id": "s-card-compact", "source": "compact"})
	var out bytes.Buffer
	if err := runSessionCard(bytes.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("seen file still exists: %v", err)
	}
}

func TestSessionStartCardCarriesRulesAndRepoDecisions(t *testing.T) {
	dir := env(t)
	repo := filepath.Join(dir, "shop")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	add := func(content, kind, scope, source string, review string) store.Fact {
		f, err := s.AddWith(content, nil, "test 2026-10-20", "", store.Meta{Kind: kind, Scope: scope, Source: source,
			Review: review})
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	old := add("Правило: отвечать по-русски.", "rule", "global", "user", "")
	add("Правило только на маке.", "rule", "host:mac", "user", "")
	decision := add("Решение: мержить squash в shop.", "decision", "repo:shop", "user", "")
	add("Решение чужого репозитория.", "decision", "repo:other", "user", "")
	add("Глобальное решение.", "decision", "global", "user", "")
	add("Ловушка в shop.", "pitfall", "repo:shop", "tool", "")
	add("Кандидат-правило.", "rule", "global", "user", store.Candidate)
	newest := add("Правило: не пушить в main.", "rule", "global", "user", "")
	_ = s.Close()

	ctx := sessionStartContext(t, repo, "startup", "s1")
	if !strings.Contains(ctx, "memcue add") || !strings.Contains(ctx, "rules and decisions from the base") {
		t.Fatalf("no note or card: %q", ctx)
	}
	for _, want := range []string{"по-русски", "не пушить в main", "мержить squash", "scope: global", "scope: repo:shop",
		time.Now().UTC().Format(time.DateOnly)} {
		if !strings.Contains(ctx, want) {
			t.Errorf("card lacks %q: %q", want, ctx)
		}
	}
	for _, unwanted := range []string{"только на маке", "чужого репозитория", "Глобальное решение", "Ловушка в shop",
		"Кандидат-правило"} {
		if strings.Contains(ctx, unwanted) {
			t.Errorf("card carries %q: %q", unwanted, ctx)
		}
	}
	if strings.Index(ctx, "не пушить в main") > strings.Index(ctx, "по-русски") {
		t.Errorf("rules are not newest first: %q", ctx)
	}

	b, err := os.ReadFile(filepath.Join(dir, "state", "memcue", "card.log"))
	if err != nil {
		t.Fatal(err)
	}
	var rec cardRecord
	if err := json.Unmarshal(bytes.TrimSpace(b), &rec); err != nil {
		t.Fatalf("card.log %q: %v", b, err)
	}
	wantIDs := map[string]bool{newest.ID: true, old.ID: true, decision.ID: true}
	if len(rec.Facts) != 3 || rec.Tier != "shop" || rec.Source != "startup" || rec.Session != "s1" || rec.Chars == 0 {
		t.Errorf("card record %+v", rec)
	}
	for _, id := range rec.Facts {
		if !wantIDs[id] {
			t.Errorf("card logged %s", id)
		}
	}
}

func TestSessionStartCardStaysWithinBudget(t *testing.T) {
	dir := env(t)
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		if _, err := s.AddWith(fmt.Sprintf("Правило %d: %s", i, strings.Repeat("слово ", 80)), nil, "test 2026-10-20",
			"", store.Meta{Kind: "rule", Scope: "global", Source: "user"}); err != nil {
			t.Fatal(err)
		}
	}
	_ = s.Close()
	ctx := sessionStartContext(t, dir, "startup", "s1")
	i := strings.Index(ctx, "memcue: rules and decisions")
	if i < 0 {
		t.Fatalf("no card: %q", ctx)
	}
	card := ctx[i:]
	if n := len([]rune(card)); n > cardBudget {
		t.Errorf("card is %d runes, budget %d", n, cardBudget)
	}
	if strings.Count(card, "\n- ") >= 20 {
		t.Errorf("the budget carried every fact: %q", card)
	}
}

// Sixty global rules do not leave a repository with three lines: its facts take up to half of the card first, and the
// rest of the room is filled in the given order.
func TestSessionCardKeepsHalfForTheRepository(t *testing.T) {
	long := func(i int, what string) string {
		return fmt.Sprintf("%s %d: %s", what, i, strings.Repeat("слово ", 50))
	}
	var facts []store.Fact
	for i := range 20 {
		facts = append(facts, store.Fact{ID: fmt.Sprintf("g%02d", i), Content: long(i, "Правило"),
			Meta: store.Meta{Kind: "rule", Scope: "global"}})
	}
	for i := range 6 {
		facts = append(facts, store.Fact{ID: fmt.Sprintf("r%02d", i), Content: long(i, "Решение"),
			Meta: store.Meta{Kind: "decision", Scope: "repo:shop"}})
	}
	card, ids, err := renderCard(facts)
	if err != nil {
		t.Fatal(err)
	}
	if n := len([]rune(card)); n > cardBudget {
		t.Errorf("card is %d runes, budget %d", n, cardBudget)
	}
	repo, global := 0, 0
	for _, id := range ids {
		if strings.HasPrefix(id, "r") {
			repo++
		} else {
			global++
		}
	}
	// A line is about 335 runes: half the card holds five of them, and the global rules, first in the order, fill the
	// other half instead of the whole card.
	if repo != 5 || global != 5 {
		t.Errorf("repository %d lines, global %d: %v", repo, global, ids)
	}
	if strings.Index(card, "Правило 0:") > strings.Index(card, "Решение 0:") {
		t.Errorf("lines are not in the given order:\n%s", card)
	}

	// Half is a floor, not a cap: facts of the repository that come first in the order keep the room after it.
	first := append(append([]store.Fact(nil), facts[20:]...), facts[:20]...)
	_, firstIDs, err := renderCard(first)
	if err != nil {
		t.Fatal(err)
	}
	got := 0
	for _, id := range firstIDs {
		if strings.HasPrefix(id, "r") {
			got++
		}
	}
	if got <= repo {
		t.Errorf("a repository first in the order got %d lines, no more than its half (%d): %v", got, repo, firstIDs)
	}

	// Without facts of a repository the global rules get the whole card.
	only, _, err := renderCard(facts[:20])
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(only, "\n- "); got <= global {
		t.Errorf("global rules alone got %d lines, no more than %d next to the repository", got, global)
	}
}

func TestSessionStartWithoutBaseHasNoCard(t *testing.T) {
	dir := env(t)
	ctx := sessionStartContext(t, dir, "startup", "s1")
	if strings.Contains(ctx, "rules and decisions from the base") {
		t.Errorf("card without a base: %q", ctx)
	}
	if _, err := os.Stat(filepath.Join(dir, "state", "memcue", "card.log")); !os.IsNotExist(err) {
		t.Errorf("card.log without a card: %v", err)
	}
}

func TestSessionStartCardComesAgainAfterCompaction(t *testing.T) {
	dir := env(t)
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddWith("Правило: отвечать по-русски.", nil, "test 2026-10-20", "",
		store.Meta{Kind: "rule", Scope: "global", Source: "user"}); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	if ctx := sessionStartContext(t, dir, "compact", "s1"); !strings.Contains(ctx, "по-русски") {
		t.Errorf("no card after compaction: %q", ctx)
	}
}

// An expired rule or decision leaves the card and card.log, while a fact whose expiry is today stays: the card
// follows the same last-day rule as recall.
func TestSessionStartCardDropsExpiredFacts(t *testing.T) {
	dir := env(t)
	repo := filepath.Join(dir, "shop")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	add := func(content, kind, scope, expires string) store.Fact {
		t.Helper()
		f, err := s.AddWith(content, nil, "test 2026-10-20", expires, store.Meta{Kind: kind, Scope: scope, Source: "user"})
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	add("EXPIRED-RULE: old.", "rule", "global", "2026-01-01")
	liveRule := add("LIVE-RULE: current.", "rule", "global", time.Now().UTC().Format(time.DateOnly))
	add("EXPIRED-DECISION: old.", "decision", "repo:shop", "2026-01-01")
	liveDecision := add("LIVE-DECISION: current.", "decision", "repo:shop", "")
	_ = s.Close()

	ctx := sessionStartContext(t, repo, "startup", "s1")
	for _, want := range []string{"LIVE-RULE", "LIVE-DECISION"} {
		if !strings.Contains(ctx, want) {
			t.Errorf("card lacks %q: %q", want, ctx)
		}
	}
	for _, unwanted := range []string{"EXPIRED-RULE", "EXPIRED-DECISION"} {
		if strings.Contains(ctx, unwanted) {
			t.Errorf("card carries expired %q: %q", unwanted, ctx)
		}
	}
	b, err := os.ReadFile(filepath.Join(dir, "state", "memcue", "card.log"))
	if err != nil {
		t.Fatal(err)
	}
	var rec cardRecord
	if err := json.Unmarshal(bytes.TrimSpace(b), &rec); err != nil {
		t.Fatalf("card.log %q: %v", b, err)
	}
	want := map[string]bool{liveRule.ID: true, liveDecision.ID: true}
	if len(rec.Facts) != len(want) {
		t.Errorf("card.log ids %v, want %v", rec.Facts, want)
	}
	for _, id := range rec.Facts {
		if !want[id] {
			t.Errorf("card.log carries an expired or unknown fact %s", id)
		}
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
			Kind: "pitfall", Source: "user", Scope: "host:db1", Evidence: "реплика 0",
			Queries: []string{"какой порт"}}}, CostUSD: 0.1}, nil
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
	if act, _ := s.Active(); len(act) != 0 {
		t.Errorf("extracted facts are active: %+v", act)
	}
	fs, err := s.Candidates()
	if err != nil || len(fs) != 2 || fs[0].Origin != "app 2026-10-19" || len(fs[0].Queries) != 1 {
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

// SessionEnd is the last trigger: a short tail that PreCompact would wait on must still reach the model.
func TestExtractSessionEndExtractsAShortTail(t *testing.T) {
	dir := env(t)
	state := filepath.Join(dir, "state")
	t.Setenv("XDG_STATE_HOME", state)
	calls := 0
	extractFn = func(string, string) (extracted, error) {
		calls++
		return extracted{Facts: []extract.Fact{{Content: "Порт demo 55432.", Keywords: []string{"порт"},
			Kind: "environment", Source: "user", Scope: "service:demo", Evidence: "реплика 0"}}}, nil
	}
	t.Cleanup(func() { extractFn = extractText })
	tr := filepath.Join(dir, "s.jsonl")
	transcriptLinesIn(t, tr, dir, 3) // ~1700 chars, below minNew
	args := []string{"-session", "sess-end", "-transcript", tr, "-event", "SessionEnd"}
	if err := runExtractSession(args); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("short SessionEnd tail did not reach the model: %d calls", calls)
	}
	recs := readLog(t, state)
	if len(recs) != 1 || recs[0].To != 3 || recs[0].Facts != 1 || recs[0].Skipped != "" {
		t.Fatalf("log %+v", recs)
	}
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if fs, err := s.Candidates(); err != nil || len(fs) != 1 {
		t.Errorf("candidates %+v %v", fs, err)
	}
}

// One empty fact from the model must not lose the valid facts of the same extraction or stall the session offset.
func TestExtractSessionStoresValidFactsDespiteAnEmptyOne(t *testing.T) {
	dir := env(t)
	state := filepath.Join(dir, "state")
	t.Setenv("XDG_STATE_HOME", state)
	extractFn = func(string, string) (extracted, error) {
		return extracted{Facts: []extract.Fact{
			{Content: "Порт demo 55432.", Keywords: []string{"порт"}, Kind: "environment", Source: "user",
				Scope: "service:demo", Evidence: "реплика 0"},
			{Content: "  ", Kind: "pitfall", Source: "user", Scope: "global", Evidence: "реплика 0"},
		}}, nil
	}
	t.Cleanup(func() { extractFn = extractText })
	tr := filepath.Join(dir, "s.jsonl")
	transcriptLines(t, tr, 10)
	args := []string{"-session", "sess-empty", "-transcript", tr}
	if err := runExtractSession(args); err != nil {
		t.Fatal(err)
	}
	recs := readLog(t, state)
	if len(recs) != 1 || recs[0].To != 10 || recs[0].Facts != 1 || len(recs[0].Rejected) != 1 ||
		recs[0].Rejected[0] != "empty content" {
		t.Fatalf("log %+v", recs)
	}
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if fs, err := s.Candidates(); err != nil || len(fs) != 1 || fs[0].Content != "Порт demo 55432." {
		t.Errorf("candidates %+v %v", fs, err)
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
		}, Dropped: []string{"журнал обновления demo"}}, nil
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
	if len(recs) != 1 || recs[0].Facts != 1 || len(recs[0].Rejected) != 3 || recs[0].Dropped != "журнал обновления demo" ||
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
	fs, err := s.Candidates()
	if err != nil || len(fs) != 1 || fs[0].Origin != "hermes 2026-10-19" ||
		fs[0].Meta != (store.Meta{Kind: "environment", Scope: "service:demo", Source: "user", Evidence: "порт базы demo",
			Review: store.Candidate}) {
		t.Errorf("facts %+v %v", fs, err)
	}
	if recs := readLog(t, state); len(recs) != 1 || recs[0].Session != "h1" || recs[0].Facts != 1 {
		t.Errorf("log %+v", recs)
	}
	if err := runExtractMessages(nil, strings.NewReader(`{"not":"a list"}`)); err == nil {
		t.Error("want error for bad input")
	}
}

func TestExtractMessagesMinCharsLetsAFinalTailThrough(t *testing.T) {
	dir := env(t)
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	calls := 0
	extractFn = func(origin, text string) (extracted, error) {
		calls++
		return extracted{Facts: []extract.Fact{{Content: "Порт demo 55432.", Keywords: []string{"порт"},
			Kind: "environment", Source: "user", Scope: "service:demo", Evidence: "Порт demo 55432."}}}, nil
	}
	t.Cleanup(func() { extractFn = extractText })
	short := `[{"role":"user","content":"Порт demo 55432."}]`

	if err := runExtractMessages(nil, strings.NewReader(short)); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("short text reached the model without an override: %d calls", calls)
	}
	if err := runExtractMessages([]string{"-min-chars", "1"}, strings.NewReader(short)); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("short text did not reach the model with -min-chars 1: %d calls", calls)
	}
	if err := runExtractMessages([]string{"-min-chars", "-1"}, strings.NewReader(short)); err == nil {
		t.Error("negative -min-chars accepted")
	}
}

// Two bases on one machine get separate state directories, so a dream on one does not mark the other as dreamt.
func TestStateDirIsKeyedByBase(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(dir, "claude"))
	t.Setenv("MEMCUE_STATE_DIR", "")
	t.Setenv("MEMCUE_FACTS", filepath.Join(dir, "a.db"))
	a, err := stateDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("MEMCUE_FACTS", filepath.Join(dir, "b.db"))
	b, err := stateDir()
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatalf("two bases share the state directory %s", a)
	}
	if !strings.HasPrefix(a, filepath.Join(dir, "state", "memcue")) {
		t.Errorf("state dir %s is outside XDG_STATE_HOME", a)
	}
}

// The default base keeps the historical path, so an existing Claude Code/DSH install loses neither offsets nor the
// dream schedule after the upgrade.
func TestStateDirKeepsTheDefaultBase(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(dir, "claude"))
	t.Setenv("MEMCUE_STATE_DIR", "")
	t.Setenv("MEMCUE_FACTS", "")
	got, err := stateDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "state", "memcue"); got != want {
		t.Errorf("default state dir = %s, want %s", got, want)
	}
}

// An explicit directory wins, which is what a host with several bases or an unusual XDG layout needs.
func TestStateDirHonoursOverride(t *testing.T) {
	want := filepath.Join(t.TempDir(), "explicit")
	t.Setenv("MEMCUE_STATE_DIR", want)
	got, err := stateDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("state dir = %s, want %s", got, want)
	}
}

// extract-messages names the repository in origin when the plugin passes the workspace, and an explicit -origin
// still wins for callers that need the old format.
func TestExtractMessagesOriginFromCwd(t *testing.T) {
	env(t)
	repo := filepath.Join(t.TempDir(), "demo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	var got string
	old := extractFn
	extractFn = func(origin, _ string) (extracted, error) {
		got = origin
		return extracted{}, nil
	}
	t.Cleanup(func() { extractFn = old })
	msgs := `[{"role":"user","content":"Факт про порт 3000."}]`
	if err := runExtractMessages([]string{"-cwd", repo, "-min-chars", "0"}, strings.NewReader(msgs)); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "demo ") {
		t.Errorf("origin = %q, want the repository tier and a date", got)
	}
	if err := runExtractMessages([]string{"-cwd", repo, "-origin", "explicit 2026-10-21", "-min-chars", "0"},
		strings.NewReader(msgs)); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "explicit ") {
		t.Errorf("explicit origin = %q", got)
	}
}

// MEMCUE_EXTRACT=0 is the documented off switch; a host that calls extract-messages directly must honour it too.
func TestExtractMessagesHonoursTheOffSwitch(t *testing.T) {
	env(t)
	t.Setenv("MEMCUE_EXTRACT", "0")
	calls := 0
	extractFn = func(origin, text string) (extracted, error) {
		calls++
		return extracted{}, nil
	}
	t.Cleanup(func() { extractFn = extractText })
	msgs := `[{"role":"user","content":"Порт demo 55432."}]`
	if err := runExtractMessages([]string{"-min-chars", "0"}, strings.NewReader(msgs)); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("the model was called %d times with MEMCUE_EXTRACT=0", calls)
	}
}

func TestExtractSessionHonoursTheOffSwitch(t *testing.T) {
	env(t)
	t.Setenv("MEMCUE_EXTRACT", "0")
	if err := runExtractSession([]string{"-session", "s1", "-transcript", "/nonexistent"}); err != nil {
		t.Fatalf("the switch did not stop extraction before reading the transcript: %v", err)
	}
}
