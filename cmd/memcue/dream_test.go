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

// dreamBase stores an old fact and two later copies of the watch-all fact from two sessions, plus an unrelated one.
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
	old = add("Postgres демо-базы слушает порт 55432 на loffe.", "postgres", "порт")
	a = add("Репозиторий juev/watch-all каждую ночь запускает workflow watch-all.yml, он подписывает аккаунт juev на все свои репозитории, кроме форков.",
		"watch-all", "подписка", "workflow")
	b = add("Workflow watch-all.yml в репозитории juev/watch-all каждую ночь подписывает juev на все свои репозитории кроме форков и архивных.",
		"watch-all", "github подписка", "workflow")
	other = add("Gradle в Brook требует JAVA_HOME=/opt/homebrew/opt/openjdk@21.", "gradle", "jdk")
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
		return []extract.Action{{Op: "correct", ID: b, Content: "слитый текст", Keywords: []string{"watch-all"}, Reason: "merge"},
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
	// the facts are older than the mark moved back a day, so the dream reviews them
	maybeDream()
	if len(*calls) != 1 {
		t.Errorf("due dream: calls %d", len(*calls))
	}
	if at, _ := lastDream(state); time.Since(at) > time.Minute {
		t.Errorf("dream.at not moved: %v", at)
	}
}
