package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
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
	ctx := resp.HookSpecificOutput.AdditionalContext
	if resp.HookSpecificOutput.HookEventName != "SessionStart" || !strings.Contains(ctx, "memcue add") {
		t.Errorf("got %+v", resp)
	}
	// The agent searches itself (#361): the note tells it to, and no longer promises facts with the prompt.
	// The flags stand before the words in the note (#481), the order the agent copies.
	if !strings.Contains(ctx, `memcue search [flags] "<words>"`) || strings.Contains(ctx, "stored facts") {
		t.Errorf("note does not ask the agent to search: %q", ctx)
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

func TestSessionStartCardCarriesRulesAndRepoDecisions(t *testing.T) {
	dir := env(t)
	repo := filepath.Join(dir, "shop")
	gitRepo(t, repo)
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
	decision := add("Решение: мержить squash в shop.", "decision", "repo:example.com/team/shop", "user", "")
	add("Решение чужого репозитория.", "decision", "repo:other", "user", "")
	add("Глобальное решение.", "decision", "global", "user", "")
	add("Ловушка в shop.", "pitfall", "repo:example.com/team/shop", "tool", "")
	add("Кандидат-правило.", "rule", "global", "user", store.Candidate)
	newest := add("Правило: не пушить в main.", "rule", "global", "user", "")
	_ = s.Close()

	ctx := sessionStartContext(t, repo, "startup", "s1")
	if !strings.Contains(ctx, "memcue add") || !strings.Contains(ctx, "rules and decisions from the base") {
		t.Fatalf("no note or card: %q", ctx)
	}
	for _, want := range []string{"по-русски", "не пушить в main", "мержить squash", "Global rules (learnt",
		"Rules and decisions of repo:example.com/team/shop (learnt",
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
	if len(rec.Facts) != 3 || rec.Tier != "example.com/team/shop" || rec.Source != "startup" || rec.Session != "s1" || rec.Chars == 0 {
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
			"", store.Meta{Kind: "rule", Scope: "global", Source: "agent"}); err != nil {
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

// The rules the user stated come first but stay within the budget (#487): those that do not fit are counted by the
// last line, and the facts nobody stated get only the room the stated ones leave.
func TestSessionCardBoundsTheStatedRules(t *testing.T) {
	dir := env(t)
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		for _, src := range []string{"user", "agent"} {
			text := fmt.Sprintf("Правило %s-%d: %s", src, i, strings.Repeat("слово ", 50))
			if _, err := s.AddWith(text, nil, "test 2026-10-20", "",
				store.Meta{Kind: "rule", Scope: "global", Source: src}); err != nil {
				t.Fatal(err)
			}
		}
	}
	_ = s.Close()
	card, _, _, fit, err := sessionCard(dir)
	if err != nil {
		t.Fatal(err)
	}
	if n := len([]rune(card)); n > cardBudget {
		t.Errorf("card is %d runes, budget %d", n, cardBudget)
	}
	stated, unstated := strings.Count(card, "- Правило user-"), strings.Count(card, "- Правило agent-")
	// A line is about 320 runes: the stated rules fill the card, and no line of the same length is left for the rest.
	if stated == 0 || stated == 12 || unstated != 0 {
		t.Errorf("%d stated and %d unstated rules on the card, want some stated and none unstated:\n%s", stated,
			unstated, card)
	}
	if want := cardHint(24 - stated); !strings.HasSuffix(card, "\n"+want+"\n") || fit.Omitted != 24-stated {
		t.Errorf("omitted %d, want the card to end with %q:\n%s", fit.Omitted, want, card)
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
	card, ids, _, _, err := renderCard(facts, cardBudget)
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
	_, firstIDs, _, _, err := renderCard(first, cardBudget)
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
	only, _, _, _, err := renderCard(facts[:20], cardBudget)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(only, "\n- "); got <= global {
		t.Errorf("global rules alone got %d lines, no more than %d next to the repository", got, global)
	}
}

// One rule split into two facts is one line on the card (#424): a fact whose opening repeats a line already placed is
// left out, the first in the given order is kept, and the card counts what it left out.
func TestSessionCardDropsARepeatedRule(t *testing.T) {
	facts := []store.Fact{
		{ID: "a1", Content: "Commit messages are always in English, whatever the system: the ticket prefix comes first.",
			Meta: store.Meta{Kind: "rule", Scope: "global", Source: "user"}},
		{ID: "b2", Content: "commit messages are always in English — whatever the system!! The prefix of the ticket goes first.",
			Meta: store.Meta{Kind: "rule", Scope: "global"}},
		{ID: "c3", Content: "Pull before a rebase.", Meta: store.Meta{Kind: "rule", Scope: "global"}},
	}
	card, ids, excluded, _, err := renderCard(facts, cardBudget)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a1", "c3"}; !slices.Equal(ids, want) {
		t.Errorf("ids %v, want %v", ids, want)
	}
	if strings.Count(card, "\n- ") != 2 || !strings.Contains(card, "the ticket prefix comes first") ||
		strings.Contains(card, "The prefix of the ticket") {
		t.Errorf("card keeps the second wording or loses the first:\n%s", card)
	}
	if excluded != 1 {
		t.Errorf("excluded = %d, want 1", excluded)
	}
}

// The card log counts what the card left out: a mirror of the built-in memory and a repeated line (#424), while chars
// stays the length of the card the model gets.
func TestSessionCardLogsTheExcludedFacts(t *testing.T) {
	dir := env(t)
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []struct{ text, source string }{
		{"Правило карты: тесты гонять перед коммитом.", "user"},
		{"Правило карты — тесты гонять перед коммитом!", "agent"},
		{"Заметка встроенной памяти о сервере.", "memory"},
	} {
		if _, err := s.AddWith(f.text, nil, "test 2026-10-20", "",
			store.Meta{Kind: "rule", Scope: "global", Source: f.source}); err != nil {
			t.Fatal(err)
		}
	}
	_ = s.Close()
	card, ids, excluded, _, err := sessionCard(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || excluded != 2 || strings.Contains(card, "встроенной памяти") {
		t.Fatalf("ids %v, excluded %d, card:\n%s", ids, excluded, card)
	}
	sessionStartContext(t, dir, "startup", "s1")
	raw, err := os.ReadFile(filepath.Join(dir, "state", "memcue", "card.log"))
	if err != nil {
		t.Fatal(err)
	}
	var rec cardRecord
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(raw))), &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Excluded != 2 || rec.Chars != len([]rune(card)) || !slices.Equal(rec.Facts, ids) {
		t.Errorf("card.log %+v, want excluded 2, chars %d, facts %v", rec, len([]rune(card)), ids)
	}
	// The body is the card without its header line: one heading and one fact.
	header, body, _ := strings.Cut(card, "\n")
	if rec.Budget != cardBudget || rec.Omitted != 0 || rec.Used != len([]rune(body)) || header == "" {
		t.Errorf("card.log %+v, want budget %d, used %d, omitted 0", rec, cardBudget, len([]rune(body)))
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
	gitRepo(t, repo)
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
	liveRule := add("LIVE-RULE: current.", "rule", "global", store.Today()) // the local day Expired judges by
	add("EXPIRED-DECISION: old.", "decision", "repo:example.com/team/shop", "2026-01-01")
	liveDecision := add("LIVE-DECISION: current.", "decision", "repo:example.com/team/shop", "")
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
	gitRepo(t, repo)

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
		recs[2].To != 23 || recs[2].Origin != "example.com/team/app 2026-10-19" {
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
	if err != nil || len(fs) != 2 || fs[0].Origin != "example.com/team/app 2026-10-19" || len(fs[0].Queries) != 1 {
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
	gitRepo(t, repo)
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
	if !strings.HasPrefix(got, "example.com/team/demo ") {
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

// extract-messages -cwd "" is a session with no project: the origin stays empty even inside a repository (#347).
func TestExtractMessagesEmptyCwdHasNoProject(t *testing.T) {
	env(t)
	repo := filepath.Join(t.TempDir(), "demo")
	gitRepo(t, repo)
	t.Chdir(repo)
	got := "unset"
	old := extractFn
	extractFn = func(origin, _ string) (extracted, error) {
		got = origin
		return extracted{}, nil
	}
	t.Cleanup(func() { extractFn = old })
	msgs := `[{"role":"user","content":"Факт про порт 3000."}]`
	if err := runExtractMessages([]string{"-cwd", "", "-min-chars", "0"}, strings.NewReader(msgs)); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "; ") { // the model is told the origin, then the host
		t.Errorf("origin = %q, want none before the host", got)
	}
	if err := runExtractMessages([]string{"-min-chars", "0"}, strings.NewReader(msgs)); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "example.com/team/demo ") {
		t.Errorf("no -cwd: origin = %q, want the working directory's project", got)
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

// The card of stated facts comes in parts of at most partBudget characters, one per SessionStart hook, so Claude Code
// never hides it in a file (#480). Every rule lands in exactly one part, the note and the hints open the first, a part
// that starts inside a group repeats the header and the heading, and only past the last part are lines left out, with
// a line that counts them.
func TestLayoutPartsKeepsEveryStatedFact(t *testing.T) {
	stated := func(global, repo int) []store.Fact {
		var facts []store.Fact
		for i := range global {
			facts = append(facts, store.Fact{ID: fmt.Sprintf("g%03d", i), Content: fmt.Sprintf("Правило %03d: %s", i,
				strings.Repeat("слово ", 50)), Meta: store.Meta{Kind: "rule", Scope: "global", Source: "user"}})
		}
		for i := range repo {
			facts = append(facts, store.Fact{ID: fmt.Sprintf("r%03d", i), Content: fmt.Sprintf("Решение %03d: %s", i,
				strings.Repeat("слово ", 50)), Meta: store.Meta{Kind: "decision", Scope: "repo:shop", Source: "user"}})
		}
		return facts
	}
	count := func(parts []string, prefix string) map[string]int {
		seen := map[string]int{}
		for _, p := range parts {
			for _, l := range strings.Split(p, "\n") {
				if strings.HasPrefix(l, prefix) {
					seen[l[:len(prefix)+4]]++
				}
			}
		}
		return seen
	}
	hints := []string{"memcue: 3 facts wait for the dream."}

	// The parts are tested here, not the budget: the card gets room for every fact.
	card, _, _, _, err := renderCard(stated(40, 20), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	parts, left := layoutParts(sessionNote, hints, card, startParts, partBudget)
	if len(parts) != startParts || left != 0 {
		t.Fatalf("%d runes of card: %d parts, %d left out; want %d parts and none left", len([]rune(card)), len(parts),
			left, startParts)
	}
	for i, p := range parts {
		if n := len([]rune(p)); n > partBudget {
			t.Errorf("part %d is %d runes, budget %d", i+1, n, partBudget)
		}
	}
	if !strings.HasPrefix(parts[0], sessionNote+"\n\n"+hints[0]+"\n\n") {
		t.Errorf("part 1 does not open with the note and the hints:\n%.300s", parts[0])
	}
	for i, p := range parts[1:] {
		heading := strings.SplitN(p, "\n", 3)[1]
		if !strings.HasPrefix(p, "memcue: rules and decisions from the base") ||
			!strings.HasPrefix(heading, "Global rules") && !strings.HasPrefix(heading, "Rules and decisions of ") {
			t.Errorf("part %d does not repeat the header and a heading:\n%.300s", i+2, p)
		}
	}
	for prefix, want := range map[string]int{"- Правило ": 40, "- Решение ": 20} {
		seen := count(parts, prefix)
		if len(seen) != want {
			t.Errorf("%q: %d distinct lines over the parts, want %d", prefix, len(seen), want)
		}
		for line, n := range seen {
			if n != 1 {
				t.Errorf("%q is in %d parts", line, n)
			}
		}
	}

	card, _, _, _, err = renderCard(stated(70, 30), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	parts, left = layoutParts(sessionNote, hints, card, startParts, partBudget)
	placed := len(count(parts, "- Правило ")) + len(count(parts, "- Решение "))
	if len(parts) != startParts || left == 0 || placed+left != 100 {
		t.Fatalf("100 facts: %d parts, %d placed, %d left out", len(parts), placed, left)
	}
	last := parts[len(parts)-1]
	if want := fmt.Sprintf("memcue: %d more rules and decisions are left out of the card", left); !strings.Contains(last, want) {
		t.Errorf("the last part does not say what is left out:\n%s", last[max(0, len(last)-300):])
	}
	for i, p := range parts {
		if n := len([]rune(p)); n > partBudget {
			t.Errorf("part %d is %d runes, budget %d", i+1, n, partBudget)
		}
	}
}

// Each SessionStart hook prints its own part: a small card fits in part 1 with the note, a later part with nothing to
// carry prints nothing, and part 0 is the whole text as before (#480). card.log gets one line per session start.
func TestSessionStartParts(t *testing.T) {
	dir := env(t)
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddWith("Правило: части карточки.", nil, "test 2026-10-26", "", store.Meta{Kind: "rule",
		Scope: "global", Source: "user"}); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	in, _ := json.Marshal(map[string]string{"session_id": "s1", "source": "startup", "cwd": dir})
	run := func(part int) string {
		t.Helper()
		var out bytes.Buffer
		if err := runSessionStartPart(bytes.NewReader(in), &out, part); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	if one := run(1); !strings.Contains(one, "Long-term memory") || !strings.Contains(one, "Правило: части карточки.") {
		t.Errorf("part 1: %s", one)
	}
	for _, part := range []int{2, 3} {
		if got := run(part); got != "" {
			t.Errorf("part %d of a small card printed %q", part, got)
		}
	}
	if whole := run(0); !strings.Contains(whole, "Long-term memory") || !strings.Contains(whole, "Правило: части карточки.") {
		t.Errorf("part 0: %s", whole)
	}
	if err := runSessionStartPart(bytes.NewReader(in), &bytes.Buffer{}, 4); err == nil {
		t.Error("-part 4: want an error")
	}
	state, err := stateDir()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(state, "card.log"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), "\n"); n != 2 {
		t.Errorf("card.log has %d lines for a part 1, 2, 3 and a part 0 run; want 2", n)
	}
}

// Forty stated rules do not fit the card (#487): it is cut at the budget, the rules left out are the tail of the given
// order, the oldest, and one bullet line counts them, as card.log does.
func TestRenderCardCutsTheOldestStatedRules(t *testing.T) {
	var facts []store.Fact
	for i := range 40 {
		facts = append(facts, store.Fact{ID: fmt.Sprintf("u%02d", i), Content: fmt.Sprintf("Правило %02d: %s", i,
			strings.Repeat("слово ", 20)), Meta: store.Meta{Kind: "rule", Scope: "global", Source: "user"}})
	}
	card, ids, _, fit, err := renderCard(facts, cardBudget)
	if err != nil {
		t.Fatal(err)
	}
	if n := len([]rune(card)); n > cardBudget {
		t.Errorf("card is %d runes, budget %d", n, cardBudget)
	}
	if len(ids) == 0 || len(ids) == 40 {
		t.Fatalf("%d of 40 stated rules on the card, want the budget to cut them", len(ids))
	}
	for i, id := range ids {
		if want := fmt.Sprintf("u%02d", i); id != want {
			t.Fatalf("ids %v are not the head of the given order", ids)
		}
	}
	omitted := 40 - len(ids)
	hint := cardHint(omitted)
	if strings.Count(card, "more do not fit the card budget") != 1 || !strings.HasSuffix(card, "\n"+hint+"\n") ||
		!strings.HasPrefix(hint, "- ") {
		t.Errorf("card does not end with one %q:\n%s", hint, card)
	}
	if fit.Omitted != omitted || fit.Budget != cardBudget {
		t.Errorf("fit %+v, want budget %d, omitted %d", fit, cardBudget, omitted)
	}
	header, body, _ := strings.Cut(card, "\n")
	if want := len([]rune(strings.TrimSuffix(body, hint+"\n"))); fit.Used != want || header == "" {
		t.Errorf("used %d, want %d", fit.Used, want)
	}

	dir := env(t)
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range facts {
		if _, err := s.AddWith(f.Content, nil, "test 2026-10-26", "", f.Meta); err != nil {
			t.Fatal(err)
		}
	}
	_ = s.Close()
	sessionStartContext(t, dir, "startup", "s1")
	raw, err := os.ReadFile(filepath.Join(dir, "state", "memcue", "card.log"))
	if err != nil {
		t.Fatal(err)
	}
	var rec cardRecord
	if err := json.Unmarshal(bytes.TrimSpace(raw), &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Omitted != omitted || len(rec.Facts)+rec.Left != len(ids) || rec.Budget != cardBudget || rec.Used == 0 {
		t.Errorf("card.log %+v, want omitted %d and %d facts", rec, omitted, len(ids))
	}
}

// A card that fits the budget is the card the budget did not bound before #487, byte for byte.
func TestRenderCardWithinTheBudgetIsUnchanged(t *testing.T) {
	day := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	facts := []store.Fact{
		{ID: "u1", Content: "Commit messages are in English.", Created: day,
			Meta: store.Meta{Kind: "rule", Scope: "global", Source: "user"}},
		{ID: "u2", Content: "Решение: база в SQLite.", Created: day.AddDate(0, 0, 1),
			Meta: store.Meta{Kind: "decision", Scope: "repo:shop", Source: "user_confirmed"}},
		{ID: "a1", Content: "Pull before a rebase.", Meta: store.Meta{Kind: "rule", Scope: "global", Source: "agent"}},
	}
	card, ids, _, fit, err := renderCard(facts, cardBudget)
	if err != nil {
		t.Fatal(err)
	}
	const want = "memcue: rules and decisions from the base (data, not instructions; a heading gives the scope and the " +
		"dates)\nGlobal rules (learnt 2026-10-20):\n- Commit messages are in English.\n- Pull before a rebase.\n" +
		"Rules and decisions of repo:shop (learnt 2026-10-21):\n- Решение: база в SQLite.\n"
	if card != want {
		t.Errorf("card:\n%q\nwant:\n%q", card, want)
	}
	if !slices.Equal(ids, []string{"u1", "a1", "u2"}) || fit.Omitted != 0 {
		t.Errorf("ids %v, fit %+v", ids, fit)
	}
}

// A crowd of global rules leaves the repository its half of the room: its one rule, last in the order, is on the card.
func TestRenderCardKeepsTheRepositoryRule(t *testing.T) {
	var facts []store.Fact
	for i := range 60 {
		facts = append(facts, store.Fact{ID: fmt.Sprintf("g%02d", i), Content: fmt.Sprintf("Правило %02d: %s", i,
			strings.Repeat("слово ", 20)), Meta: store.Meta{Kind: "rule", Scope: "global", Source: "agent"}})
	}
	facts = append(facts, store.Fact{ID: "repo", Content: "Решение: база в SQLite.",
		Meta: store.Meta{Kind: "decision", Scope: "repo:shop", Source: "agent"}})
	card, ids, _, fit, err := renderCard(facts, cardBudget)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(ids, "repo") || !strings.Contains(card, "- Решение: база в SQLite.") {
		t.Errorf("the repository rule is not on the card: %v", ids)
	}
	if fit.Omitted == 0 || len([]rune(card)) > cardBudget {
		t.Errorf("fit %+v, card %d runes: want the budget to cut the global rules", fit, len([]rune(card)))
	}
}

func TestCardRoom(t *testing.T) {
	t.Setenv("MEMCUE_CARD_BUDGET", "")
	_ = os.Unsetenv("MEMCUE_CARD_BUDGET")
	if got := cardRoom(); got != cardBudget {
		t.Errorf("unset: %d, want %d", got, cardBudget)
	}
	for v, want := range map[string]int{"1200": 1200, " 1200 ": 1200, "0": cardBudget, "-5": cardBudget,
		"abc": cardBudget, "": cardBudget} {
		t.Setenv("MEMCUE_CARD_BUDGET", v)
		if got := cardRoom(); got != want {
			t.Errorf("MEMCUE_CARD_BUDGET=%q: %d, want %d", v, got, want)
		}
	}
}

// The parts of the Claude Code session start carry every stated fact, outside the card budget (#480, #495), while
// session-card, the card of Hermes and DSH, keeps them inside it (#487).
func TestSessionStartPartsCarryEveryStatedRule(t *testing.T) {
	dir := env(t)
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	for i := range 40 {
		if _, err := s.AddWith(fmt.Sprintf("Правило %02d: %s", i, strings.Repeat("слово ", 50)), nil, "test 2026-10-26", "",
			store.Meta{Kind: "rule", Scope: "global", Source: "user"}); err != nil {
			t.Fatal(err)
		}
	}
	_ = s.Close()
	in, _ := json.Marshal(map[string]string{"session_id": "s1", "source": "startup", "cwd": dir})
	context := func(run func(io.Reader, io.Writer) error) string {
		t.Helper()
		var out bytes.Buffer
		if err := run(bytes.NewReader(in), &out); err != nil {
			t.Fatal(err)
		}
		if out.Len() == 0 {
			return ""
		}
		var resp struct {
			HookSpecificOutput struct {
				AdditionalContext string `json:"additionalContext"`
			} `json:"hookSpecificOutput"`
		}
		if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		return resp.HookSpecificOutput.AdditionalContext
	}
	rules := func(text string) int { return strings.Count(text, "\n- Правило ") }
	var parts string
	for part := 1; part <= startParts; part++ {
		parts += "\n" + context(func(r io.Reader, w io.Writer) error { return runSessionStartPart(r, w, part) })
	}
	if n := rules(parts); n != 40 || strings.Contains(parts, cardHintTail) {
		t.Errorf("the parts carry %d of 40 stated rules (hint: %v)", n, strings.Contains(parts, cardHintTail))
	}
	card := context(runSessionCard)
	if n := rules(card); n == 0 || n >= 40 || !strings.Contains(card, cardHintTail) {
		t.Errorf("session-card carries %d of 40 stated rules; want the budget of #487 to cut them, with the hint", n)
	}
}
