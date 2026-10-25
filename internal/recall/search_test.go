package recall

import (
	"path/filepath"
	"slices"
	"strings"
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
	got, err := Search(s, Query{Text: "nats", Project: []string{"example.com/me/app"}, Host: "mac", N: 10, MinScore: MinScore, In: []string{"all"}})
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
	got, err := Search(s, Query{Text: "брокер nats", Project: []string{"example.com/me/fork", "example.com/them/upstream"}, Host: "", N: 10, MinScore: MinScore, In: []string{"all"}})
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
	got, err := Search(s, Query{Text: "nats", Project: nil, Host: "mac", N: 1, MinScore: MinScore})
	if err != nil || len(got) != 1 || got[0].Scope != "host:mac" {
		t.Fatalf("got %+v %v, want the host's fact", got, err)
	}
}

// A query of one word finds its fact: the agent wrote it, and the term floors of Recall would silence it.
func TestSearchHasNoGates(t *testing.T) {
	s := setup(t)
	got, err := Search(s, Query{Text: "gradle", Project: nil, Host: "", N: 10, MinScore: MinScore})
	if err != nil || len(got) == 0 {
		t.Fatalf("got %+v %v, want the gradle fact", got, err)
	}
}

func TestSearchNoRoomFindsNothing(t *testing.T) {
	s := setup(t)
	if got, err := Search(s, Query{Text: "gradle", Project: nil, Host: "", N: 0, MinScore: MinScore}); err != nil || got != nil {
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
	got, err := Search(s, Query{Text: "брокер nats db1", Project: nil, Host: "", N: 5, MinScore: MinScore})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Keywords[0] != "nats" {
		t.Fatalf("got %d facts %+v, want the nats fact alone", len(got), got)
	}
	all, err := Search(s, Query{Text: "брокер nats db1", Project: nil, Host: "", N: 5, MinScore: 0})
	if err != nil || len(all) != 4 {
		t.Fatalf("minScore 0: got %d facts %v, want all four", len(all), err)
	}
	if few, err := Search(s, Query{Text: "брокер nats db1", Project: nil, Host: "", N: 2, MinScore: 0}); err != nil || len(few) != 2 {
		t.Fatalf("n 2: got %d facts %v, want two", len(few), err)
	}
}

// controlBase holds one fact per place and kind, each holding "nats", for the search controls of #409.
func controlBase(t *testing.T) *store.Store {
	t.Helper()
	s := emptyBase(t)
	for _, f := range []struct{ content, origin, expires, kind, scope string }{
		{"own repo nats", "example.com/me/app 2026-09-10", "", "decision", "repo:example.com/me/app"},
		{"learnt here nats", "example.com/me/app 2026-09-10", "", "pitfall", ""},
		{"other repo nats", "example.com/them/lib 2026-09-10", "", "decision", "repo:example.com/them/lib"},
		{"global rule nats", "_global 2026-09-10", "", "rule", "global"},
		{"this host nats", "example.com/them/lib 2026-09-10", "", "environment", "host:mac"},
		{"other host nats", "_global 2026-09-10", "", "environment", "host:loft"},
		{"service nats", "example.com/them/lib 2026-09-10", "", "procedure", "service:hermes"},
		{"expired nats", "example.com/me/app 2026-09-10", "2026-09-30", "event", "repo:example.com/me/app"},
	} {
		if _, err := s.AddWith(f.content, []string{"nats"}, f.origin, f.expires, store.Meta{Kind: f.kind, Scope: f.scope}); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func found(t *testing.T, s *store.Store, q Query) []string {
	t.Helper()
	q.Text, q.Project, q.Host, q.Today = "nats", []string{"example.com/me/app"}, "mac", "2026-10-25"
	if q.N == 0 {
		q.N = 20
	}
	hits, err := Search(s, q)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, h := range hits {
		out = append(out, strings.TrimSuffix(h.Content, " nats"))
	}
	slices.Sort(out)
	return out
}

func TestSearchControls(t *testing.T) {
	s := controlBase(t)
	for _, c := range []struct {
		name string
		q    Query
		want []string
	}{
		// By default a session in a repository gets that repository's facts, and none that has expired.
		{"default", Query{}, []string{"learnt here", "own repo"}},
		{"project", Query{In: []string{"project"}}, []string{"learnt here", "own repo"}},
		{"global", Query{In: []string{"global"}}, []string{"global rule", "other host"}},
		{"host", Query{In: []string{"host"}}, []string{"this host"}},
		{"another host", Query{In: []string{"host:loft"}}, []string{"other host"}},
		{"another repo", Query{In: []string{"repo:example.com/them/lib"}},
			[]string{"other repo", "service", "this host"}},
		{"service", Query{In: []string{"service:hermes"}}, []string{"service"}},
		{"union", Query{In: []string{"project", "host"}}, []string{"learnt here", "own repo", "this host"}},
		{"all", Query{In: []string{"all"}},
			[]string{"global rule", "learnt here", "other host", "other repo", "own repo", "service", "this host"}},
		{"kind", Query{In: []string{"all"}, Kinds: []string{"decision", "rule"}}, []string{"global rule", "other repo", "own repo"}},
		{"expired kept", Query{In: []string{"project"}, Expired: true}, []string{"expired", "learnt here", "own repo"}},
		{"since tomorrow", Query{In: []string{"all"}, Since: "2999-01-01"}, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := found(t, s, c.q); !slices.Equal(got, c.want) {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// Outside a repository the default is the whole base.
func TestSearchDefaultOutsideARepository(t *testing.T) {
	s := controlBase(t)
	for _, project := range [][]string{nil, {"dir:/home/me"}} {
		hits, err := Search(s, Query{Text: "nats", N: 20, Project: project, Today: "2026-10-25"})
		if err != nil || len(hits) != 7 {
			t.Errorf("project %q: got %d facts %v, want every live fact", project, len(hits), err)
		}
	}
}

// A project-less session has no project's facts, rather than all of them.
func TestSearchProjectWithoutProject(t *testing.T) {
	s := controlBase(t)
	hits, err := Search(s, Query{Text: "nats", N: 20, In: []string{"project"}})
	if err != nil || len(hits) != 0 {
		t.Fatalf("got %d facts %v, want none", len(hits), err)
	}
}

// Since keeps what was learnt from that day on.
func TestSearchSince(t *testing.T) {
	s := controlBase(t)
	if got := found(t, s, Query{In: []string{"all"}, Since: store.Today()}); len(got) != 7 {
		t.Errorf("since today: got %q, want every live fact", got)
	}
}

// The filter runs before the limit: a narrow search finds its fact behind many better ones.
func TestSearchFiltersBeforeTheLimit(t *testing.T) {
	s := emptyBase(t)
	for range 30 {
		if _, err := s.Add("nats nats nats broker", nil, "x 2026-09-10"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.AddWith("a rule about nats among many words of other kinds", nil, "x 2026-09-10", "",
		store.Meta{Kind: "rule"}); err != nil {
		t.Fatal(err)
	}
	hits, err := Search(s, Query{Text: "nats", N: 1, Kinds: []string{"rule"}})
	if err != nil || len(hits) != 1 || hits[0].Kind != "rule" {
		t.Fatalf("got %+v %v, want the rule", hits, err)
	}
}

// All words: a fact must hold each of them.
func TestSearchAllWords(t *testing.T) {
	s := emptyBase(t)
	for _, c := range []string{"nats on db1", "nats elsewhere", "db1 reboots"} {
		if _, err := s.Add(c, nil, "x 2026-09-10"); err != nil {
			t.Fatal(err)
		}
	}
	hits, err := Search(s, Query{Text: "nats db1", N: 5, AllWords: true})
	if err != nil || len(hits) != 1 || hits[0].Content != "nats on db1" {
		t.Fatalf("got %+v %v, want the fact with both words", hits, err)
	}
	any, err := Search(s, Query{Text: "nats db1", N: 5})
	if err != nil || len(any) != 3 {
		t.Fatalf("any word: got %d facts %v, want three", len(any), err)
	}
}

func TestSearchRefusesUnknownControls(t *testing.T) {
	s := emptyBase(t)
	for _, q := range []Query{{In: []string{"everywhere"}}, {Kinds: []string{"wish"}}, {Since: "yesterday"},
		{In: []string{"repo:"}}} {
		q.Text, q.N = "nats", 5
		if _, err := Search(s, q); err == nil {
			t.Errorf("%+v: want an error", q)
		}
	}
}
