package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/juev/memcue/internal/extract"
	"github.com/juev/memcue/internal/store"
)

// fakeJudge answers every review call with verdicts for the ids it was given, and records the inputs.
func fakeJudge(t *testing.T, answer func(input string) (extract.ReviewResult, error)) *[]string {
	t.Helper()
	var calls []string
	old := reviewFn
	reviewFn = func(_ context.Context, input string) (extract.ReviewResult, error) {
		calls = append(calls, input)
		return answer(input)
	}
	t.Cleanup(func() { reviewFn = old })
	return &calls
}

// judgeByID answers keep for the ids in keep and drop for the rest.
func judgeByID(keep ...string) func(string) (extract.ReviewResult, error) {
	want := map[string]bool{}
	for _, id := range keep {
		want[id] = true
	}
	return func(input string) (extract.ReviewResult, error) {
		var req struct {
			Candidates []judgedCandidate `json:"candidates"`
		}
		if err := json.Unmarshal([]byte(input), &req); err != nil {
			return extract.ReviewResult{}, err
		}
		var r extract.ReviewResult
		for _, c := range req.Candidates {
			verdict := extract.DropVerdict
			if want[c.ID] {
				verdict = extract.KeepVerdict
			}
			r.Decisions = append(r.Decisions, extract.ReviewDecision{ID: c.ID, Verdict: verdict, Reason: "judged " + verdict})
		}
		r.CostUSD = 0.002
		return r, nil
	}
}

func vetFact(t *testing.T, content, origin, source string) string {
	t.Helper()
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	f, err := s.AddWith(content, []string{"x"}, origin, "", store.Meta{Kind: "pitfall", Scope: "host:test",
		Source: source, Review: store.Candidate})
	if err != nil {
		t.Fatal(err)
	}
	return f.ID
}

func vetStatus(t *testing.T, id string) store.Fact {
	t.Helper()
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	f, err := s.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// The rules decide a stale mirror and a test trace; the model decides the rest; every decision lands in vet.log.
func TestVetAppliesRulesAndJudge(t *testing.T) {
	dir := env(t)
	notes := filepath.Join(dir, "notes")
	if err := os.MkdirAll(notes, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(notes, "MEMORY.md"), []byte("Пользователь пишет коммиты по-английски."), 0o600); err != nil {
		t.Fatal(err)
	}
	mirror := vetFact(t, "Пользователь пишет коммиты по-английски.", "hermes-memory 2026-10-20", "memory")
	gone := vetFact(t, "В контейнере hermes нет CLI sqlite3, базу читают через python3.", "hermes-memory 2026-10-20", "memory")
	trace := vetFact(t, "Проект demo: база PostgreSQL 18 слушает порт 55432.", "test 2026-10-19", "tool")
	keep := vetFact(t, "Ключ DeepSeek берут из окружения процесса шлюза, а не из .env.", "hermes 2026-10-21", "tool")
	drop := vetFact(t, "Сегодня прогнали dream на живой базе, извлечение дало 9 фактов.", "hermes 2026-10-21", "tool")
	calls := fakeJudge(t, judgeByID(keep))

	var out bytes.Buffer
	if err := runVet([]string{"-memory", notes}, &out); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 {
		t.Fatalf("review calls %d", len(*calls))
	}
	var sent struct {
		Candidates []judgedCandidate `json:"candidates"`
	}
	if err := json.Unmarshal([]byte((*calls)[0]), &sent); err != nil || len(sent.Candidates) != 2 {
		t.Fatalf("input %s: %v", (*calls)[0], err)
	}
	if f := vetStatus(t, mirror); f.Review != "" || f.Status != store.Active {
		t.Errorf("live mirror %+v", f)
	}
	if f := vetStatus(t, gone); f.Status != store.Retired {
		t.Errorf("stale mirror %+v", f)
	}
	if f := vetStatus(t, trace); f.Status != store.Retired {
		t.Errorf("test trace %+v", f)
	}
	if f := vetStatus(t, keep); f.Review != "" {
		t.Errorf("judged keep %+v", f)
	}
	if f := vetStatus(t, drop); f.Status != store.Retired {
		t.Errorf("judged drop %+v", f)
	}
	if !strings.Contains(out.String(), "принято 2") || !strings.Contains(out.String(), "отклонено 3") {
		t.Errorf("report:\n%s", out.String())
	}

	logb, err := os.ReadFile(filepath.Join(dir, "state", "memcue", "vet.log"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(logb)), "\n")
	if len(lines) != 6 { // five candidates, then the summary
		t.Fatalf("log lines %d: %s", len(lines), logb)
	}
	var rec vetRecord
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &rec); err != nil {
		t.Fatalf("summary %q: %v", lines[len(lines)-1], err)
	}
	if rec.Candidates != 5 || rec.Approved != 2 || rec.Retired != 3 || rec.CostUSD != 0.002 {
		t.Errorf("summary %+v", rec)
	}
}

