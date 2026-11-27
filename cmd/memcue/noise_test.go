package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/juev/memcue/internal/extract"
	"github.com/juev/memcue/internal/store"
)

func TestCheckActionsWithNoise(t *testing.T) {
	groups := [][]store.Fact{{{ID: "A", Version: 1}}, {{ID: "B", Version: 1}, {ID: "C", Version: 1}}}
	noise := map[string]int{"A": 1, "B": 1, "C": 1}
	for _, tc := range []struct {
		name   string
		action extract.Action
		want   bool
	}{
		{"singleton eligible", extract.Action{Op: "retire", ID: "A", Kind: "noise", Reason: "one-off release event"}, true},
		{"group member eligible", extract.Action{Op: "retire", ID: "B", Kind: "noise", Reason: "transient auth state"}, true},
		{"reason required", extract.Action{Op: "retire", ID: "A", Kind: "noise"}, false},
		{"resolved singleton still refused", extract.Action{Op: "retire", ID: "A", Kind: "resolved", Reason: "not a noise action"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ok, refused := checkActionsWithNoise(groups, []extract.Action{tc.action}, noise)
			if (len(ok) == 1) != tc.want || (len(refused) > 0) == tc.want {
				t.Errorf("ok=%v refused=%v", ok, refused)
			}
		})
	}
	ok, refused := checkActionsWithNoise(groups, []extract.Action{
		{Op: "retire", ID: "B", Kind: "noise", Reason: "transient state"},
		{Op: "retire", ID: "C", Kind: "noise", Reason: "would empty the group"},
	}, noise)
	if len(ok) != 1 || len(refused) != 1 {
		t.Errorf("retiring a whole ordinary group: ok=%v refused=%v", ok, refused)
	}
	for _, noiseSet := range []map[string]int{nil, {"B": 1}} {
		action := extract.Action{Op: "retire", ID: "A", Kind: "noise", Reason: "not eligible"}
		if ok, _ := checkActionsWithNoise(groups, []extract.Action{action}, noiseSet); len(ok) != 0 {
			t.Errorf("noise set %v accepted an ineligible fact", noiseSet)
		}
	}
}

func TestDreamRetiresEligibleSingletonNoise(t *testing.T) {
	env(t)
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	noise, err := s.AddWith("Fixed in main 7b218eb: the inherited dream interval no longer breaks tests.",
		"x 2026-10-19", time.Now().AddDate(0, 0, -1).Format(time.DateOnly), store.Meta{Kind: "pitfall", Source: "tool"})
	if err != nil {
		t.Fatal(err)
	}
	kept, err := s.AddWith("Redis queues on host amber use port 6379 for synthetic fixture traffic.",
		"x 2026-10-19", "", store.Meta{Kind: "environment", Source: "tool"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	calls := fakeDream(t, func(string) []extract.Action {
		return []extract.Action{{Op: "retire", ID: noise.ID, Kind: "noise", Reason: "the current task is fixed"}}
	})
	if err := dreamModel([]string{"-all"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || !strings.Contains((*calls)[0], "Noise candidate") || !strings.Contains((*calls)[0], noise.ID) {
		t.Fatalf("noise candidate was not presented to the model: %+v", *calls)
	}
	if got := storedFact(t, noise.ID); got.Status != store.Retired || got.Version != 2 {
		t.Fatalf("noise fact was not retired with a version bump: %+v", got)
	}
	if got := storedFact(t, kept.ID); got.Status != store.Active || got.Version != 1 {
		t.Fatalf("durable fact changed: %+v", got)
	}
}

func TestDreamListMarksNoiseWithoutModel(t *testing.T) {
	env(t)
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddWith("First npm publish of memcue-pi completed with a temporary placeholder.",
		"x 2026-10-19", "", store.Meta{Kind: "pitfall", Source: "tool"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runDream([]string{"-list", "-all"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Noise candidate") {
		t.Fatalf("dream -list did not expose the noise candidate: %q", out.String())
	}
}

func TestDreamNoiseOnlySkipsDurableWork(t *testing.T) {
	dir := env(t)
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	noise, err := s.AddWith("First npm publish of memcue-pi completed with a temporary placeholder.",
		"x 2026-10-19", "", store.Meta{Kind: "pitfall", Source: "tool"})
	if err != nil {
		t.Fatal(err)
	}
	kept, err := s.AddWith("Redis queues on host amber use port 6379 for synthetic fixture traffic.",
		"x 2026-10-19", "", store.Meta{Kind: "environment", Source: "tool", Scope: "global"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	calls := fakeDream(t, func(string) []extract.Action {
		return []extract.Action{{Op: "retire", ID: noise.ID, Kind: "noise", Reason: "one-off release event"}}
	})
	if err := dreamModel([]string{"-all", "-noise"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || strings.Contains((*calls)[0], kept.Content) {
		t.Fatalf("noise-only review included durable work: %+v", *calls)
	}
	if got := storedFact(t, noise.ID); got.Status != store.Retired {
		t.Fatalf("noise-only candidate was not retired: %+v", got)
	}
	if got := storedFact(t, kept.ID); got.Status != store.Active {
		t.Fatalf("durable fact changed during noise-only review: %+v", got)
	}
	for _, file := range []string{"dream.seen", "dream.at", "dream.merge", "dream.scope", unpromotedFile} {
		if _, err := os.Stat(filepath.Join(dir, "state", "memcue", file)); !os.IsNotExist(err) {
			t.Errorf("noise-only review created ordinary dream state %s: %v", file, err)
		}
	}
}
