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

// fakeExpand answers every expansion call with one query per fact sent, "запрос <id>", and records the ids it got.
func fakeExpand(t *testing.T, fail error) *[]string {
	t.Helper()
	var sent []string
	old := expandFn
	expandFn = func(input string) (extract.ExpandResult, error) {
		var req struct {
			Facts []struct{ ID, Text string } `json:"facts"`
		}
		if err := json.Unmarshal([]byte(input), &req); err != nil {
			return extract.ExpandResult{}, err
		}
		var r extract.ExpandResult
		for _, f := range req.Facts {
			sent = append(sent, f.ID)
			r.Facts = append(r.Facts, extract.Expansion{ID: f.ID, Queries: []string{"запрос " + f.ID}})
		}
		return r, fail
	}
	t.Cleanup(func() { expandFn = old })
	return &sent
}

// A dream gives search queries to the fresh facts that have none, and leaves the facts that have them alone; the next
// dream has nothing to expand.
func TestDreamExpandsFactsWithoutQueries(t *testing.T) {
	dir := env(t)
	old, a, b, other := dreamBase(t)
	s, _ := store.Open(factsPath())
	if _, err := s.SetQueries(other, 1, []string{"уже есть"}); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	sent := fakeExpand(t, nil)
	fakeDream(t, func(string) []extract.Action { return nil })
	var out bytes.Buffer
	if err := runDream([]string{"-all"}, &out); err != nil {
		t.Fatal(err)
	}
	if len(*sent) != 3 || strings.Contains(strings.Join(*sent, " "), other) {
		t.Fatalf("sent %q, want the three facts without queries", *sent)
	}
	for _, id := range []string{old, a, b} {
		if f := vetStatus(t, id); len(f.Queries) != 1 || f.Queries[0] != "запрос "+id || f.Version != 2 {
			t.Errorf("fact %s: %+v", id, f)
		}
	}
	if !strings.Contains(out.String(), "expanded 3 facts") {
		t.Errorf("out %q", out.String())
	}
	if r := readDreamLog(t, dir); r[len(r)-1].Expanded != 3 {
		t.Errorf("record %+v", r[len(r)-1])
	}
	*sent = nil
	if err := runDream(nil, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if len(*sent) != 0 {
		t.Errorf("the next dream expanded %q again", *sent)
	}
}

// The parts of a split have no queries yet, so the same dream expands them.
func TestDreamExpandsTheParts(t *testing.T) {
	env(t)
	compound, _ := splitBase(t)
	fakeSplit(t, splitByText)
	sent := fakeExpand(t, nil)
	fakeDream(t, func(string) []extract.Action { return nil })
	if err := runDream([]string{"-all"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(*sent, " "), compound) {
		t.Errorf("the compound fact was expanded: %q", *sent)
	}
	s, _ := store.Open(factsPath())
	defer func() { _ = s.Close() }()
	act, _ := s.Active()
	for _, f := range act {
		if (f.Content == versionPart || f.Content == pathPart) && len(f.Queries) != 1 {
			t.Errorf("part without queries: %+v", f)
		}
	}
}

// A dry run puts the queries in the plan; -apply writes them without the model and still applies the groups whose
// facts it expanded, since that change is the plan's own.
func TestDreamPlanCarriesQueries(t *testing.T) {
	dir := env(t)
	_, a, b, _ := dreamBase(t)
	sent := fakeExpand(t, nil)
	fakeDream(t, mergeAnswer(a, b))
	plan := filepath.Join(dir, "plan.json")
	if err := runDream([]string{"-all", "-dry-run", "-plan", plan}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if f := vetStatus(t, a); f.Version != 1 || len(f.Queries) != 0 {
		t.Fatalf("a dry run wrote %+v", f)
	}
	var p dreamPlan
	raw, _ := os.ReadFile(plan)
	if err := json.Unmarshal(raw, &p); err != nil || len(p.Expansions) != 4 {
		t.Fatalf("plan %s: %v", raw, err)
	}
	*sent = nil
	if err := runDream([]string{"-apply", plan}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if len(*sent) != 0 {
		t.Errorf("-apply called the model: %q", *sent)
	}
	if f := vetStatus(t, a); f.Status != store.Retired {
		t.Errorf("the group action was refused after the expansion: %+v", f)
	}
	if f := vetStatus(t, b); f.Content != "слитый текст" {
		t.Errorf("b %+v", f)
	}
	recs := readDreamLog(t, dir)
	if last := recs[len(recs)-1]; last.Expanded != 4 || len(last.Refused) != 0 || len(last.Actions) != 2 {
		t.Errorf("apply record %+v", last)
	}
}

// A failed expansion call leaves the facts for the next dream and does not stop this one.
func TestDreamExpansionFailureIsCounted(t *testing.T) {
	dir := env(t)
	_, a, _, _ := dreamBase(t)
	fakeExpand(t, errors.New("rate limited"))
	calls := fakeDream(t, func(string) []extract.Action { return nil })
	if err := runDream([]string{"-all"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if f := vetStatus(t, a); len(f.Queries) != 0 {
		t.Errorf("a failed call wrote queries: %+v", f)
	}
	recs := readDreamLog(t, dir)
	if r := recs[len(recs)-1]; r.ExpandErrors != 1 || r.Error != "" || len(*calls) != 1 {
		t.Errorf("record %+v, dream calls %d", r, len(*calls))
	}
}