// A dry run decides and reports, and changes nothing.
func TestVetDryRunChangesNothing(t *testing.T) {
	env(t)
	id := vetFact(t, "Плагин отдаёт usage.log только при XDG_STATE_HOME в env контейнера.", "hermes 2026-10-21", "tool")
	fakeJudge(t, judgeByID(id))
	var out bytes.Buffer
	if err := runVet([]string{"-dry-run"}, &out); err != nil {
		t.Fatal(err)
	}
	if f := vetStatus(t, id); f.Review != store.Candidate {
		t.Errorf("dry run changed the base: %+v", f)
	}
	if !strings.Contains(out.String(), "Пробный прогон") {
		t.Errorf("report:\n%s", out.String())
	}
}

// A judge that fails or says nothing approves nothing: those candidates wait for the next run.
func TestVetJudgeFailureKeepsCandidates(t *testing.T) {
	env(t)
	id := vetFact(t, "Скрипт крона должен лежать в HERMES_HOME/scripts.", "hermes 2026-10-21", "tool")
	fakeJudge(t, func(string) (extract.ReviewResult, error) {
		return extract.ReviewResult{}, errors.New("endpoint returned no choices")
	})
	var out bytes.Buffer
	if err := runVet(nil, &out); err != nil {
		t.Fatal(err)
	}
	if f := vetStatus(t, id); f.Review != store.Candidate || f.Status != store.Active {
		t.Errorf("a failed judge changed the base: %+v", f)
	}
	if !strings.Contains(out.String(), "the judge failed") {
		t.Errorf("report:\n%s", out.String())
	}
}

// The rules alone need no endpoint and no model.
func TestVetNoModel(t *testing.T) {
	env(t)
	trace := vetFact(t, "Проект demo: база PostgreSQL 18 слушает порт 55432.", "test 2026-10-19", "tool")
	other := vetFact(t, "Ключ DeepSeek берут из окружения процесса шлюза.", "hermes 2026-10-21", "tool")
	calls := fakeJudge(t, judgeByID(other))
	var out bytes.Buffer
	if err := runVet([]string{"-no-model"}, &out); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 0 {
		t.Errorf("the model was called with -no-model")
	}
	if f := vetStatus(t, trace); f.Status != store.Retired {
		t.Errorf("test trace %+v", f)
	}
	if f := vetStatus(t, other); f.Review != store.Candidate {
		t.Errorf("undecided candidate %+v", f)
	}
	if !strings.Contains(out.String(), "осталось решать 1") {
		t.Errorf("report:\n%s", out.String())
	}
}

// A session reviews only the ids it stored, and only when MEMCUE_VET=1 and an endpoint is set: the extraction path
// must not start a model call on its own.
func TestMaybeVetGatesAndReviewsStoredIDs(t *testing.T) {
	env(t)
	old := extract.Judge.URL
	t.Cleanup(func() { extract.Judge.URL = old })
	newer := vetFact(t, "Ключ DeepSeek берут из окружения процесса шлюза.", "hermes 2026-10-21", "tool")
	older := vetFact(t, "Скрипт крона должен лежать в HERMES_HOME/scripts.", "hermes 2026-10-20", "tool")
	calls := fakeJudge(t, judgeByID(newer))

	maybeVet([]string{newer})
	if len(*calls) != 0 {
		t.Fatal("an unset MEMCUE_VET reviewed the queue")
	}
	t.Setenv("MEMCUE_VET", "1")
	maybeVet([]string{newer})
	if len(*calls) != 0 {
		t.Fatal("a missing endpoint reviewed the queue")
	}
	extract.Judge.URL = "http://127.0.0.1:1"
	maybeVet([]string{newer})
	if len(*calls) != 1 {
		t.Fatalf("review calls %d", len(*calls))
	}
	if f := vetStatus(t, newer); f.Review != "" {
		t.Errorf("the stored id %+v", f)
	}
	if f := vetStatus(t, older); f.Review != store.Candidate {
		t.Errorf("a candidate the session did not store was decided: %+v", f)
	}
}

