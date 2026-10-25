package recall

import (
	"path/filepath"
	"testing"

	"github.com/juev/memcue/internal/store"
)

func emptyBase(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "facts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// The same text stored four times ties on BM25: the host's fact comes first, then the one learnt in the session's
// project, then the global one, then the rest.
func TestSearchOrdersTiesByHostProjectGlobal(t *testing.T) {
	s := emptyBase(t)
	const text = "Брокер nats слушает 4222, мониторинг на 8222."
	for _, f := range []struct{ origin, scope string }{
		{"example.com/elsewhere/z 2026-09-10", ""},
		{"_global 2026-09-11", "global"},
		{"example.com/me/app 2026-09-12", "repo:example.com/me/app"},
		{"example.com/elsewhere/y 2026-09-13", "host:mac"},
	} {
		if _, err := s.AddWith(text, []string{"nats"}, f.origin, "", store.Meta{Scope: f.scope}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Search(s, "nats", []string{"example.com/me/app"}, "mac", 10, MinScore)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"example.com/elsewhere/y 2026-09-13", "example.com/me/app 2026-09-12", "_global 2026-09-11",
		"example.com/elsewhere/z 2026-09-10"}
	if len(got) != len(want) {
		t.Fatalf("got %d facts, want %d", len(got), len(want))
	}
	for i, h := range got {
		if h.Origin != want[i] {
			t.Errorf("place %d: %q, want %q", i, h.Origin, want[i])
		}
	}
}

// A fact learnt under any key of the session's project wins a tie: a fork's session prefers a fact of its upstream
// over the same text learnt elsewhere.
func TestSearchAnyProjectKeyWinsTies(t *testing.T) {
	s := emptyBase(t)
	for _, origin := range []string{"example.com/elsewhere/z 2026-09-10", "example.com/them/upstream 2026-08-01"} {
		if _, err := s.Add("Брокер nats слушает 4222, мониторинг на 8222.", []string{"nats"}, origin); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Search(s, "брокер nats", []string{"example.com/me/fork", "example.com/them/upstream"}, "", 10, MinScore)
	if err != nil || len(got) != 2 || got[0].BM25 != got[1].BM25 {
		t.Fatalf("want a tie of two, got %+v %v", got, err)
	}
	if got[0].Origin != "example.com/them/upstream 2026-08-01" {
		t.Errorf("first is %q, want the upstream's fact", got[0].Origin)
	}
}

// The cut comes after the tie order: with room for one fact, the host's fact is the one, wherever the database put it.
func TestSearchCutKeepsTheTieWinner(t *testing.T) {
	s := emptyBase(t)
	for _, sc := range []string{"host:mac", "", ""} {
		if _, err := s.AddWith("Брокер nats слушает 4222.", []string{"nats"}, "x 2026-09-10", "",
			store.Meta{Scope: sc}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Search(s, "nats", nil, "mac", 1, MinScore)
	if err != nil || len(got) != 1 || got[0].Scope != "host:mac" {
		t.Fatalf("got %+v %v, want the host's fact", got, err)
	}
}

// A query of one word finds its fact: the agent wrote it, and the term floors of Recall would silence it.
func TestSearchHasNoGates(t *testing.T) {
	s := setup(t)
	got, err := Search(s, "gradle", nil, "", 10, MinScore)
	if err != nil || len(got) == 0 {
		t.Fatalf("got %+v %v, want the gradle fact", got, err)
	}
}

func TestSearchNoRoomFindsNothing(t *testing.T) {
	s := setup(t)
	if got, err := Search(s, "gradle", nil, "", 0, MinScore); err != nil || got != nil {
		t.Fatalf("got %+v %v", got, err)
	}
}

// n is a maximum (#407): a fact far below the best one does not fit the query and is left out, so a query that one
// fact answers gets that fact alone, while minScore 0 hands over every fact up to n as before.
func TestSearchLeavesOutWhatDoesNotFit(t *testing.T) {
	s := emptyBase(t)
	if _, err := s.Add("Брокер nats на db1 слушает порт 4222, мониторинг nats на 8222, кластер nats из трёх узлов.",
		[]string{"nats", "брокер"}, "x 2026-09-10"); err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{"Порт postgres на db1 — 5432.", "На db1 стоит node_exporter.", "db1 перезагружали в среду."} {
		if _, err := s.Add(c, nil, "x 2026-09-10"); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Search(s, "брокер nats db1", nil, "", 5, MinScore)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Keywords[0] != "nats" {
		t.Fatalf("got %d facts %+v, want the nats fact alone", len(got), got)
	}
	all, err := Search(s, "брокер nats db1", nil, "", 5, 0)
	if err != nil || len(all) != 4 {
		t.Fatalf("minScore 0: got %d facts %v, want all four", len(all), err)
	}
	if few, err := Search(s, "брокер nats db1", nil, "", 2, 0); err != nil || len(few) != 2 {
		t.Fatalf("n 2: got %d facts %v, want two", len(few), err)
	}
}
