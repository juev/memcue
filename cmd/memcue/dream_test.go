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
	a = add("Репозиторий example/nightly-sync каждую ночь запускает workflow nightly-sync.yml, он подписывает аккаунт bot на все свои репозитории, кроме форков.",
		"nightly-sync", "подписка", "workflow")
	b = add("Workflow nightly-sync.yml в репозитории example/nightly-sync каждую ночь подписывает bot на все свои репозитории кроме форков и архивных.",
		"nightly-sync", "github подписка", "workflow")
	other = add("Gradle в demo-app требует JAVA_HOME=/opt/jdk-21.", "gradle", "jdk")
	return
}

func TestDreamGroupsCopiesAndRetiresOne(t *testing.T) {
	dir := env(t)
	old, a, b, other := dreamBase(t)
	calls := fakeDream(t, func(string) []extract.Action {
		return []extract.Action{{Op: "retire", ID: a, Reason: "covered by " + b}}
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

func TestDreamDryRunChangesNothing(t *testing.T) {
	dir := env(t)
	_, a, b, _ := dreamBase(t)
	fakeDream(t, func(string) []extract.Action {
		return []extract.Action{{Op: "correct", ID: b, Content: "слитый текст", Reason: "merge"},
			{Op: "retire", ID: a, Reason: "covered"}}
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
			{Op: "retire", ID: a, Reason: "merged into " + b}}
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
			{Op: "retire", ID: a, Reason: "merged"}}
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
	groups := [][]store.Fact{{{ID: "A"}, {ID: "B"}}}
	ok, refused := checkActions(groups, []extract.Action{
		{Op: "retire", ID: "Z", Reason: "outside"},
		{Op: "retire", ID: "A", Reason: "dup"},
		{Op: "retire", ID: "A", Reason: "again"},
		{Op: "retire", ID: "B", Reason: "last one"},
		{Op: "correct", ID: "B", Reason: "no text"},
		{Op: "delete", ID: "B", Reason: "unknown"},
	})
	if len(ok) != 1 || ok[0].ID != "A" {
		t.Errorf("ok %+v", ok)
	}
	if len(refused) != 5 {
		t.Errorf("refused %q", refused)
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
				{Op: "retire", ID: a, Reason: "merged into " + b}}
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
