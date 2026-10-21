package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/juev/memcue/internal/extract"
	"github.com/juev/memcue/internal/store"
)

// fakeDream answers every model call with answer and records the groups it was given.
func fakeDream(t *testing.T, answer func(groups string) []extract.Action) *[]string {
	t.Helper()
	var calls []string
	old := dreamFn
	dreamFn = func(groups string) (extract.DreamResult, error) {
		calls = append(calls, groups)
		return extract.DreamResult{Actions: answer(groups), CostUSD: 0.01}, nil
	}
	t.Cleanup(func() { dreamFn = old })
	return &calls
}

// dreamBase stores an old fact and two later copies of the nightly-sync fact from two sessions, plus an unrelated one.
// b is written a few milliseconds before a, so its id is the smaller one: a dream keeps b and retires a.
func dreamBase(t *testing.T) (old, a, b, other string) {
	t.Helper()
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	add := func(c string, k ...string) string {
		f, err := s.Add(c, k, "x 2026-10-19")
		if err != nil {
			t.Fatal(err)
		}
		return f.ID
	}
	old = add("Postgres демо-базы слушает порт 5432 на db1.", "postgres", "порт")
	b = add("Workflow nightly-sync.yml в репозитории example/nightly-sync каждую ночь подписывает bot на все свои репозитории кроме форков и архивных.",
		"nightly-sync", "github подписка", "workflow")
	time.Sleep(3 * time.Millisecond) // a ULID orders by millisecond only
	a = add("Репозиторий example/nightly-sync каждую ночь запускает workflow nightly-sync.yml, он подписывает аккаунт bot на все свои репозитории, кроме форков.",
		"nightly-sync", "подписка", "workflow")
	other = add("Gradle в demo-app требует JAVA_HOME=/opt/jdk-21.", "gradle", "jdk")
	return
}

func TestDreamGroupsCopiesAndRetiresOne(t *testing.T) {
	dir := env(t)
	old, a, b, other := dreamBase(t)
	calls := fakeDream(t, func(string) []extract.Action {
		return []extract.Action{{Op: "retire", ID: a, By: b, Kind: "duplicate", Reason: "covered by " + b}}
	})
	var out bytes.Buffer
	if err := runDream([]string{"-all"}, &out); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 {
		t.Fatalf("calls %d", len(*calls))
	}
	sent := (*calls)[0]
	if !strings.Contains(sent, a) || !strings.Contains(sent, b) || strings.Contains(sent, old) || strings.Contains(sent, other) {
		t.Errorf("groups sent:\n%s", sent)
	}
	s, _ := store.Open(factsPath())
	defer func() { _ = s.Close() }()
	if f, _ := s.Get(a); f.Status != store.Retired {
		t.Errorf("copy not retired: %+v", f)
	}
	if f, _ := s.Get(b); f.Status != store.Active {
		t.Errorf("kept fact: %+v", f)
	}
	var rec dreamRecord
	logb, err := os.ReadFile(filepath.Join(dir, "state", "memcue", "dream.log"))
	if err != nil || json.Unmarshal(logb, &rec) != nil {
		t.Fatalf("log %q: %v", logb, err)
	}
	if rec.Groups != 1 || len(rec.Actions) != 1 || rec.Actions[0].Reason == "" || rec.CostUSD != 0.01 {
		t.Errorf("record %+v", rec)
	}
	if _, ok := lastDream(filepath.Join(dir, "state", "memcue")); !ok {
		t.Error("dream.at not written")
	}
}

// A provisional fact is recalled as a neighbour, so it must enter the group with its own data, not as a zero fact.
func TestDreamGroupKeepsAProvisionalNeighbour(t *testing.T) {
	env(t)
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	approved, err := s.Add("Postgres демо-базы слушает порт 5432 на db1.", []string{"postgres", "порт"}, "x 2026-10-19")
	if err != nil {
		t.Fatal(err)
	}
	provisional, err := s.AddWith("Postgres демо-базы на db1 держит порт 5432.", []string{"postgres", "порт"},
		"x 2026-10-19", "2026-12-01", store.Meta{Review: store.Provisional, Kind: "environment", Scope: "host:db1",
			Source: "tool", Evidence: "port 5432"})
	if err != nil {
		t.Fatal(err)
	}
	active, err := s.Active()
	if err != nil || len(active) != 1 {
		t.Fatalf("active %+v %v", active, err)
	}
	groups, err := groupFacts(s, active, active)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || len(groups[0]) != 2 {
		t.Fatalf("groups %+v", groups)
	}
	for _, f := range groups[0] {
		if f.ID != approved.ID && f.ID != provisional.ID {
			t.Errorf("unexpected group member %+v", f)
		}
		if f.ID == "" || f.Content == "" || f.Origin == "" {
			t.Errorf("zero group member %+v", f)
		}
	}
}