// A candidate reaches the judge with the approved facts nearest to it, so a duplicate or a contradiction is visible
// in the answer rather than guessed.
func TestVetInputCarriesNeighbours(t *testing.T) {
	env(t)
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	approved, err := s.Add("В контейнере hermes базу facts.db читают через python3: CLI sqlite3 нет.",
		[]string{"sqlite3", "python3"}, "hermes 2026-10-19")
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	cand := vetFact(t, "В контейнере hermes нет sqlite3, поэтому базу facts.db читают модулем python3.", "hermes 2026-10-21", "tool")
	calls := fakeJudge(t, judgeByID(cand))
	if err := runVet(nil, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 {
		t.Fatalf("review calls %d", len(*calls))
	}
	if !strings.Contains((*calls)[0], approved.ID) || !strings.Contains((*calls)[0], "neighbours") {
		t.Errorf("input lacks the neighbour %s: %s", approved.ID, (*calls)[0])
	}
}

// An expiry the judge gives is the expiry of the approved fact, in one version rather than an approval and a fix.
func TestVetAppliesExpiryFromTheJudge(t *testing.T) {
	env(t)
	id := vetFact(t, "Проверить, как прошёл ночной dream на живой базе.", "hermes 2026-10-21", "tool")
	fakeJudge(t, func(input string) (extract.ReviewResult, error) {
		return extract.ReviewResult{Decisions: []extract.ReviewDecision{{ID: id, Verdict: extract.KeepVerdict,
			Reason: "временная проверка", Expires: "2026-11-01"}}}, nil
	})
	if err := runVet(nil, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	f := vetStatus(t, id)
	if f.Review != "" || f.Expires != "2026-11-01" || f.Version != 2 {
		t.Errorf("approved fact %+v", f)
	}
}

// A malformed expiry is not a write failure: the candidate becomes unsure and stays in the queue.
func TestVetInvalidExpiryStaysCandidate(t *testing.T) {
	env(t)
	id := vetFact(t, "Проверить, как прошёл ночной dream на живой базе.", "hermes 2026-10-21", "tool")
	fakeJudge(t, func(input string) (extract.ReviewResult, error) {
		return extract.ReviewResult{Decisions: []extract.ReviewDecision{{ID: id, Verdict: extract.KeepVerdict,
			Reason: "временная проверка", Expires: "2026-11-01T00:00:00Z"}}}, nil
	})
	var out bytes.Buffer
	if err := runVet(nil, &out); err != nil {
		t.Fatal(err)
	}
	if f := vetStatus(t, id); f.Review != store.Candidate || f.Status != store.Active {
		t.Errorf("a malformed expiry changed the base: %+v", f)
	}
	if !strings.Contains(out.String(), "invalid expiry") || strings.Contains(out.String(), "принято 1") {
		t.Errorf("report hides the malformed expiry:\n%s", out.String())
	}
}

// A write failure is visible in the human report and makes the command fail; it is not counted as a decided fact.
func TestPrintVetShowsFailedWrite(t *testing.T) {
	line := vetLine{ID: "01a", Verdict: "KEEP", Decision: "failed: disk full", Applied: false,
		Reason: "looks useful", Content: "a fact"}
	var out bytes.Buffer
	if err := printVet(&out, []vetLine{line}, vetRecord{Candidates: 1, Failed: 1}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "failed: disk full") || !strings.Contains(out.String(), "ошибок записи 1") {
		t.Errorf("report hides the failed write:\n%s", out.String())
	}
	if err := vetFailure(vetRecord{Failed: 1}); err == nil {
		t.Error("a failed write must make the command fail")
	}
}

// A manual run must not overlap a session run: both take the same lock.
func TestRunVetRefusesWhenAnotherRunHoldsTheLock(t *testing.T) {
	env(t)
	dir, err := stateDir()
	if err != nil {
		t.Fatal(err)
	}
	lock, err := os.OpenFile(filepath.Join(dir, "vet.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Close() }()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()
	if err := runVet(nil, &bytes.Buffer{}); !errors.Is(err, errVetBusy) {
		t.Fatalf("runVet = %v, want errVetBusy", err)
	}
}

// fakeDupJudge stands in for the duplicate judge: it answers by candidate text and counts the calls.
func fakeDupJudge(t *testing.T, answer func(cand extract.JudgeFact, approved []extract.JudgeFact) (extract.DuplicateVerdict, error)) *[]extract.JudgeFact {
	t.Helper()
	var calls []extract.JudgeFact
	var mu sync.Mutex
	oldFn := judgeDupFn
	judgeDupFn = func(_ context.Context, cand extract.JudgeFact, approved []extract.JudgeFact) (extract.DuplicateVerdict, error) {
		mu.Lock()
		calls = append(calls, cand)
		mu.Unlock()
		return answer(cand, approved)
	}
	oldJev := extract.JevJudge
	extract.JevJudge.URL, extract.JevJudge.Model, extract.JevJudge.Key = "http://127.0.0.1:1", "typesafe/jev-1.14", "k"
	extract.JevJudge.Min, extract.JevJudge.MaxFacts = 0.9, 400
	t.Cleanup(func() { judgeDupFn, extract.JevJudge = oldFn, oldJev })
	return &calls
}

// approvedFact stores a fact the base already holds: what the duplicate judge compares a candidate with.
func approvedFact(t *testing.T, content string) string {
	t.Helper()
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	f, err := s.Add(content, []string{"x"}, "hermes 2026-10-19")
	if err != nil {
		t.Fatal(err)
	}
	return f.ID
}

// A candidate the judge calls a duplicate is retired with its probability in the log; everything it does not call a
// duplicate still goes to the chat judge, because Jev answers "is this known", not "is this worth keeping".
func TestVetJudgeRetiresDuplicateAndLeavesTheRestToTheModel(t *testing.T) {
	dir := env(t)
	existing := approvedFact(t, "В контейнере hermes базу facts.db читают через python3: CLI sqlite3 нет.")
	dup := vetFact(t, "В контейнере hermes нет sqlite3, базу facts.db читают модулем python3.", "hermes 2026-10-21", "tool")
	other := vetFact(t, "Ключ DeepSeek берут из окружения процесса шлюза, а не из .env.", "hermes 2026-10-21", "tool")
	calls := fakeDupJudge(t, func(cand extract.JudgeFact, approved []extract.JudgeFact) (extract.DuplicateVerdict, error) {
		if cand.ID == dup {
			return extract.DuplicateVerdict{P: 0.95, HitID: existing, Tokens: extract.Tokens{Input: 40000}, Cost: 0.0017}, nil
		}
		return extract.DuplicateVerdict{P: 0.11, Tokens: extract.Tokens{Input: 40000}, Cost: 0.0017}, nil
	})
	reviewCalls := fakeJudge(t, judgeByID(other))

	var out bytes.Buffer
	if err := runVet(nil, &out); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 2 {
		t.Fatalf("judge calls %d, want one per candidate", len(*calls))
	}
	if f := vetStatus(t, dup); f.Status != store.Retired {
		t.Errorf("duplicate %+v", f)
	}
	if f := vetStatus(t, other); f.Review != "" {
		t.Errorf("the model did not decide the rest: %+v", f)
	}
	if len(*reviewCalls) != 1 || strings.Contains((*reviewCalls)[0], dup) {
		t.Errorf("the chat judge saw the duplicate: %s", (*reviewCalls)[0])
	}

	logb, err := os.ReadFile(filepath.Join(dir, "state", "memcue", "vet.log"))
	if err != nil {
		t.Fatal(err)
	}
	var seen vetLine
	for _, line := range strings.Split(strings.TrimSpace(string(logb)), "\n") {
		var l vetLine
		if err := json.Unmarshal([]byte(line), &l); err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}
		if l.ID == dup {
			seen = l
		}
	}
	if seen.By != "judge" || seen.Decision != "retire" || seen.P == nil || *seen.P != 0.95 ||
		!strings.Contains(seen.Reason, existing) {
		t.Errorf("log line %+v", seen)
	}
	if !strings.Contains(out.String(), "отклонено 1") || !strings.Contains(out.String(), "$0.005") {
		t.Errorf("report:\n%s", out.String())
	}
}

// A stale Drop must not undo an approval made while the judge was thinking.
func TestVetDoesNotRetireAnApprovedFact(t *testing.T) {
	env(t)
	_ = approvedFact(t, "В контейнере hermes базу facts.db читают через python3: CLI sqlite3 нет.")
	cand := vetFact(t, "В контейнере hermes нет sqlite3, базу facts.db читают модулем python3.", "hermes 2026-10-21", "tool")
	fakeDupJudge(t, func(c extract.JudgeFact, _ []extract.JudgeFact) (extract.DuplicateVerdict, error) {
		s, err := store.Open(factsPath())
		if err != nil {
			return extract.DuplicateVerdict{}, err
		}
		_, err = s.Approve(c.ID)
		_ = s.Close()
		if err != nil {
			return extract.DuplicateVerdict{}, err
		}
		return extract.DuplicateVerdict{P: 0.99}, nil
	})
	var out bytes.Buffer
	if err := runVet(nil, &out); err == nil || !strings.Contains(err.Error(), "failed to apply") {
		t.Fatalf("runVet = %v, want a failed write", err)
	}
	if f := vetStatus(t, cand); f.Review != "" || f.Status != store.Active {
		t.Errorf("a stale drop retired an approved fact: %+v", f)
	}
	if !strings.Contains(out.String(), "failed:") || !strings.Contains(out.String(), "ошибок записи 1") {
		t.Errorf("report hides the stale drop:\n%s", out.String())
	}
}

// A judge call that fails decides nothing: the candidate waits for the next run instead of being approved blind, and
// the chat judge is not asked to answer the question the duplicate judge could not.
func TestVetJudgeFailureLeavesCandidates(t *testing.T) {
	env(t)
	cand := vetFact(t, "Скрипт крона должен лежать в HERMES_HOME/scripts.", "hermes 2026-10-21", "tool")
	fakeDupJudge(t, func(extract.JudgeFact, []extract.JudgeFact) (extract.DuplicateVerdict, error) {
		return extract.DuplicateVerdict{}, errors.New("decisions 500: boom")
	})
	reviewCalls := fakeJudge(t, judgeByID(cand))

	var out bytes.Buffer
	if err := runVet(nil, &out); err != nil {
		t.Fatal(err)
	}
	if f := vetStatus(t, cand); f.Review != store.Candidate || f.Status != store.Active {
		t.Errorf("a failed judge changed the base: %+v", f)
	}
	if len(*reviewCalls) != 0 {
		t.Errorf("the chat judge was asked after the duplicate judge failed")
	}
	if !strings.Contains(out.String(), "duplicate judge failed") || !strings.Contains(out.String(), "осталось решать 1") {
		t.Errorf("report:\n%s", out.String())
	}
}

// A base larger than one request is not judged: the review model with its neighbours stays in charge, unchanged.
func TestVetJudgeSkippedWhenTheBaseDoesNotFit(t *testing.T) {
	env(t)
	approvedFact(t, "В контейнере hermes базу facts.db читают через python3: CLI sqlite3 нет.")
	approvedFact(t, "Ключ DeepSeek берут из окружения процесса шлюза, а не из .env.")
	cand := vetFact(t, "Скрипт крона должен лежать в HERMES_HOME/scripts.", "hermes 2026-10-21", "tool")
	calls := fakeDupJudge(t, func(extract.JudgeFact, []extract.JudgeFact) (extract.DuplicateVerdict, error) {
		return extract.DuplicateVerdict{P: 0.99}, nil
	})
	extract.JevJudge.MaxFacts = 1
	reviewCalls := fakeJudge(t, judgeByID(cand))

	if err := runVet(nil, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 0 {
		t.Errorf("the judge ran on a base that does not fit: %d calls", len(*calls))
	}
	if len(*reviewCalls) != 1 {
		t.Fatalf("review calls %d", len(*reviewCalls))
	}
	if f := vetStatus(t, cand); f.Review != "" {
		t.Errorf("the model did not decide: %+v", f)
	}
}
