package main

import (
	"bytes"
	"encoding/json"
	"errors"
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
	splitFn = func(input string) (splitReply, error) {
		calls = append(calls, input)
		splits, err := answer(input)
		return splitReply{Splits: splits}, err
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

// A dream splits a compound fact: the parts are approved with the original's meta. The packet grouped the compound
// fact as it was, so an action decided on that group is refused once the split goes first, and the next dream groups
// the parts with their neighbours: the part that repeats an older fact reaches the model with it.
func TestDreamSplitsACompoundFact(t *testing.T) {
	dir := env(t)
	compound, path := splitBase(t)
	splits := fakeSplit(t, splitByText)
	calls := fakeDream(t, func(groups string) []extract.Action {
		if !strings.Contains(groups, compound) {
			return nil
		}
		return []extract.Action{{Op: "retire", ID: compound, By: path, Kind: "outdated", Reason: "older path"}}
	})
	var out bytes.Buffer
	if err := dreamModel([]string{"-all"}, &out); err != nil {
		t.Fatal(err)
	}
	if len(*splits) != 1 {
		t.Fatalf("split calls %d", len(*splits))
	}
	s, _ := store.Open(factsPath())
	defer func() { _ = s.Close() }()
	if f, _ := s.Get(compound); f.Status != store.Retired || f.Version != 2 {
		t.Errorf("compound fact %+v", f)
	}
	if f, _ := s.Get(path); f.Status != store.Active {
		t.Errorf("the group action decided on the compound fact was applied: %+v", f)
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
	if !strings.Contains(out.String(), "split "+compound) || !strings.Contains(out.String(), "+ ") {
		t.Errorf("out %q", out.String())
	}
	recs := readDreamLog(t, dir)
	last := recs[len(recs)-1]
	if len(last.Splits) != 1 || last.Splits[0].ID != compound || len(last.Actions) != 0 || len(last.Refused) != 1 ||
		!strings.Contains(last.Refused[0], "group changed since the packet") {
		t.Errorf("record %+v", last)
	}
	// The next dream reviews the parts, which this one never grouped.
	*splits, *calls = nil, nil
	if err := dreamModel(nil, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if len(*splits) != 1 || !strings.Contains((*splits)[0], pathPart) || strings.Contains((*splits)[0], path) {
		t.Errorf("the next dream split input %q", *splits)
	}
	if len(*calls) != 1 || !strings.Contains((*calls)[0], pathPart) || !strings.Contains((*calls)[0], path) {
		t.Errorf("the next dream groups %q", *calls)
	}
}

// -apply refuses a split whose fact changed since the packet and still marks the dream.
func TestDreamApplyRefusesAChangedSplit(t *testing.T) {
	dir := env(t)
	compound, _ := splitBase(t)
	fakeSplit(t, splitByText)
	fakeDream(t, func(string) []extract.Action { return nil })
	plan, err := prepareAnswer([]string{"-all"}, &bytes.Buffer{})
	if err != nil {
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

// An answer the dream cannot use is refused alone, and the dream is still marked.
func TestDreamRefusesBadSplits(t *testing.T) {
	dir := env(t)
	compound, path := splitBase(t)
	fakeSplit(t, func(string) ([]extract.Split, error) {
		return []extract.Split{
			{ID: "01NOTSENT", Parts: []string{"a", "b"}},
			{ID: path, Parts: []string{"one part", "  "}},
			{ID: compound, Parts: []string{versionPart, pathPart}},
			{ID: compound, Parts: []string{versionPart, pathPart}},
		}, nil
	})
	fakeDream(t, func(string) []extract.Action { return nil })
	if err := dreamModel([]string{"-all"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	recs := readDreamLog(t, dir)
	r := recs[len(recs)-1]
	if len(r.Splits) != 1 || len(r.Refused) != 3 || r.Error != "" {
		t.Errorf("record %+v", r)
	}
	for _, want := range []string{"not a fact of the packet to split", "1 part(s)", "second split"} {
		if !strings.Contains(strings.Join(r.Refused, "\n"), want) {
			t.Errorf("refused %q lacks %q", r.Refused, want)
		}
	}
	if _, ok := lastDream(filepath.Join(dir, "state", "memcue")); !ok {
		t.Error("-apply did not mark the dream")
	}
}

// A session whose model failed applies nothing: the base and the packet stay, and the next answer can still use it.
func TestDreamModelFailureChangesNothing(t *testing.T) {
	dir := env(t)
	compound, _ := splitBase(t)
	fakeSplit(t, func(string) ([]extract.Split, error) { return nil, errModel })
	if err := dreamModel([]string{"-all"}, &bytes.Buffer{}); !errors.Is(err, errModel) {
		t.Fatalf("err %v", err)
	}
	if f := vetStatus(t, compound); f.Status != store.Active || f.Version != 1 {
		t.Errorf("a failed answer changed the fact: %+v", f)
	}
	if _, ok := lastDream(filepath.Join(dir, "state", "memcue")); ok {
		t.Error("a dream with no answer was marked")
	}
}