// The list view must not print an empty entry for a provisional neighbour either.
func TestDreamListShowsAProvisionalNeighbour(t *testing.T) {
	env(t)
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	approved, err := s.Add("Postgres демо-базы слушает порт 5432 на db1.", []string{"postgres", "порт"}, "x 2026-10-19")
	if err != nil {
		t.Fatal(err)
	}
	provisional, err := s.AddWith("Postgres демо-базы на db1 держит порт 5432.", []string{"postgres", "порт"},
		"x 2026-10-19", "2026-12-01", store.Meta{Review: store.Provisional, Kind: "environment", Scope: "host:db1",
			Source: "tool", Evidence: "port 5432"})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	var out bytes.Buffer
	if err := runDream([]string{"-all", "-list"}, &out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, approved.ID) || !strings.Contains(text, provisional.ID) {
		t.Fatalf("group misses a fact:\n%s", text)
	}
	if strings.Contains(text, "- id  (origin") {
		t.Fatalf("empty group entry:\n%s", text)
	}
}

func TestDreamDryRunChangesNothing(t *testing.T) {
	dir := env(t)
	_, a, b, _ := dreamBase(t)
	fakeDream(t, func(string) []extract.Action {
		return []extract.Action{{Op: "correct", ID: b, Content: "слитый текст", Reason: "merge"},
			{Op: "retire", ID: a, By: b, Kind: "duplicate", Reason: "covered"}}
	})
	if err := runDream([]string{"-all", "-dry-run"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	s, _ := store.Open(factsPath())
	defer func() { _ = s.Close() }()
	for _, id := range []string{a, b} {
		if f, _ := s.Get(id); f.Version != 1 || f.Status != store.Active {
			t.Errorf("%s changed: %+v", id, f)
		}
	}
	if _, ok := lastDream(filepath.Join(dir, "state", "memcue")); ok {
		t.Error("dry run moved dream.at")
	}
}

func TestDreamCorrectsMergedFact(t *testing.T) {
	env(t)
	_, a, b, _ := dreamBase(t)
	fakeDream(t, func(string) []extract.Action {
		return []extract.Action{{Op: "correct", ID: b, Content: "слитый текст", Keywords: []string{"nightly-sync"}, Reason: "merge"},
			{Op: "retire", ID: a, By: b, Kind: "duplicate", Reason: "merged into " + b}}
	})
	if err := runDream([]string{"-all"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	s, _ := store.Open(factsPath())
	defer func() { _ = s.Close() }()
	if f, _ := s.Get(b); f.Content != "слитый текст" || f.Version != 2 || f.Origin != "x 2026-10-19" {
		t.Errorf("corrected: %+v", f)
	}
}

func TestDreamCorrectWithoutKeywordsKeepsThem(t *testing.T) {
	env(t)
	_, a, b, _ := dreamBase(t)
	fakeDream(t, func(string) []extract.Action {
		return []extract.Action{{Op: "correct", ID: b, Content: "слитый текст", Reason: "merge"},
			{Op: "retire", ID: a, By: b, Kind: "duplicate", Reason: "merged"}}
	})
	if err := runDream([]string{"-all"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	s, _ := store.Open(factsPath())
	defer func() { _ = s.Close() }()
	if f, _ := s.Get(b); f.Content != "слитый текст" || strings.Join(f.Keywords, ",") != "nightly-sync,github подписка,workflow" {
		t.Errorf("corrected: %+v", f)
	}
}

func TestDreamRefusesUnsafeActions(t *testing.T) {
	groups := [][]store.Fact{{{ID: "A"}, {ID: "B"}, {ID: "C"}}, {{ID: "D"}, {ID: "E"}}}
	ok, refused := checkActions(groups, []extract.Action{
		{Op: "retire", ID: "Z", By: "A", Kind: "duplicate", Reason: "outside"},
		{Op: "retire", ID: "B", By: "A", Kind: "duplicate", Reason: "dup"},
		{Op: "retire", ID: "B", By: "A", Kind: "duplicate", Reason: "again"},
		{Op: "retire", ID: "A", By: "C", Kind: "outdated", Reason: "covers B"},
		{Op: "retire", ID: "C", Kind: "outdated", Reason: "no by"},
		{Op: "retire", ID: "C", By: "C", Kind: "outdated", Reason: "by itself"},
		{Op: "retire", ID: "C", By: "B", Kind: "outdated", Reason: "by a retired fact"},
		{Op: "retire", ID: "C", By: "D", Kind: "outdated", Reason: "by another group"},
		{Op: "retire", ID: "C", By: "A", Kind: "dup", Reason: "unknown kind"},
		{Op: "retire", ID: "D", By: "E", Kind: "duplicate", Reason: "keeps the larger id"},
		{Op: "retire", ID: "D", By: "E", Kind: "outdated", Reason: "outdated by a later fact"},
		{Op: "retire", ID: "E", By: "D", Kind: "duplicate", Reason: "last one"},
		{Op: "correct", ID: "C", Reason: "no text"},
		{Op: "delete", ID: "C", Reason: "unknown"},
	})
	if len(ok) != 2 || ok[0].ID != "B" || ok[1].ID != "D" {
		t.Errorf("ok %+v", ok)
	}
	if len(refused) != 12 {
		t.Errorf("refused %d: %q", len(refused), refused)
	}
}

// Whichever copy of a duplicate the model names, and in whatever order, only the larger id is retired, so two
// machines dreaming over one base keep the same copy.
func TestDreamDuplicateKeepsSmallerID(t *testing.T) {
	groups := [][]store.Fact{{{ID: "01A"}, {ID: "01B"}}}
	answers := [][]extract.Action{
		{{Op: "retire", ID: "01B", By: "01A", Kind: "duplicate", Reason: "dup"}},
		{{Op: "retire", ID: "01A", By: "01B", Kind: "duplicate", Reason: "dup"}},
		{{Op: "retire", ID: "01A", By: "01B", Kind: "duplicate", Reason: "dup"},
			{Op: "retire", ID: "01B", By: "01A", Kind: "duplicate", Reason: "dup"}},
	}
	for i, answer := range answers {
		ok, _ := checkActions(groups, answer)
		for _, a := range ok {
			if a.ID != "01B" {
				t.Errorf("answer %d: retired %s, the smaller id", i, a.ID)
			}
		}
	}
	if ok, _ := checkActions(groups, answers[2]); len(ok) != 1 {
		t.Errorf("both orders: ok %+v", ok)
	}
}

func TestDreamSinceLastRunOnly(t *testing.T) {
	dir := env(t)
	dreamBase(t)
	calls := fakeDream(t, func(string) []extract.Action { return nil })
	if err := runDream(nil, &bytes.Buffer{}); err == nil {
		t.Error("first dream without -since or -all must ask for one")
	}
	if err := markDream(filepath.Join(dir, "state", "memcue"), time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := runDream(nil, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 0 {
		t.Errorf("facts older than the last dream were sent: %q", *calls)
	}
}

func TestDreamModelErrorIsLoggedAndChangesNothing(t *testing.T) {
	dir := env(t)
	_, a, _, _ := dreamBase(t)
	old := dreamFn
	dreamFn = func(string) (extract.DreamResult, error) { return extract.DreamResult{}, errors.New("rate limited") }
	t.Cleanup(func() { dreamFn = old })
	if err := runDream([]string{"-all"}, &bytes.Buffer{}); err == nil {
		t.Error("want the error")
	}
	b, _ := os.ReadFile(filepath.Join(dir, "state", "memcue", "dream.log"))
	if !strings.Contains(string(b), "rate limited") {
		t.Errorf("log %q", b)
	}
	if _, ok := lastDream(filepath.Join(dir, "state", "memcue")); ok {
		t.Error("failed dream moved dream.at")
	}
	s, _ := store.Open(factsPath())
	defer func() { _ = s.Close() }()
	if f, _ := s.Get(a); f.Status != store.Active {
		t.Errorf("fact changed: %+v", f)
	}
}

func TestMaybeDreamOnlyWhenOnAndDue(t *testing.T) {
	dir := env(t)
	state := filepath.Join(dir, "state", "memcue")
	dreamBase(t)
	calls := fakeDream(t, func(string) []extract.Action { return nil })

	t.Setenv("MEMCUE_DREAM", "")
	maybeDream()
	if _, err := os.Stat(filepath.Join(state, "dream.at")); !os.IsNotExist(err) {
		t.Errorf("dream off but state written: %v", err)
	}

	t.Setenv("MEMCUE_DREAM", "1")
	maybeDream() // first call marks the start only
	if _, ok := lastDream(state); !ok || len(*calls) != 0 {
		t.Fatalf("first call: marked %v, calls %d", ok, len(*calls))
	}
	maybeDream() // not a day yet
	if len(*calls) != 0 {
		t.Errorf("dreamt before a day passed")
	}
	if err := markDream(state, time.Now().Add(-25*time.Hour)); err != nil {
		t.Fatal(err)
	}
	// the first call saw the whole base, so a due dream has nothing new to review
	maybeDream()
	if len(*calls) != 0 {
		t.Errorf("facts the first call saw were sent: calls %d", len(*calls))
	}
	if err := markDream(state, time.Now().Add(-25*time.Hour)); err != nil {
		t.Fatal(err)
	}
	addFact(t, "Workflow nightly-sync.yml в example/nightly-sync ночью подписывает bot на все свои репозитории, кроме форков.",
		"nightly-sync", "workflow")
	maybeDream()
	if len(*calls) != 1 {
		t.Errorf("due dream with a new fact: calls %d", len(*calls))
	}
	if at, _ := lastDream(state); time.Since(at) > time.Minute {
		t.Errorf("dream.at not moved: %v", at)
	}
}

func addFact(t *testing.T, content string, keywords ...string) string {
	t.Helper()
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	f, err := s.Add(content, keywords, "y 2026-10-20")
	if err != nil {
		t.Fatal(err)
	}
	return f.ID
}

// A fact written on another machine before this one's last dream, and pulled after it, is new to this machine.
func TestDreamReviewsFactPulledAfterLastDream(t *testing.T) {
	dir := env(t)
	state := filepath.Join(dir, "state", "memcue")
	_, a, _, _ := dreamBase(t)
	calls := fakeDream(t, func(string) []extract.Action { return nil })
	if err := runDream([]string{"-all"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	pulled := addFact(t, "Репозиторий example/nightly-sync ночью подписывает bot на свои репозитории через workflow nightly-sync.yml.",
		"nightly-sync", "workflow")
	// the mark is later than the pulled fact was written, as when the other machine wrote it before this dream
	if err := markDream(state, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	*calls = nil
	if err := runDream(nil, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || !strings.Contains((*calls)[0], pulled) || !strings.Contains((*calls)[0], a) {
		t.Fatalf("pulled fact not reviewed with its neighbour: %q", *calls)
	}
	if !strings.Contains((*calls)[0], pulled+" (origin: y 2026-10-20; записан 2026-") ||
		!strings.Contains((*calls)[0], ", новый;") {
		t.Errorf("pulled fact not marked new: %q", (*calls)[0])
	}
}

// Facts the last dream saw, including the versions it wrote, are not sent again; a version written since is.
func TestDreamSkipsSeenReviewsNewVersion(t *testing.T) {
	dir := env(t)
	state := filepath.Join(dir, "state", "memcue")
	_, a, b, _ := dreamBase(t)
	calls := fakeDream(t, func(groups string) []extract.Action {
		if strings.Contains(groups, a) {
			return []extract.Action{{Op: "correct", ID: b, Content: "Workflow nightly-sync.yml в example/nightly-sync ночью подписывает bot на все репозитории, кроме форков и архивных.", Reason: "merge"},
				{Op: "retire", ID: a, By: b, Kind: "duplicate", Reason: "merged into " + b}}
		}
		return nil
	})
	if err := runDream([]string{"-all"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	// a mark in the past would send every fact by when it was written; the seen list keeps them out
	if err := markDream(state, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	*calls = nil
	if err := runDream(nil, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 0 {
		t.Fatalf("seen facts sent again: %q", *calls)
	}

	other := addFact(t, "Workflow nightly-sync.yml подписывает аккаунт bot на репозитории каждую ночь.", "nightly-sync", "workflow")
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Correct(b, "Workflow nightly-sync.yml в example/nightly-sync каждую ночь подписывает bot на все репозитории, кроме форков.", nil); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	if err := markDream(state, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := runDream(nil, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || !strings.Contains((*calls)[0], other) || !strings.Contains((*calls)[0], b+" (origin: x 2026-10-19; записан 2026-") {
		t.Fatalf("new fact and new version not reviewed: %q", *calls)
	}
}

// mergeAnswer retires a and folds it into b, the answer for the nightly-sync group of dreamBase.
func mergeAnswer(a, b string) func(string) []extract.Action {
	return func(groups string) []extract.Action {
		if !strings.Contains(groups, a) {
			return nil
		}
		return []extract.Action{{Op: "correct", ID: b, Content: "слитый текст", Reason: "merge"},
			{Op: "retire", ID: a, By: b, Kind: "duplicate", Reason: "merged into " + b}}
	}
}

func readDreamLog(t *testing.T, dir string) []dreamRecord {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "state", "memcue", "dream.log"))
	if err != nil {
		t.Fatal(err)
	}
	var recs []dreamRecord
	for line := range strings.Lines(string(b)) {
		var r dreamRecord
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatal(err)
		}
		recs = append(recs, r)
	}
	return recs
}

// A plan applied on an unchanged base gives what a real dream with the same answer gives, with no model call.
func TestDreamApplyPlanMatchesRealDream(t *testing.T) {
	dir := env(t)
	state := filepath.Join(dir, "state", "memcue")
	_, a, b, _ := dreamBase(t)
	calls := fakeDream(t, mergeAnswer(a, b))
	plan := filepath.Join(dir, "plan.json")
	if err := runDream([]string{"-all", "-dry-run", "-plan", plan}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := lastDream(state); ok {
		t.Fatal("dry run with a plan moved dream.at")
	}
	*calls = nil
	var out bytes.Buffer
	if err := runDream([]string{"-apply", plan}, &out); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 0 {
		t.Errorf("-apply called the model %d times", len(*calls))
	}
	s, _ := store.Open(factsPath())
	if f, _ := s.Get(a); f.Status != store.Retired {
		t.Errorf("a not retired: %+v", f)
	}
	if f, _ := s.Get(b); f.Content != "слитый текст" || f.Version != 2 {
		t.Errorf("b not corrected: %+v", f)
	}
	_ = s.Close()
	if !strings.Contains(out.String(), "retire "+a) {
		t.Errorf("out %q", out.String())
	}
	recs := readDreamLog(t, dir)
	if last := recs[len(recs)-1]; last.Plan != plan || len(last.Actions) != 2 || last.CostUSD != 0 {
		t.Errorf("apply record %+v", last)
	}
	// marked as a real dream: the next one has nothing new, and the corrected version counts as seen
	if _, ok := lastDream(state); !ok {
		t.Fatal("-apply did not mark the dream")
	}
	*calls = nil
	if err := runDream(nil, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 0 {
		t.Errorf("after -apply the next dream sent %q", *calls)
	}
}

// A group whose fact changed after the plan is refused and logged; the other groups still apply.
func TestDreamApplyPlanRefusesChangedGroup(t *testing.T) {
	dir := env(t)
	old, a, b, _ := dreamBase(t)
	pg := addFact(t, "Postgres демо-базы на db1 слушает порт 5432, логин test.", "postgres", "порт")
	fakeDream(t, func(groups string) []extract.Action {
		var acts []extract.Action
		if strings.Contains(groups, a) {
			acts = append(acts, extract.Action{Op: "retire", ID: a, By: b, Kind: "duplicate", Reason: "covered by " + b})
		}
		if strings.Contains(groups, pg) {
			acts = append(acts, extract.Action{Op: "retire", ID: old, By: pg, Kind: "outdated", Reason: "covered by " + pg})
		}
		return acts
	})
	plan := filepath.Join(dir, "plan.json")
	if err := runDream([]string{"-all", "-dry-run", "-plan", plan}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	s, _ := store.Open(factsPath())
	if _, err := s.Correct(b, "Workflow nightly-sync.yml подписывает bot на все репозитории, кроме форков и архивных.", nil); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	if err := runDream([]string{"-apply", plan}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	s, _ = store.Open(factsPath())
	defer func() { _ = s.Close() }()
	if f, _ := s.Get(a); f.Status != store.Active {
		t.Errorf("action of a changed group applied: %+v", f)
	}
	if f, _ := s.Get(old); f.Status != store.Retired {
		t.Errorf("action of an unchanged group not applied: %+v", f)
	}
	recs := readDreamLog(t, dir)
	last := recs[len(recs)-1]
	if len(last.Refused) != 1 || !strings.Contains(last.Refused[0], "retire "+a+": group changed since the plan") {
		t.Errorf("refused %q", last.Refused)
	}
}

// A correction made while the model runs makes the group's decisions stale: the whole group is refused, so the
// corrected fact is not overwritten and its duplicate is not retired on the old text.
func TestDreamRefusesGroupChangedDuringTheModelCall(t *testing.T) {
	dir := env(t)
	_, a, b, _ := dreamBase(t)
	const edited = "Workflow nightly-sync.yml подписывает bot на все репозитории, кроме форков и архивных."
	fakeDream(t, func(groups string) []extract.Action {
		if !strings.Contains(groups, a) {
			return nil
		}
		s, err := store.Open(factsPath())
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = s.Close() }()
		if _, err := s.Correct(b, edited, nil); err != nil {
			t.Fatal(err)
		}
		return []extract.Action{{Op: "correct", ID: b, Content: "слитый текст", Reason: "merge"},
			{Op: "retire", ID: a, By: b, Kind: "duplicate", Reason: "merged into " + b}}
	})
	var out bytes.Buffer
	if err := runDream([]string{"-all"}, &out); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if f, _ := s.Get(b); f.Content != edited || f.Version != 2 || f.Status != store.Active {
		t.Errorf("the correction made during the call was overwritten: %+v", f)
	}
	if f, _ := s.Get(a); f.Status != store.Active {
		t.Errorf("a stale action retired a fact: %+v", f)
	}
	recs := readDreamLog(t, dir)
	last := recs[len(recs)-1]
	if len(last.Actions) != 0 {
		t.Errorf("stale actions applied: %+v", last.Actions)
	}
	if len(last.Refused) != 2 {
		t.Errorf("refused %q", last.Refused)
	}
	for _, want := range []string{"retire " + a + ": group changed since it was sent",
		"correct " + b + ": group changed since it was sent"} {
		found := false
		for _, r := range last.Refused {
			if strings.Contains(r, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("refused %q misses %q", last.Refused, want)
		}
	}
	if strings.Contains(out.String(), "retire "+a) ||
		strings.Contains(out.String(), "correct "+b+": ") {
		t.Errorf("a stale action was printed as applied: %q", out.String())
	}
}

func TestDreamPlanFlags(t *testing.T) {
	env(t)
	for _, args := range [][]string{{"-all", "-plan", "p.json"}, {"-apply", "p.json", "-all"}, {"-apply", "p.json", "-dry-run"}} {
		if err := runDream(args, &bytes.Buffer{}); err == nil {
			t.Errorf("%q: want an error", args)
		}
	}
}

func TestDreamSeenFile(t *testing.T) {
	dir := t.TempDir()
	if seen, err := readDreamSeen(dir); err != nil || seen != nil {
		t.Fatalf("no file: %v %v", seen, err)
	}
	want := map[string]int{"01B": 2, "01A": 1}
	if err := writeDreamSeen(dir, want); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "dream.seen"))
	if string(b) != "01A 1\n01B 2\n" {
		t.Errorf("file %q", b)
	}
	got, err := readDreamSeen(dir)
	if err != nil || len(got) != 2 || got["01A"] != 1 || got["01B"] != 2 {
		t.Errorf("read %v %v", got, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dream.seen"), []byte("01A x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readDreamSeen(dir); err == nil {
		t.Error("bad line read without error")
	}
}
