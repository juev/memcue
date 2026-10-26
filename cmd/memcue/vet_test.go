package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/juev/memcue/internal/extract"
	"github.com/juev/memcue/internal/store"
)

// quarantine keeps the old human queue when a test is about failed or unsure decisions specifically.
func quarantine(t *testing.T) {
	t.Helper()
	t.Setenv("MEMCUE_VET_POLICY", "quarantine")
}

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

// A judge that fails or says nothing approves nothing: those candidates wait for the next run, even under the
// default provisional policy, because a failed call is not a model "unsure" answer.
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

// The rules alone need no endpoint and no model, and an undecided candidate stays a candidate under the default
// provisional policy: there was no model judgement to turn into a provisional fact.
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

// An explicit -auto cannot override -no-model: without a model answer there is nothing to make provisional.
func TestVetNoModelWinsOverAuto(t *testing.T) {
	env(t)
	id := vetFact(t, "Ключ DeepSeek берут из окружения процесса шлюза.", "hermes 2026-10-21", "tool")
	calls := fakeJudge(t, judgeByID(id))
	var out bytes.Buffer
	if err := runVet([]string{"-no-model", "-auto"}, &out); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 0 {
		t.Errorf("the model was called with -no-model")
	}
	if f := vetStatus(t, id); f.Review != store.Candidate || f.Status != store.Active {
		t.Errorf("-auto overrode -no-model: %+v", f)
	}
	if !strings.Contains(out.String(), "осталось решать 1") {
		t.Errorf("report:\n%s", out.String())
	}
}

// A note file that cannot be read must skip the mirror rule, not apply it to the notes that were readable: those are
// not the whole note, and a mirror they do not hold is not a deleted one. This file already needs POSIX for
// syscall.Flock; the mode check only has to skip root, which reads a mode-000 file anyway.
func TestVetUnreadableNoteSkipsMirrors(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read a mode-000 file")
	}
	dir := env(t)
	notes := filepath.Join(dir, "notes")
	if err := os.MkdirAll(notes, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(notes, "MEMORY.md"), []byte("Пользователь предпочитает короткие ответы."), 0o600); err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(notes, "PRIVATE.md")
	if err := os.WriteFile(locked, []byte("Пользователь пишет коммиты по-английски."), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o600) })

	quarantine(t) // keep an undecided mirror a candidate rather than turning it provisional
	mirror := vetFact(t, "Пользователь пишет коммиты по-английски.", "hermes-memory 2026-10-20", "memory")
	var out bytes.Buffer
	if err := runVet([]string{"-memory", notes, "-no-model"}, &out); err != nil {
		t.Fatal(err)
	}
	if f := vetStatus(t, mirror); f.Status != store.Active {
		t.Errorf("an unreadable note retired its mirror: %+v", f)
	}
	if !strings.Contains(out.String(), "осталось решать 1") {
		t.Errorf("report:\n%s", out.String())
	}
}

