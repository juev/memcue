package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/juev/memcue/internal/extract"
	"github.com/juev/memcue/internal/store"
)

// dupBase stores the same fact twice, learnt under the two origins: b is the older one, which a dream keeps.
func dupBase(t *testing.T, originB, originA string) (a, b string) {
	t.Helper()
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	fb, err := s.AddWith("Workflow nightly-sync.yml подписывает bot на все репозитории владельца кроме форков и архивных.",
		[]string{"nightly-sync", "workflow"}, originB, "", store.Meta{})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * time.Millisecond) // a ULID orders by millisecond only
	fa, err := s.AddWith("Каждую ночь workflow nightly-sync.yml подписывает аккаунт bot на все репозитории владельца, кроме форков.",
		[]string{"nightly-sync", "workflow"}, originA, "", store.Meta{})
	if err != nil {
		t.Fatal(err)
	}
	return fa.ID, fb.ID
}

// A fact that holds in two repositories stays two facts, one in each: the dream retires a duplicate only within one
// project, and makes no fact global (#607, which replaces the promotion of #346). Two keys of one repository are one
// project: the path of a checkout that has a remote now, and the name memcue gave a clone before #343.
func TestDreamKeepsADuplicateOfAnotherProject(t *testing.T) {
	cases := []struct {
		name             string
		originB, originA func(dir string) string
		retired          bool
	}{
		{"two projects", at("example.com/me/one"), at("example.com/me/two"), false},
		{"one project", at("example.com/me/one"), at("example.com/me/one"), true},
		{"the kept fact is global", at("_global"), at("example.com/me/two"), true},
		{"the duplicate was learnt nowhere", at("example.com/me/one"), func(string) string { return "" }, true},
		{"a checkout's path and its remote", at("example.com/team/shop"),
			func(dir string) string { return "dir:" + filepath.Join(dir, "shop") + " 2026-10-20" }, true},
		{"a clone's old name and its key", at("example.com/team/shop"), at("shop"), true},
		{"an old name no clone has", at("example.com/team/shop"), at("store"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := env(t)
			clone := filepath.Join(dir, "shop")
			gitRepo(t, clone)
			write(t, filepath.Join(dir, "claude", "projects", "-x", "s.jsonl"),
				`{"type":"user","cwd":"`+clone+`","message":{"content":"hi"}}`+"\n")
			a, b := dupBase(t, c.originB(dir), c.originA(dir))
			fakeDream(t, func(string) []extract.Action {
				return []extract.Action{{Op: "retire", ID: a, By: b, Kind: "duplicate", Reason: "covered"}}
			})
			var out bytes.Buffer
			if err := dreamModel([]string{"-all"}, &out); err != nil {
				t.Fatal(err)
			}
			s, _ := store.Open(factsPath())
			defer func() { _ = s.Close() }()
			kept, _ := s.Get(b)
			if kept.Status != store.Active || kept.Scope != "" || kept.Version != 1 {
				t.Errorf("kept fact %+v, want it as it was", kept)
			}
			gone, _ := s.Get(a)
			if got := gone.Status == store.Retired; got != c.retired {
				t.Errorf("duplicate retired %v, want %v\n%s", got, c.retired, out.String())
			}
			if refused := strings.Contains(out.String(), "refused retire "+a+": a duplicate learnt in"); refused == c.retired {
				t.Errorf("out %q", out.String())
			}
			if strings.Contains(out.String(), "global ") {
				t.Errorf("a fact was made global: %q", out.String())
			}
		})
	}
}

// A correction that merges a duplicate of another project into the fact it was retired for is refused with the
// retirement: the fact stays as its own project wrote it.
func TestDreamKeepsTheFactADuplicateOfAnotherProjectWasMergedInto(t *testing.T) {
	env(t)
	a, b := dupBase(t, "example.com/me/one 2026-10-19", "example.com/me/two 2026-10-20")
	fakeDream(t, func(string) []extract.Action {
		return []extract.Action{
			{Op: "retire", ID: a, By: b, Kind: "duplicate", Reason: "covered"},
			{Op: "correct", ID: b, Content: "Слитый текст обоих.", Reason: "merge"},
		}
	})
	var out bytes.Buffer
	if err := dreamModel([]string{"-all"}, &out); err != nil {
		t.Fatal(err)
	}
	s, _ := store.Open(factsPath())
	defer func() { _ = s.Close() }()
	for _, id := range []string{a, b} {
		if f, _ := s.Get(id); f.Status != store.Active || f.Version != 1 {
			t.Errorf("%s changed: %+v\n%s", id, f, out.String())
		}
	}
	if !strings.Contains(out.String(), "refused correct "+b) {
		t.Errorf("out %q", out.String())
	}
}

// at is an origin learnt in key.
func at(key string) func(string) string {
	return func(string) string { return key + " 2026-10-19" }
}
