package main

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

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
	if err := dreamModel([]string{"-all"}, &out); err != nil {
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
	if err := dreamModel(nil, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if len(*sent) != 0 {
		t.Errorf("the next dream expanded %q again", *sent)
	}
}

// The parts of a split have no queries yet, and are new to the next dream, so it expands them. The compound fact
// itself is not expanded: its split goes first.
func TestDreamExpandsTheParts(t *testing.T) {
	env(t)
	compound, _ := splitBase(t)
	fakeSplit(t, splitByText)
	fakeExpand(t, nil)
	fakeDream(t, func(string) []extract.Action { return nil })
	if err := dreamModel([]string{"-all"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if f := vetStatus(t, compound); f.Status != store.Retired || len(f.Queries) != 0 {
		t.Errorf("the compound fact was expanded instead of split: %+v", f)
	}
	if err := dreamModel(nil, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
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

// An answer that expands a fact and acts on its group applies both: the version the expansion writes is the answer's
// own, so it does not make the group look changed.
func TestDreamAppliesQueriesAndTheGroupOfTheFact(t *testing.T) {
	dir := env(t)
	_, a, b, _ := dreamBase(t)
	fakeExpand(t, nil)
	fakeDream(t, mergeAnswer(a, b))
	if err := dreamModel([]string{"-all"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
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

// Versions that only added search queries or new labels do not make a fact new to the next dream: a whole base expanded
// or labelled at once must not send every fact to the split step and the groups again.
func TestDreamDoesNotReviewAFactAgainForItsQueries(t *testing.T) {
	env(t)
	_, a, b, _ := dreamBase(t)
	calls := fakeDream(t, func(string) []extract.Action { return nil })
	if err := dreamModel([]string{"-all"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	s, _ := store.Open(factsPath())
	fa := vetStatus(t, a)
	fa, err := s.SetQueries(a, fa.Version, []string{"новые запросы"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Relabel(a, fa.Version, "pitfall", "repo:nightly-sync", ""); err != nil {
		t.Fatal(err)
	}
	fb := vetStatus(t, b)
	if _, err := s.Correct(b, fb.Content+" Проверено.", fb.Keywords); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	splits := fakeSplit(t, func(input string) ([]extract.Split, error) { return nil, nil })
	*calls = nil
	if err := dreamModel(nil, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	sent := strings.Join(*splits, " ")
	if strings.Contains(sent, a) {
		t.Errorf("a fact with new queries and labels only was reviewed again: %q", sent)
	}
	if !strings.Contains(sent, b) {
		t.Errorf("a corrected fact was not reviewed: %q", sent)
	}
}

// Facts a dream does not see as fresh still get queries: up to packetExpand a dream, the newest first, without a review
// of their text. A base written before extraction wrote queries is expanded over a few dreams (#276).
func TestDreamExpandsTheBacklog(t *testing.T) {
	env(t)
	old, a, b, other := dreamBase(t)
	byAge := []string{old, a, b, other}
	slices.Sort(byAge) // ids order by time
	oldExpand := packetExpand
	packetExpand = 2
	t.Cleanup(func() { packetExpand = oldExpand })
	noSweep(t) // the merge sweep reviews old facts by design; this test is about their queries
	sent := fakeExpand(t, nil)
	calls := fakeDream(t, func(string) []extract.Action { return nil })
	nothingFresh := []string{"-since", time.Now().Add(time.Hour).Format(time.RFC3339)}

	if err := dreamModel(nothingFresh, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if want := []string{byAge[3], byAge[2]}; !slices.Equal(*sent, want) {
		t.Fatalf("first dream sent %q, want the two newest %q", *sent, want)
	}
	*sent = nil
	if err := dreamModel(nothingFresh, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if want := []string{byAge[1], byAge[0]}; !slices.Equal(*sent, want) {
		t.Fatalf("second dream sent %q, want the rest %q", *sent, want)
	}
	*sent = nil
	if err := dreamModel(nothingFresh, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if len(*sent) != 0 {
		t.Errorf("third dream sent %q, want nothing", *sent)
	}
	for _, id := range byAge {
		if f := vetStatus(t, id); len(f.Queries) != 1 || f.Version != 2 {
			t.Errorf("fact %s: %+v", id, f)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("the backlog was reviewed: %d dream calls", len(*calls))
	}
}