// A session reviews only the ids it stored, and only when an endpoint is set and MEMCUE_VET is not 0: without a review
// model the extraction path must not start a call, and MEMCUE_VET=0 turns the review off.
func TestMaybeVetGatesAndReviewsStoredIDs(t *testing.T) {
	env(t)
	t.Setenv("MEMCUE_VET", "")
	t.Setenv("PATH", t.TempDir()) // no claude: the review needs the endpoint
	oldURL := extract.API.URL
	t.Cleanup(func() { extract.API.URL = oldURL })
	newer := vetFact(t, "Ключ DeepSeek берут из окружения процесса шлюза.", "hermes 2026-10-21", "tool")
	older := vetFact(t, "Скрипт крона должен лежать в HERMES_HOME/scripts.", "hermes 2026-10-20", "tool")
	calls := fakeJudge(t, judgeByID(newer))

	maybeVet([]string{newer})
	if len(*calls) != 0 {
		t.Fatal("a missing endpoint reviewed the queue")
	}
	extract.API.URL = "http://127.0.0.1:1"
	t.Setenv("MEMCUE_VET", "0")
	maybeVet([]string{newer})
	if len(*calls) != 0 {
		t.Fatal("MEMCUE_VET=0 reviewed the queue")
	}
	t.Setenv("MEMCUE_VET", "")
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

// Without an endpoint the review takes the `claude -p` path the extraction takes, when claude is on PATH.
func TestMaybeVetUsesClaudeWithoutAnEndpoint(t *testing.T) {
	env(t)
	t.Setenv("MEMCUE_VET", "")
	oldURL := extract.API.URL
	t.Cleanup(func() { extract.API.URL = oldURL })
	extract.API.URL = ""
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	id := vetFact(t, "Ключ DeepSeek берут из окружения процесса шлюза.", "hermes 2026-10-21", "tool")
	calls := fakeJudge(t, judgeByID(id))
	maybeVet([]string{id})
	if len(*calls) != 1 {
		t.Fatalf("review calls %d, want 1", len(*calls))
	}
	if f := vetStatus(t, id); f.Review != "" {
		t.Errorf("the stored id %+v", f)
	}
}

// A model outage during MEMCUE_VET=1 leaves the session's candidates in the queue: a failed call is not a model
// "unsure" answer, so the default provisional policy must not turn it into a recallable fact.
func TestMaybeVetFailureKeepsCandidate(t *testing.T) {
	env(t)
	t.Setenv("MEMCUE_VET", "1")
	oldURL := extract.API.URL
	t.Cleanup(func() { extract.API.URL = oldURL })
	extract.API.URL = "http://127.0.0.1:1"
	id := vetFact(t, "Проверить, как прошёл ночной dream на живой базе.", "hermes 2026-10-21", "tool")
	fakeJudge(t, func(string) (extract.ReviewResult, error) {
		return extract.ReviewResult{}, errors.New("endpoint returned no choices")
	})
	maybeVet([]string{id})
	if f := vetStatus(t, id); f.Review != store.Candidate || f.Status != store.Active {
		t.Errorf("a failed session review changed the candidate: %+v", f)
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

// A malformed expiry is not a write failure: the candidate becomes unsure and stays in the queue, even under the
// default provisional policy, because the model answer could not be applied as written.
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

// The default policy turns an unsure answer into a provisional fact.
func TestVetDefaultsToProvisional(t *testing.T) {
	env(t)
	t.Setenv("MEMCUE_VET_POLICY", "")
	id := vetFact(t, "Проверить, как прошёл ночной dream на живой базе.", "hermes 2026-10-21", "tool")
	fakeJudge(t, func(input string) (extract.ReviewResult, error) {
		return extract.ReviewResult{Decisions: []extract.ReviewDecision{{ID: id, Verdict: extract.UnsureVerdict}}}, nil
	})
	var out bytes.Buffer
	if err := runVet(nil, &out); err != nil {
		t.Fatal(err)
	}
	if f := vetStatus(t, id); f.Review != store.Provisional || f.Expires == "" {
		t.Errorf("default provisional = %+v", f)
	}
	if !strings.Contains(out.String(), "временно принято 1") {
		t.Errorf("report:\n%s", out.String())
	}
}

// An explicit quarantine keeps an unsure model answer for a person.
func TestVetQuarantineKeepsUnsureCandidate(t *testing.T) {
	env(t)
	id := vetFact(t, "Проверить, как прошёл ночной dream на живой базе.", "hermes 2026-10-21", "tool")
	fakeJudge(t, func(input string) (extract.ReviewResult, error) {
		return extract.ReviewResult{Decisions: []extract.ReviewDecision{{ID: id, Verdict: extract.UnsureVerdict}}}, nil
	})
	var out bytes.Buffer
	if err := runVet([]string{"-quarantine"}, &out); err != nil {
		t.Fatal(err)
	}
	if f := vetStatus(t, id); f.Review != store.Candidate || f.Version != 1 {
		t.Errorf("quarantine flag changed the candidate: %+v", f)
	}
	if !strings.Contains(out.String(), "осталось решать 1") {
		t.Errorf("report:\n%s", out.String())
	}
}

// With -auto an unsure model answer becomes a provisional fact for the TTL instead of a human task.
func TestVetAutoTurnsUnsureIntoProvisional(t *testing.T) {
	env(t)
	id := vetFact(t, "Проверить, как прошёл ночной dream на живой базе.", "hermes 2026-10-21", "tool")
	fakeJudge(t, func(input string) (extract.ReviewResult, error) {
		return extract.ReviewResult{Decisions: []extract.ReviewDecision{{ID: id, Verdict: extract.UnsureVerdict,
			Reason: "не хватает уверенности"}}}, nil
	})
	var out bytes.Buffer
	if err := runVet([]string{"-auto", "-ttl-days", "7"}, &out); err != nil {
		t.Fatal(err)
	}
	f := vetStatus(t, id)
	if f.Review != store.Provisional || f.Expires == "" || f.Version != 2 {
		t.Errorf("auto provisional = %+v", f)
	}
	if !strings.Contains(out.String(), "provisional until") || !strings.Contains(out.String(), "временно принято 1") {
		t.Errorf("report:\n%s", out.String())
	}
}

// The same policy is used by the detached extraction hook through MEMCUE_VET_POLICY=auto.
func TestMaybeVetAutoPolicy(t *testing.T) {
	env(t)
	t.Setenv("MEMCUE_VET", "1")
	t.Setenv("MEMCUE_VET_POLICY", "auto")
	oldURL := extract.API.URL
	t.Cleanup(func() { extract.API.URL = oldURL })
	extract.API.URL = "http://127.0.0.1:1"
	id := vetFact(t, "Проверить, как прошёл ночной dream на живой базе.", "hermes 2026-10-21", "tool")
	fakeJudge(t, func(input string) (extract.ReviewResult, error) {
		return extract.ReviewResult{Decisions: []extract.ReviewDecision{{ID: id, Verdict: extract.UnsureVerdict}}}, nil
	})
	maybeVet([]string{id})
	if f := vetStatus(t, id); f.Review != store.Provisional {
		t.Errorf("maybeVet did not apply the auto policy: %+v", f)
	}
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

// A stale Drop must not undo an approval made while the model was thinking.
func TestVetDoesNotRetireAnApprovedFact(t *testing.T) {
	env(t)
	_ = approvedFact(t, "В контейнере hermes базу facts.db читают через python3: CLI sqlite3 нет.")
	cand := vetFact(t, "В контейнере hermes нет sqlite3, базу facts.db читают модулем python3.", "hermes 2026-10-21", "tool")
	drop := judgeByID()
	fakeJudge(t, func(input string) (extract.ReviewResult, error) {
		s, err := store.Open(factsPath())
		if err != nil {
			return extract.ReviewResult{}, err
		}
		_, err = s.Approve(cand)
		_ = s.Close()
		if err != nil {
			return extract.ReviewResult{}, err
		}
		return drop(input)
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

// The review sees each candidate's nearest approved facts and drops a duplicate itself (#266).
func TestVetReviewDropsADuplicate(t *testing.T) {
	env(t)
	existing := approvedFact(t, "В контейнере hermes базу facts.db читают через python3: CLI sqlite3 нет.")
	dup := vetFact(t, "В контейнере hermes нет sqlite3, базу facts.db читают модулем python3.", "hermes 2026-10-21", "tool")
	reviewCalls := fakeJudge(t, judgeByID())

	var out bytes.Buffer
	if err := runVet(nil, &out); err != nil {
		t.Fatal(err)
	}
	if len(*reviewCalls) != 1 || !strings.Contains((*reviewCalls)[0], existing) {
		t.Errorf("the review did not see the approved neighbour: %v", *reviewCalls)
	}
	if f := vetStatus(t, dup); f.Status != store.Retired {
		t.Errorf("the duplicate the review dropped is %+v", f)
	}
}

func TestVetBaseReadFailureIsAnError(t *testing.T) {
	env(t)
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	f, err := s.AddWith("20 октября 2026 обновили demo.", nil, "x 2026-10-20", "",
		store.Meta{Review: store.Candidate})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	_, rec := vetQueue(s, []store.Fact{f}, vetOptions{}) // Active() on the closed base
	if rec.Error == "" {
		t.Fatal("the base read error was not recorded")
	}
	if err := vetFailure(rec); err == nil || !strings.Contains(err.Error(), rec.Error) {
		t.Errorf("exit error %v, want the base error %q", err, rec.Error)
	}
}

// judgeSplit answers split with parts for every candidate whose text is a key of split, keep for the texts in keep and
// drop for the rest.
func judgeSplit(split map[string][]string, keep ...string) func(string) (extract.ReviewResult, error) {
	return func(input string) (extract.ReviewResult, error) {
		var req struct {
			Candidates []judgedCandidate `json:"candidates"`
		}
		if err := json.Unmarshal([]byte(input), &req); err != nil {
			return extract.ReviewResult{}, err
		}
		var r extract.ReviewResult
		for _, c := range req.Candidates {
			d := extract.ReviewDecision{ID: c.ID, Verdict: extract.DropVerdict, Reason: "judged"}
			if parts, ok := split[c.Text]; ok {
				d.Verdict, d.Parts = extract.SplitVerdict, parts
			} else if slices.Contains(keep, c.Text) {
				d.Verdict = extract.KeepVerdict
			}
			r.Decisions = append(r.Decisions, d)
		}
		return r, nil
	}
}

// A compound candidate is retired and its parts are stored as candidates with its origin and meta; the same run reviews
// the parts, so a session's vet does not leave them waiting for a manual run.
func TestVetSplitsACompoundCandidateAndReviewsItsParts(t *testing.T) {
	dir := env(t)
	t.Setenv("MEMCUE_VET", "1")
	oldURL := extract.API.URL
	t.Cleanup(func() { extract.API.URL = oldURL })
	extract.API.URL = "http://127.0.0.1:1"
	compound := "На loffe стоит memcue 0.4.0; бинарь лежит в /opt/data/bin."
	version, path := "На loffe стоит memcue 0.4.0.", "Бинарь memcue на loffe лежит в /opt/data/bin."
	id := vetFact(t, compound, "hermes 2026-10-22", "tool")
	calls := fakeJudge(t, judgeSplit(map[string][]string{compound: {version, path}}, path))

	maybeVet([]string{id})
	if len(*calls) != 2 {
		t.Fatalf("review calls %d, want the candidate and then its parts", len(*calls))
	}
	if f := vetStatus(t, id); f.Status != store.Retired {
		t.Errorf("the compound candidate %+v", f)
	}
	logb, err := os.ReadFile(filepath.Join(dir, "state", "memcue", "vet.log"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(logb)), "\n")
	if len(lines) != 4 { // the split, its two parts, then the summary
		t.Fatalf("log lines %d: %s", len(lines), logb)
	}
	var split vetLine
	if err := json.Unmarshal([]byte(lines[0]), &split); err != nil {
		t.Fatal(err)
	}
	if split.Decision != "split" || !split.Applied || len(split.Into) != 2 || len(split.Parts) != 2 {
		t.Fatalf("split line %+v", split)
	}
	byText := map[string]store.Fact{}
	for _, pid := range split.Into {
		f := vetStatus(t, pid)
		if f.Origin != "hermes 2026-10-22" || f.Scope != "host:test" || f.Kind != "pitfall" || f.Source != "tool" {
			t.Errorf("a part lost the original's origin or meta: %+v", f)
		}
		byText[f.Content] = f
	}
	if f := byText[path]; f.Status != store.Active || f.Review != "" {
		t.Errorf("the part judged keep %+v", f)
	}
	if f := byText[version]; f.Status != store.Retired {
		t.Errorf("the part judged drop %+v", f)
	}
	var rec vetRecord
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Candidates != 3 || rec.Split != 1 || rec.Approved != 1 || rec.Retired != 1 {
		t.Errorf("summary %+v", rec)
	}
}

// A split the base cannot use leaves the candidate in the queue, even under the provisional policy: one part, or a
// part that the model wants to split again in the same run.
func TestVetBadSplitKeepsCandidate(t *testing.T) {
	env(t)
	one := vetFact(t, "Порт gitea 3000 на хосте loffe.", "hermes 2026-10-22", "tool")
	twice := vetFact(t, "Порт gitea 3000; данные в /srv/gitea; бэкап по cron.", "hermes 2026-10-22", "tool")
	fakeJudge(t, judgeSplit(map[string][]string{
		"Порт gitea 3000 на хосте loffe.":                      {"Порт gitea 3000 на хосте loffe."},
		"Порт gitea 3000; данные в /srv/gitea; бэкап по cron.": {"Порт gitea 3000.", "Данные gitea в /srv/gitea; бэкап по cron."},
		"Данные gitea в /srv/gitea; бэкап по cron.":            {"Данные gitea в /srv/gitea.", "Бэкап gitea по cron."},
	}, "Порт gitea 3000."))
	var out bytes.Buffer
	if err := runVet(nil, &out); err != nil {
		t.Fatal(err)
	}
	if f := vetStatus(t, one); f.Status != store.Active || f.Review != store.Candidate {
		t.Errorf("a split into one part changed the candidate: %+v", f)
	}
	if f := vetStatus(t, twice); f.Status != store.Retired {
		t.Errorf("the compound candidate %+v", f)
	}
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	cands, err := s.Candidates()
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, c := range cands {
		texts = append(texts, c.Content)
	}
	slices.Sort(texts)
	want := []string{"Данные gitea в /srv/gitea; бэкап по cron.", "Порт gitea 3000 на хосте loffe."}
	if !slices.Equal(texts, want) {
		t.Errorf("queue %q, want %q", texts, want)
	}
	for _, w := range []string{"split into 1 part(s)", "a part of a split was split again", "разделено на части 1",
		"+ Порт gitea 3000."} {
		if !strings.Contains(out.String(), w) {
			t.Errorf("report lacks %q:\n%s", w, out.String())
		}
	}
}

// A dry run reports the split and its parts and writes nothing.
func TestVetDryRunReportsASplit(t *testing.T) {
	env(t)
	compound := "Порт gitea 3000; данные в /srv/gitea."
	id := vetFact(t, compound, "hermes 2026-10-22", "tool")
	calls := fakeJudge(t, judgeSplit(map[string][]string{compound: {"Порт gitea 3000.", "Данные gitea в /srv/gitea."}}))
	var out bytes.Buffer
	if err := runVet([]string{"-dry-run"}, &out); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 {
		t.Errorf("review calls %d: a dry run has no parts to review", len(*calls))
	}
	if f := vetStatus(t, id); f.Status != store.Active || f.Review != store.Candidate || f.Version != 1 {
		t.Errorf("a dry run changed the candidate: %+v", f)
	}
	if !strings.Contains(out.String(), "+ Данные gitea в /srv/gitea.") || !strings.Contains(out.String(), "разделено на части 1") {
		t.Errorf("report:\n%s", out.String())
	}
}

// A mirror is approved when the memory is written (#440), so the suspects pass is what retires it once its note is
// gone: by rule, without a model, and only for a fact whose text the notes no longer hold.
func TestVetRetiresAnApprovedMirrorWhoseNoteIsGone(t *testing.T) {
	dir := env(t)
	notes := filepath.Join(dir, "notes")
	if err := os.MkdirAll(notes, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(notes, "MEMORY.md"), []byte("Пользователь пишет коммиты по-английски."), 0o600); err != nil {
		t.Fatal(err)
	}
	live := vetFact(t, "Пользователь пишет коммиты по-английски.", "hermes-memory 2026-10-20", "memory")
	gone := vetFact(t, "В контейнере hermes нет CLI sqlite3, базу читают через python3.", "hermes-memory 2026-10-20", "memory")
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{live, gone} { // as the provider leaves them: approved, not candidates
		if _, err := s.Approve(id); err != nil {
			t.Fatal(err)
		}
	}
	_ = s.Close()

	lines, rec := func() ([]vetLine, vetRecord) {
		st, err := store.Open(factsPath())
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = st.Close() }()
		return vetSuspects(st, vetOptions{memory: notes, noModel: true}, time.Now())
	}()
	if rec.SuspectsRetired != 1 {
		t.Errorf("retired %d, want the one stale mirror: %+v", rec.SuspectsRetired, lines)
	}
	if f := vetStatus(t, gone); f.Status != store.Retired {
		t.Errorf("stale mirror %+v", f)
	}
	if f := vetStatus(t, live); f.Status != store.Active {
		t.Errorf("live mirror %+v", f)
	}
	if len(lines) != 1 || !strings.Contains(lines[0].Reason, "stale mirror") || lines[0].By != "rule" {
		t.Errorf("lines %+v", lines)
	}
}

// A mirror queued before the write-time approval (#440) is no session's candidate: a session's review takes it along,
// approves the one whose note is new as a global fact, and retires the one whose rule the base already holds (#458).
func TestMaybeVetDecidesTheMirrorBacklog(t *testing.T) {
	dir := env(t)
	t.Setenv("MEMCUE_VET", "1")
	oldURL := extract.API.URL
	t.Cleanup(func() { extract.API.URL = oldURL })
	extract.API.URL = "http://127.0.0.1:1"
	notes := filepath.Join(dir, "notes")
	if err := os.MkdirAll(notes, 0o755); err != nil {
		t.Fatal(err)
	}
	note := "Пользователь пишет коммиты по-английски.\n§\nНавыки держат только процедуры: шаги, команды и критерии приёмки."
	if err := os.WriteFile(filepath.Join(notes, "MEMORY.md"), []byte(note), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MEMCUE_MEMORY_DIR", notes)
	rule := vetFact(t, "Навыки держат только процедуры: шаги, команды и критерии приёмки.", "x 2026-10-20", "user")
	session := vetFact(t, "Ключ DeepSeek берут из окружения процесса шлюза.", "hermes 2026-10-26", "tool")
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	mirror := func(content string) string { // as the provider queued it before #440: no kind, no scope
		t.Helper()
		f, err := s.AddWith(content, nil, "hermes-memory 2026-10-24", "", store.Meta{Source: "memory", Review: store.Candidate})
		if err != nil {
			t.Fatal(err)
		}
		return f.ID
	}
	fresh := mirror("Пользователь пишет коммиты по-английски.")
	dup := mirror("Навыки держат только процедуры: шаги, команды и критерии приёмки.")
	if _, err := s.Approve(rule); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	fakeJudge(t, judgeByID(session))

	maybeVet([]string{session})
	if f := vetStatus(t, fresh); f.Status != store.Active || f.Review != "" || f.Scope != "global" {
		t.Errorf("a new mirror is not an approved global fact: %+v", f)
	}
	if f := vetStatus(t, dup); f.Status != store.Retired {
		t.Errorf("a mirror of a live rule was not retired: %+v", f)
	}
}
