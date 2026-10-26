package recall

import (
	"fmt"
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
		// By default a session in a repository gets that repository's facts, this host's and the global ones (#418),
		// and none that has expired.
		{"default", Query{}, []string{"global rule", "learnt here", "other host", "own repo", "this host"}},
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

func TestSearchNarrowByDefault(t *testing.T) {
	repo := []string{"example.com/me/app"}
	for _, c := range []struct {
		q    Query
		want bool
	}{
		{Query{Project: repo}, true},
		{Query{Project: repo, In: []string{"project"}}, false},
		{Query{Project: []string{"dir:/home/me"}}, false},
		{Query{}, false},
	} {
		if got := c.q.NarrowByDefault(); got != c.want {
			t.Errorf("%+v: %v, want %v", c.q, got, c.want)
		}
	}
}

// In a repository the default finds a fact of the repository, one of this host and a global one, and none of another
// repository or host (#418).
func TestSearchDefaultInARepository(t *testing.T) {
	s := emptyBase(t)
	for _, f := range []struct{ content, origin, scope string }{
		{"project fact nats", "example.com/me/app 2026-10-26", "repo:example.com/me/app"},
		{"host fact nats", "example.com/them/lib 2026-10-26", "host:mac"},
		{"global fact nats", "example.com/them/lib 2026-10-26", "global"},
		{"another repo nats", "example.com/them/lib 2026-10-26", "repo:example.com/them/lib"},
		{"another host nats", "example.com/them/lib 2026-10-26", "host:loft"},
	} {
		if _, err := s.AddWith(f.content, []string{"nats"}, f.origin, "", store.Meta{Scope: f.scope}); err != nil {
			t.Fatal(err)
		}
	}
	hits, err := Search(s, Query{Text: "nats", N: 20, Project: []string{"example.com/me/app"}, Host: "mac"})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, h := range hits {
		got = append(got, strings.TrimSuffix(h.Content, " nats"))
	}
	slices.Sort(got)
	if want := []string{"global fact", "host fact", "project fact"}; !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveRepos(t *testing.T) {
	known := []string{"repo:github.com/me/memcue", "repo:example.com/a/app", "repo:example.com/b/app", "repo:Example.com/c/Tool"}
	for _, c := range []struct {
		name    string
		in      []string
		known   []string
		tiers   []string
		want    []string
		wantErr []string // parts the error must hold; nil is no error
	}{
		{"exact key keeps its spelling", []string{"REPO:github.com/me/MEMCUE"}, known, nil,
			[]string{"REPO:github.com/me/MEMCUE"}, nil},
		{"unique suffix", []string{"repo:memcue"}, known, nil, []string{"repo:github.com/me/memcue"}, nil},
		{"suffix of several parts, case aside", []string{"repo:c/tool"}, known, nil, []string{"repo:Example.com/c/Tool"}, nil},
		{"other places untouched", []string{"host", "global", "service:x", "repo:memcue"}, known, nil,
			[]string{"host", "global", "service:x", "repo:github.com/me/memcue"}, nil},
		{"empty known list", []string{"repo:nothing"}, nil, nil, []string{"repo:nothing"}, nil},
		{"no match", []string{"repo:memcu"}, known, nil, nil,
			[]string{`-in repo:memcu: no stored repository key ends with "memcu"; known keys: `, "github.com/me/memcue"}},
		{"two matches", []string{"repo:app"}, known, nil, nil,
			[]string{`-in repo:app: 2 stored repository keys end with "app": example.com/a/app, example.com/b/app`}},
		{"a part of a name is no suffix", []string{"repo:cue"}, known, nil, nil, []string{"no stored repository key"}},
		// #459: -in repo:KEY also matches the origin tier KEY, so a tier is a known key as it is.
		{"an origin tier is kept", []string{"repo:widget-notes"}, known, []string{"widget-notes", "_global"},
			[]string{"repo:widget-notes"}, nil},
		{"the global tier is kept", []string{"repo:_global"}, known, []string{"widget-notes", "_global"},
			[]string{"repo:_global"}, nil},
		{"a tier matches exactly, as the origin filter does", []string{"repo:widget"}, known,
			[]string{"widget-notes"}, nil, []string{"no stored repository key"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			q, err := Query{In: c.in}.ResolveRepos(c.known, c.tiers)
			if c.wantErr != nil {
				if err == nil {
					t.Fatalf("got %q, want an error", q.In)
				}
				for _, part := range c.wantErr {
					if !strings.Contains(err.Error(), part) {
						t.Errorf("error %q lacks %q", err, part)
					}
				}
				return
			}
			if err != nil || !slices.Equal(q.In, c.want) {
				t.Errorf("got %q %v, want %q", q.In, err, c.want)
			}
		})
	}
}

// Past twelve stored keys the error names the first twelve and counts the rest.
func TestResolveReposCountsTheRest(t *testing.T) {
	var known []string
	for i := range 15 {
		known = append(known, fmt.Sprintf("repo:example.com/a/r%02d", i))
	}
	_, err := Query{In: []string{"repo:nope"}}.ResolveRepos(known, nil)
	if err == nil {
		t.Fatal("want an error")
	}
	want := `-in repo:nope: no stored repository key ends with "nope"; known keys: example.com/a/r00, example.com/a/r01, ` +
		`example.com/a/r02, example.com/a/r03, example.com/a/r04, example.com/a/r05, example.com/a/r06, ` +
		`example.com/a/r07, example.com/a/r08, example.com/a/r09, example.com/a/r10, example.com/a/r11, and 3 more`
	if err.Error() != want {
		t.Errorf("got  %q\nwant %q", err, want)
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

// An origin key outside ASCII still names its facts: the database counts characters, not bytes.
func TestSearchProjectKeyOutsideASCII(t *testing.T) {
	s := emptyBase(t)
	if _, err := s.Add("nats на стенде", nil, "dir:/дом/проект 2026-10-25"); err != nil {
		t.Fatal(err)
	}
	hits, err := Search(s, Query{Text: "nats", N: 5, Project: []string{"dir:/дом/проект"}, In: []string{"project"}})
	if err != nil || len(hits) != 1 {
		t.Fatalf("got %d facts %v, want the project's fact", len(hits), err)
	}
}

// A query of more than messageWords words reads as a message: its floor is MessageFloor, not MinScore. An explicit
// -min-score is the caller's choice, and 0 keeps every fact the words fit (#417).
func TestQueryFloor(t *testing.T) {
	long := "Давай посмотрим сколько у нас свободно на диске на nas? Что больше всего занимает и сколько весят бекапы с loffe?"
	short := "навык skill структура действия инструкции"
	for _, c := range []struct {
		name string
		q    Query
		want float64
	}{
		{"a search", Query{Text: short, MinScore: MinScore}, MinScore},
		{"a message", Query{Text: long, MinScore: MinScore}, MessageFloor},
		{"a message at 0.3", Query{Text: long, MinScore: 0.3}, 0.3},
		{"a message wide", Query{Text: long, MinScore: 0}, 0},
		{"no floor asked", Query{Text: long}, 0},
	} {
		if got := c.q.floor(); got != c.want {
			t.Errorf("%s: floor %v, want %v", c.name, got, c.want)
		}
	}
	if !IsMessage(Query{Text: long}) || IsMessage(Query{Text: short}) {
		t.Errorf("IsMessage: %q is a message, %q is not", long, short)
	}
}

// A message holds many common words: a fact that shares one of them with it is about something else, while the fact
// that answers one part of the message shares several. -min-score 0 stays the wide net (#417).
func TestSearchMessageLeavesOutAFactOfOneTerm(t *testing.T) {
	s := emptyBase(t)
	if _, err := s.Add("NAS на хосте loffe держит restic-репозиторий в каталоге /volume1/backup.", nil,
		"example.org/me/app 2026-10-20"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add("Файл /opt/data/memory_store.db занимает 260 КБ.", nil, "example.org/me/app 2026-10-20"); err != nil {
		t.Fatal(err)
	}
	msg := "Давай посмотрим сколько у нас свободно на диске на nas? Что больше всего занимает и сколько весят бекапы с loffe?"
	got, err := Search(s, Query{Text: msg, N: 5, MinScore: MinScore, In: []string{"all"}})
	if err != nil {
		t.Fatal(err)
	}
	var about, other bool
	for _, h := range got {
		switch {
		case strings.Contains(h.Content, "restic"):
			about = true
		case strings.Contains(h.Content, "memory_store"):
			other = true
		}
	}
	if !about || other {
		t.Errorf("message: restic fact %v, one-term fact %v, want the first and not the second: %v", about, other, hits(got))
	}
	wide, err := Search(s, Query{Text: msg, N: 5, MinScore: 0, In: []string{"all"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(wide) != 2 {
		t.Errorf("wide net: %d facts, want both: %v", len(wide), hits(wide))
	}
}

func hits(h []store.Hit) []string {
	out := make([]string, len(h))
	for i, x := range h {
		out[i] = x.Content
	}
	return out
}

// A shelf is read only when it is named (#433): every other place, "all" included, leaves the shelves out.
func TestQueryFilterShelf(t *testing.T) {
	shelves := []string{ShelfPrefix}
	for _, c := range []struct {
		name     string
		in       []string
		scopes   []string
		prefixes []string
		exclude  []string
	}{
		{"all", []string{"all"}, nil, nil, shelves},
		{"default outside a repository", nil, nil, nil, shelves},
		{"project", []string{"project"}, []string{"repo:example.com/me/app"}, nil, shelves},
		{"host", []string{"host"}, []string{"host:mac"}, nil, shelves},
		{"every shelf", []string{"shelf"}, nil, shelves, nil},
		{"one shelf", []string{"shelf:research"}, []string{"shelf:research"}, nil, nil},
		{"a shelf and the host", []string{"host", "shelf:research"}, []string{"host:mac", "shelf:research"}, nil, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			q := Query{In: c.in, Host: "mac"}
			if c.name != "default outside a repository" {
				q.Project = []string{"example.com/me/app"}
			}
			f, err := q.Filter()
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(f.Scopes, c.scopes) || !slices.Equal(f.ScopePrefixes, c.prefixes) ||
				!slices.Equal(f.ExcludePrefixes, c.exclude) {
				t.Errorf("scopes %q prefixes %q exclude %q, want %q %q %q", f.Scopes, f.ScopePrefixes,
					f.ExcludePrefixes, c.scopes, c.prefixes, c.exclude)
			}
		})
	}
}

func TestSearchShelf(t *testing.T) {
	s := controlBase(t)
	// Learnt in the session's project, so only the exclusion keeps it out of a project search.
	for _, f := range []struct{ content, scope string }{{"research nats", "shelf:research"}, {"rust nats", "shelf:rust"}} {
		if _, err := s.AddWith(f.content, []string{"nats"}, "example.com/me/app 2026-09-10", "",
			store.Meta{Kind: "environment", Scope: f.scope}); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		name string
		in   []string
		want []string
	}{
		{"default", nil, []string{"global rule", "learnt here", "other host", "own repo", "this host"}},
		{"project", []string{"project"}, []string{"learnt here", "own repo"}},
		{"all", []string{"all"},
			[]string{"global rule", "learnt here", "other host", "other repo", "own repo", "service", "this host"}},
		{"every shelf", []string{"shelf"}, []string{"research", "rust"}},
		{"one shelf", []string{"shelf:research"}, []string{"research"}},
		{"one shelf, any case", []string{"Shelf:Research"}, []string{"research"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := found(t, s, Query{In: c.in}); !slices.Equal(got, c.want) {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// Only a place that names a repository needs the stored keys.
func TestNamesRepo(t *testing.T) {
	for _, c := range []struct {
		in   []string
		want bool
	}{
		{nil, false},
		{[]string{"all"}, false},
		{[]string{"project", "host"}, false},
		{[]string{"repo:"}, false},
		{[]string{"global", " REPO:memcue"}, true},
	} {
		if got := (Query{In: c.in}).NamesRepo(); got != c.want {
			t.Errorf("NamesRepo(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
