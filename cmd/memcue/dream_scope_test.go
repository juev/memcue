package main

import (
	"bytes"
	"io"
	"os"
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

// promotedBase writes what the promotion of #346 left: b went from each of scopes in turn to global, and a, its
// duplicate learnt under scopeA, was retired for it; dream.log names the promotion.
func promotedBase(t *testing.T, dir, scopeA string, scopes ...string) (a, b store.Fact) {
	t.Helper()
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	b, err = s.AddWith("Workflow nightly-sync.yml подписывает bot на все репозитории владельца кроме форков и архивных.",
		[]string{"nightly-sync"}, "", "", store.Meta{Scope: scopes[0]})
	if err != nil {
		t.Fatal(err)
	}
	for _, sc := range append(scopes[1:], "global") {
		if b, err = s.Relabel(b.ID, b.Version, b.Kind, sc, b.Expires); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(3 * time.Millisecond)
	if a, err = s.AddWith("Каждую ночь workflow nightly-sync.yml подписывает аккаунт bot на все репозитории владельца.",
		[]string{"nightly-sync"}, "", "", store.Meta{Scope: scopeA}); err != nil {
		t.Fatal(err)
	}
	if a, err = s.Retire(a.ID); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(dir, "state", "memcue")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(state, "dream.log"), `{"time":"2026-10-25T17:46:26Z","fresh":1}`+"\n"+
		`{"time":"2026-10-25T18:00:00Z","actions":[{"op":"retire","id":"`+a.ID+`","by":"`+b.ID+
		`","kind":"duplicate","reason":"covered"}],"promoted":["`+b.ID+`"]}`+"\n")
	return a, b
}

// The dream takes back what the promotion made global, once a machine (#607): the fact goes back to the project it
// held in, keeping its creation time, and a duplicate of another project comes back. A later dream that is told the
// two are duplicates keeps both, so the fact does not go back to global.
func TestDreamTakesBackAPromotion(t *testing.T) {
	dir := env(t)
	a, b := promotedBase(t, dir, "repo:example.com/me/two", "repo:example.com/me/one")
	fakeDream(t, func(string) []extract.Action {
		return []extract.Action{{Op: "retire", ID: a.ID, By: b.ID, Kind: "duplicate", Reason: "covered"}}
	})
	var out bytes.Buffer
	if err := dreamModel([]string{"-all"}, &out); err != nil {
		t.Fatal(err)
	}
	s, _ := store.Open(factsPath())
	kept, _ := s.Get(b.ID)
	back, _ := s.Get(a.ID)
	_ = s.Close()
	if kept.Status != store.Active || kept.Scope != "repo:example.com/me/one" || !kept.Created.Equal(b.Created) {
		t.Errorf("promoted fact %+v, want it back in repo:example.com/me/one with its creation time", kept)
	}
	if back.Status != store.Active {
		t.Errorf("duplicate of another project %+v, want it back\n%s", back, out.String())
	}
	if !strings.Contains(out.String(), "refused retire "+a.ID+": a duplicate learnt in") {
		t.Errorf("the dream did not refuse the duplicate of another project:\n%s", out.String())
	}
	log, _ := os.ReadFile(filepath.Join(dir, "state", "memcue", "dream.log"))
	if !strings.Contains(string(log), `"unpromoted":["`+b.ID+` repo:example.com/me/one"]`) ||
		!strings.Contains(string(log), `"reopened":["`+a.ID+`"]`) {
		t.Errorf("dream.log:\n%s", log)
	}

	// Once a machine: a fact made global again by hand stays global.
	s, _ = store.Open(factsPath())
	if _, err := s.Relabel(kept.ID, kept.Version, kept.Kind, "global", kept.Expires); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	if err := dreamModel([]string{"-all"}, &out); err != nil {
		t.Fatal(err)
	}
	s, _ = store.Open(factsPath())
	defer func() { _ = s.Close() }()
	if f, _ := s.Get(b.ID); f.Scope != "global" {
		t.Errorf("second dream moved it again: %+v", f)
	}
}

// Within one repository the duplicate stays retired, and a fact whose scope before the promotion was the clone's old
// name goes back under the key an earlier version had. A fact that was global before a version without a scope is
// left global: it had no project to go back to.
func TestDreamTakesBackAPromotionWithinOneRepository(t *testing.T) {
	dir := env(t)
	clone := filepath.Join(dir, "shop")
	key := gitRepo(t, clone)
	write(t, filepath.Join(dir, "claude", "projects", "-x", "s.jsonl"),
		`{"type":"user","cwd":"`+clone+`","message":{"content":"hi"}}`+"\n")
	a, b := promotedBase(t, dir, "repo:shop", "repo:"+key, "repo:shop")
	if err := dreamModel([]string{"-all"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	s, _ := store.Open(factsPath())
	defer func() { _ = s.Close() }()
	if f, _ := s.Get(b.ID); f.Scope != "repo:"+key {
		t.Errorf("promoted fact %+v, want repo:%s", f, key)
	}
	if f, _ := s.Get(a.ID); f.Status != store.Retired {
		t.Errorf("duplicate of the same repository %+v, want it retired", f)
	}

	dir = env(t)
	_, b = promotedBase(t, dir, "repo:example.com/me/two", "global", "")
	if err := dreamModel([]string{"-all"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	s2, _ := store.Open(factsPath())
	defer func() { _ = s2.Close() }()
	if f, _ := s2.Get(b.ID); f.Scope != "global" {
		t.Errorf("a fact global before the promotion %+v, want it global", f)
	}
}
