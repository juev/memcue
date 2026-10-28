package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/juev/memcue/internal/store"
)

func TestReScopeShelf(t *testing.T) {
	for scope, want := range map[string]bool{"shelf:research": true, "shelf:rust-async": true, "shelf:0": true,
		"Shelf:Research!": false, "shelf:Research": false, "shelf:": false, "shelf:-x": false,
		"shelf:" + strings.Repeat("a", 65): false} {
		if got := reScope.MatchString(scope); got != want {
			t.Errorf("reScope(%q) = %v, want %v", scope, got, want)
		}
	}
}

func addShelfFact(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := runAdd(append(args, "--", "Example API allows 60 requests a minute."), &out)
	return strings.TrimSpace(out.String()), err
}

func factScope(t *testing.T, id string) string {
	t.Helper()
	var scope string
	if err := withStore(func(s *store.Store) error {
		f, err := s.Get(id)
		scope = f.Scope
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return scope
}

func TestAddShelf(t *testing.T) {
	env(t)
	byShelf, err := addShelfFact(t, "-kind", "environment", "-shelf", "research")
	if err != nil {
		t.Fatal(err)
	}
	byScope, err := addShelfFact(t, "-kind", "environment", "-scope", "shelf:research")
	if err != nil {
		t.Fatal(err)
	}
	if a, b := factScope(t, byShelf), factScope(t, byScope); a != "shelf:research" || a != b {
		t.Errorf("-shelf stored %q, -scope stored %q", a, b)
	}
	if _, err := addShelfFact(t, "-shelf", "research", "-scope", "shelf:research"); err == nil ||
		!strings.Contains(err.Error(), "-shelf and -scope") {
		t.Errorf("both flags: err = %v", err)
	}
	if _, err := addShelfFact(t, "-shelf", "Research"); err == nil || !strings.Contains(err.Error(), "[a-z0-9]") {
		t.Errorf("bad -shelf: err = %v", err)
	}
	if _, err := addShelfFact(t, "-scope", "shelf:Research X"); err == nil ||
		!strings.Contains(err.Error(), "[a-z0-9][a-z0-9-]{0,63}") {
		t.Errorf("bad shelf scope: err = %v, want the slug shape named", err)
	}
}

func searchOutput(t *testing.T, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	if err := runSearch(args, &out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestSearchReadsTheShelfOnlyWhenNamed(t *testing.T) {
	env(t)
	shelved, err := addShelfFact(t, "-kind", "environment", "-shelf", "research")
	if err != nil {
		t.Fatal(err)
	}
	if got := searchOutput(t, "-in", "all", "Example", "API"); strings.Contains(got, shelved) {
		t.Errorf("-in all returned the shelved fact: %q", got)
	}
	if got := searchOutput(t, "Example", "API"); strings.Contains(got, shelved) {
		t.Errorf("the default returned the shelved fact: %q", got)
	}
	// A shelf named beside "all", in either order, is read too (#532).
	for _, in := range []string{"shelf:research", "shelf", "all,shelf", "shelf,all", "all,shelf:research", "shelf:research,all"} {
		if got := searchOutput(t, "-in", in, "Example", "API"); !strings.Contains(got, shelved) {
			t.Errorf("-in %s did not return the shelved fact: %q", in, got)
		}
	}
}

func TestSearchHintsAtTheShelf(t *testing.T) {
	env(t)
	got := searchOutput(t, "-in", "all", "Example", "API")
	if got != "No stored facts match.\n" {
		t.Errorf("empty shelf: %q", got)
	}
	if _, err := addShelfFact(t, "-kind", "environment", "-shelf", "research"); err != nil {
		t.Fatal(err)
	}
	got = searchOutput(t, "-in", "all", "Example", "API")
	if want := "No stored facts match.\n1 fact on the shelf matches: -in shelf:research\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	// A search that named the shelf and found nothing has nothing to point at.
	for _, in := range []string{"shelf", "all,shelf:research"} {
		if got := searchOutput(t, "-in", in, "nothing", "here"); got != "No stored facts match.\n" {
			t.Errorf("-in %s: %q", in, got)
		}
	}
}

// -json carries the shelf hint beside the facts: the hosts parse -json, and the hint is their only way to the shelf
// (#453).
func TestSearchJSONHintsAtTheShelf(t *testing.T) {
	env(t)
	if _, err := addShelfFact(t, "-kind", "environment", "-shelf", "research"); err != nil {
		t.Fatal(err)
	}
	got := searchOutput(t, "-json", "-in", "all", "Example", "API")
	if want := `{"facts":[],"hints":["1 fact on the shelf matches: -in shelf:research"]}` + "\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := searchOutput(t, "-json", "-in", "all", "nothing", "here"); got != `{"facts":[]}`+"\n" {
		t.Errorf("no hint: %q", got)
	}
}

func TestSearchJSONCarriesScope(t *testing.T) {
	env(t)
	if _, err := addShelfFact(t, "-kind", "environment", "-shelf", "research"); err != nil {
		t.Fatal(err)
	}
	var hits []map[string]any
	if err := unmarshalFacts([]byte(searchOutput(t, "-json", "-in", "shelf", "Example", "API")), &hits); err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0]["scope"] != "shelf:research" {
		t.Errorf("hits %v", hits)
	}
}
