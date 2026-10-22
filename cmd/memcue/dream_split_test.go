package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/juev/memcue/internal/extract"
	"github.com/juev/memcue/internal/store"
)

// fakeSplit answers every split call with answer and records the inputs.
func fakeSplit(t *testing.T, answer func(input string) ([]extract.Split, error)) *[]string {
	t.Helper()
	var calls []string
	old := splitFn
	splitFn = func(input string) (extract.SplitResult, error) {
		calls = append(calls, input)
		splits, err := answer(input)
		return extract.SplitResult{Splits: splits, CostUSD: 0.001}, err
	}
	t.Cleanup(func() { splitFn = old })
	return &calls
}

const (
	compoundText = "На loffe стоит memcue 0.4.0; бинарь memcue лежит в /opt/data/bin."
	versionPart  = "На loffe стоит memcue 0.4.0."
	pathPart     = "Бинарь memcue на loffe лежит в /opt/data/bin."
)

// splitBase stores a compound fact and an older fact that repeats one of its parts.
func splitBase(t *testing.T) (compound, path string) {
	t.Helper()
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	p, err := s.AddWith("Бинарь memcue на хосте loffe лежит в каталоге /opt/data/bin.", []string{"memcue", "loffe"},
		"hermes 2026-10-20", "", store.Meta{Kind: "environment", Scope: "host:loffe"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.AddWith(compoundText, []string{"memcue"}, "hermes 2026-10-22", "",
		store.Meta{Kind: "environment", Scope: "host:loffe", Source: "tool"})
	if err != nil {
		t.Fatal(err)
	}
	return c.ID, p.ID
}

// splitByText splits the fact whose text is compoundText, wherever it is in the input.
func splitByText(input string) ([]extract.Split, error) {
	var req struct {
		Facts []struct{ ID, Text string } `json:"facts"`
	}
	if err := json.Unmarshal([]byte(input), &req); err != nil {
		return nil, err
	}
	for _, f := range req.Facts {
		if f.Text == compoundText {
			return []extract.Split{{ID: f.ID, Parts: []string{versionPart, pathPart}, Reason: "version and path"}}, nil
		}
	}
	return nil, nil
}

// A dream splits a compound fact before grouping: the parts are approved with the original's meta, and the part that
// repeats an older fact reaches the model in a group with it.
func TestDreamSplitsACompoundFactBeforeGrouping(t *testing.T) {
	dir := env(t)
	compound, path := splitBase(t)
	splits := fakeSplit(t, splitByText)
	calls := fakeDream(t, func(string) []extract.Action { return nil })
	var out bytes.Buffer
	if err := runDream([]string{"-all"}, &out); err != nil {
		t.Fatal(err)
	}
	if len(*splits) != 1 {
		t.Fatalf("split calls %d", len(*splits))
	}
	s, _ := store.Open(factsPath())
	defer func() { _ = s.Close() }()
	if f, _ := s.Get(compound); f.Status != store.Retired {
		t.Errorf("compound fact %+v", f)
	}
	active, _ := s.Active()
	var parts []store.Fact
	for _, f := range active {
		if f.Content == versionPart || f.Content == pathPart {
			parts = append(parts, f)
		}
	}
	if len(parts) != 2 {
		t.Fatalf("parts in recall %+v", parts)
	}
	for _, p := range parts {
		if p.Origin != "hermes 2026-10-22" || p.Scope != "host:loffe" || p.Source != "tool" || p.Review != "" {
			t.Errorf("part %+v", p)
		}
	}
	if len(*calls) != 1 || !strings.Contains((*calls)[0], pathPart) || !strings.Contains((*calls)[0], path) ||
		strings.Contains((*calls)[0], compound) {
		t.Errorf("groups sent %q", *calls)
	}
	if !strings.Contains(out.String(), "split "+compound) || !strings.Contains(out.String(), "+ ") {
		t.Errorf("out %q", out.String())
	}
	recs := readDreamLog(t, dir)
	if r := recs[len(recs)-1]; len(r.Splits) != 1 || r.Splits[0].ID != compound || r.CostUSD < 0.001 {
		t.Errorf("record %+v", r)
	}
	// The parts were reviewed by this dream: the next one has nothing new.
	*splits, *calls = nil, nil
	if err := runDream(nil, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if len(*splits) != 0 || len(*calls) != 0 {
		t.Errorf("the next dream sent splits %q and groups %q", *splits, *calls)
	}
}

// A dry run puts the split in the plan and changes nothing; -apply splits without the model, refuses the group that
// held the compound fact, and leaves the parts for the next dream to group.
func TestDreamPlanCarriesTheSplit(t *testing.T) {
	dir := env(t)
	compound, path := splitBase(t)
	splits := fakeSplit(t, splitByText)
	fakeDream(t, func(groups string) []extract.Action {
		if !strings.Contains(groups, compound) {
			return nil
		}
		return []extract.Action{{Op: "retire", ID: compound, By: path, Kind: "outdated", Reason: "older path"}}
	})
	plan := filepath.Join(dir, "plan.json")
	var dry bytes.Buffer
	if err := runDream([]string{"-all", "-dry-run", "-plan", plan}, &dry); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dry.String(), "+ "+pathPart) {
		t.Errorf("dry run out %q", dry.String())
	}
	if f := vetStatus(t, compound); f.Status != store.Active || f.Version != 1 {
		t.Fatalf("a dry run changed the base: %+v", f)
	}
	var p dreamPlan
	b, _ := os.ReadFile(plan)
	if err := json.Unmarshal(b, &p); err != nil || len(p.Splits) != 1 || p.Splits[0].ID != compound ||
		p.Splits[0].Version != 1 || len(p.Splits[0].Parts) != 2 {
		t.Fatalf("plan %s: %v", b, err)
	}

	*splits = nil
	var out bytes.Buffer
	if err := runDream([]string{"-apply", plan}, &out); err != nil {
		t.Fatal(err)
	}
	if len(*splits) != 0 {
		t.Errorf("-apply called the model %d times", len(*splits))
	}
	if f := vetStatus(t, compound); f.Status != store.Retired || f.Version != 2 {
		t.Errorf("compound fact after apply %+v", f)
	}
	if f := vetStatus(t, path); f.Status != store.Active {
		t.Errorf("the group action decided on the compound fact was applied: %+v", f)
	}
	recs := readDreamLog(t, dir)
	last := recs[len(recs)-1]
	if len(last.Splits) != 1 || len(last.Actions) != 0 || len(last.Refused) != 1 ||
		!strings.Contains(last.Refused[0], "group changed since the plan") {
		t.Errorf("apply record %+v", last)
	}
	// The next dream reviews the parts, which the plan never grouped.
	if err := runDream(nil, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if len(*splits) != 1 || !strings.Contains((*splits)[0], pathPart) || strings.Contains((*splits)[0], path) {
		t.Errorf("the next dream split input %q", *splits)
	}
}

// -apply refuses a split whose fact changed since the plan and still marks the dream.
func TestDreamApplyRefusesAChangedSplit(t *testing.T) {
	dir := env(t)
	compound, _ := splitBase(t)
	fakeSplit(t, splitByText)
	fakeDream(t, func(string) []extract.Action { return nil })
	plan := filepath.Join(dir, "plan.json")
	if err := runDream([]string{"-all", "-dry-run", "-plan", plan}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if err := runCorrect([]string{compound, "На loffe стоит memcue 0.4.1; бинарь memcue лежит в /opt/data/bin."},
		&bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if err := runDream([]string{"-apply", plan}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if f := vetStatus(t, compound); f.Status != store.Active || f.Version != 2 {
		t.Errorf("a changed fact was split: %+v", f)
	}
	recs := readDreamLog(t, dir)
	if last := recs[len(recs)-1]; len(last.Splits) != 0 || len(last.Refused) != 1 ||
		!strings.Contains(last.Refused[0], "split "+compound) {
		t.Errorf("apply record %+v", last)
	}
}

// An answer the dream cannot use is refused, and a failed split call leaves its facts and does not stop the dream.
func TestDreamRefusesBadSplits(t *testing.T) {
	dir := env(t)
	compound, path := splitBase(t)
	fakeSplit(t, func(string) ([]extract.Split, error) {
		return []extract.Split{
			{ID: "01NOTSENT", Parts: []string{"a", "b"}},
			{ID: path, Parts: []string{"one part"}},
			{ID: compound, Parts: []string{versionPart, pathPart}},
			{ID: compound, Parts: []string{versionPart, pathPart}},
		}, nil
	})
	fakeDream(t, func(string) []extract.Action { return nil })
	if err := runDream([]string{"-all", "-dry-run"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	recs := readDreamLog(t, dir)
	r := recs[len(recs)-1]
	if len(r.Splits) != 1 || len(r.Refused) != 3 {
		t.Errorf("record %+v", r)
	}
	for _, want := range []string{"not a fact of the batch", "1 part(s)", "second split"} {
		if !strings.Contains(strings.Join(r.Refused, "\n"), want) {
			t.Errorf("refused %q lacks %q", r.Refused, want)
		}
	}

	fakeSplit(t, func(string) ([]extract.Split, error) { return nil, errors.New("rate limited") })
	calls := fakeDream(t, func(string) []extract.Action { return nil })
	if err := runDream([]string{"-all"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if f := vetStatus(t, compound); f.Status != store.Active {
		t.Errorf("a failed split call changed the fact: %+v", f)
	}
	recs = readDreamLog(t, dir)
	if r := recs[len(recs)-1]; r.SplitErrors != 1 || r.Error != "" || len(*calls) != 1 {
		t.Errorf("record %+v, dream calls %d", r, len(*calls))
	}
}
