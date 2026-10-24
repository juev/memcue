package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/juev/memcue/internal/extract"
	"github.com/juev/memcue/internal/store"
)

// promoteBase stores the same fact twice, learnt under the two origins: b is the older one, which a dream keeps.
func promoteBase(t *testing.T, originB, originA string) (a, b string) {
	t.Helper()
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	fb, err := s.Add("Workflow nightly-sync.yml подписывает bot на все репозитории владельца кроме форков и архивных.",
		[]string{"nightly-sync", "workflow"}, originB)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * time.Millisecond) // a ULID orders by millisecond only
	fa, err := s.Add("Каждую ночь workflow nightly-sync.yml подписывает аккаунт bot на все репозитории владельца, кроме форков.",
		[]string{"nightly-sync", "workflow"}, originA)
	if err != nil {
		t.Fatal(err)
	}
	return fa.ID, fb.ID
}

// A duplicate learnt in another project makes the fact it was retired for global: a fact learnt twice,
// independently, in two projects holds in both (#346). Within one project, or next to a global fact, nothing moves.
func TestDreamPromotesADuplicateOfAnotherProject(t *testing.T) {
	cases := []struct {
		name             string
		originB, originA string
		want             string
	}{
		{"two projects", "example.com/me/one 2026-10-19", "example.com/me/two 2026-10-20", "global"},
		{"one project", "example.com/me/one 2026-10-19", "example.com/me/one 2026-10-20", ""},
		{"the kept fact is global", "_global 2026-10-19", "example.com/me/two 2026-10-20", ""},
		{"the duplicate was learnt nowhere", "example.com/me/one 2026-10-19", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env(t)
			a, b := promoteBase(t, c.originB, c.originA)
			fakeDream(t, func(string) []extract.Action {
				return []extract.Action{{Op: "retire", ID: a, By: b, Kind: "duplicate", Reason: "covered"}}
			})
			var out bytes.Buffer
			if err := dreamModel([]string{"-all"}, &out); err != nil {
				t.Fatal(err)
			}
			s, _ := store.Open(factsPath())
			defer func() { _ = s.Close() }()
			f, _ := s.Get(b)
			if f.Status != store.Active || f.Scope != c.want || f.Origin != c.originB {
				t.Errorf("kept fact %+v, want scope %q and its origin", f, c.want)
			}
			if got := strings.Contains(out.String(), "global "+b); got != (c.want == "global") {
				t.Errorf("out %q", out.String())
			}
		})
	}
}
