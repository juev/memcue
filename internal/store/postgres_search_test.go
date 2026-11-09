package store

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"
)

// searchPair is the same facts in a local SQLite base and in PostgreSQL, written through the copy.
func searchPair(t *testing.T) (local, remote *Store) {
	t.Helper()
	dsn := emptyPostgres(t)
	local, err := Open(filepath.Join(t.TempDir(), "facts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = local.Close() })
	for _, in := range []FactInput{
		{Content: "PostgreSQL cedar backend keeps one row per fact; cedar search runs on the server", Origin: "memcue 2026-11-09", Meta: Meta{Kind: "decision", Scope: "repo:github.com/juev/memcue"}},
		{Content: "Cedar pitfall: the cedar helper must settle before login, cedar cedar", Origin: "memcue 2026-11-08", Meta: Meta{Kind: "pitfall", Scope: "repo:github.com/juev/memcue"}},
		{Content: "Перезагрузка сервера loffe сбрасывает tailnet; перезагрузить helper вручную", Origin: "loffe 2026-11-01", Meta: Meta{Kind: "pitfall", Scope: "host:loffe"}},
		{Content: "Research shelf: PostgreSQL GIN indexes tsvector lexemes, cedar mentioned once", Origin: "research 2026-11-02", Meta: Meta{Kind: "environment", Scope: "shelf:research"}},
		{Content: "Global rule: answer in Russian, cedar or not", Origin: "global 2026-10-01", Meta: Meta{Kind: "rule", Scope: "Global"}},
		{Content: "Ticket PROJ-1234 pins pgx v5.9.2 on port 55432", Origin: "memcue 2026-11-05", Meta: Meta{Kind: "event"}},
		{Content: "Expired cedar note about an old release", Origin: "memcue 2026-01-01", Expires: "2026-02-01", Meta: Meta{Kind: "event", Scope: "repo:github.com/juev/memcue"}},
	} {
		if _, err := local.AddBatch([]FactInput{in}); err != nil {
			t.Fatal(err)
		}
	}
	retired, err := local.AddWith("Retired cedar fact never found", "memcue 2026-11-03", "", Meta{Scope: "repo:github.com/juev/memcue"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := local.Retire(retired.ID, retired.Version); err != nil {
		t.Fatal(err)
	}
	rows, err := local.Rows()
	if err != nil {
		t.Fatal(err)
	}
	if err := CopyToPostgres(context.Background(), dsn, rows); err != nil {
		t.Fatal(err)
	}
	remote, err = OpenPostgres(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = remote.Close() })
	return local, remote
}

// The server ranks as the local base does: the same facts, in the same order, with the same BM25 and stems, for
// plain, prefix (Russian), ticket and number terms and for every filter of Search.
func TestPostgresSearchRanksLikeTheLocalBase(t *testing.T) {
	local, remote := searchPair(t)
	cases := []struct {
		name  string
		terms []string
		f     Filter
	}{
		{"one term", []string{"cedar"}, Filter{}},
		{"any term", []string{"cedar", "postgresql", "helper"}, Filter{}},
		{"all terms", []string{"cedar", "postgresql"}, Filter{AllTerms: true}},
		{"russian prefix", []string{"перезагруз"}, Filter{}},
		{"ticket and number", []string{"tkproj1234", "nm55432"}, Filter{}},
		{"kinds", []string{"cedar"}, Filter{Kinds: []string{"pitfall", "rule"}}},
		{"scope folds ASCII case", []string{"cedar"}, Filter{Scopes: []string{"global"}}},
		{"scope prefix", []string{"cedar", "перезагруз"}, Filter{ScopePrefixes: []string{"REPO:"}}},
		{"origin fallback", []string{"cedar", "pgx"}, Filter{Origins: []string{"memcue"}, OriginOnlyUnscoped: true}},
		{"shelf excluded", []string{"cedar", "postgresql"}, Filter{ExcludePrefixes: []string{"shelf:"}}},
		{"named shelf kept", []string{"cedar", "postgresql"}, Filter{ExcludePrefixes: []string{"shelf:"}, KeepScopes: []string{"shelf:research"}}},
		{"expired left out", []string{"cedar"}, Filter{Today: "2026-11-09"}},
		{"since", []string{"cedar"}, Filter{Since: "2000-01-01T00:00:00"}},
		{"unknown term", []string{"absentword"}, Filter{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			want, err := local.SearchFiltered(c.terms, 10, c.f)
			if err != nil {
				t.Fatal(err)
			}
			got, err := remote.SearchFiltered(c.terms, 10, c.f)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(want) {
				t.Fatalf("got %d hits, want %d", len(got), len(want))
			}
			for i := range want {
				if got[i].ID != want[i].ID {
					t.Errorf("hit %d: got %s, want %s", i, got[i].Content, want[i].Content)
				}
				if math.Abs(got[i].BM25-want[i].BM25) > 1e-9*math.Max(1, math.Abs(want[i].BM25)) {
					t.Errorf("hit %d BM25: got %v, want %v", i, got[i].BM25, want[i].BM25)
				}
				if !slices.Equal(got[i].Stems, want[i].Stems) {
					t.Errorf("hit %d stems: got %v, want %v", i, got[i].Stems, want[i].Stems)
				}
				if got[i].Content != want[i].Content || got[i].Version != want[i].Version {
					t.Errorf("hit %d fact differs", i)
				}
			}
		})
	}
	terms := []string{"cedar", "перезагруз", "tkproj1234", "absentword"}
	wantDF, wantN, err := local.DocFreq(terms)
	if err != nil {
		t.Fatal(err)
	}
	gotDF, gotN, err := remote.DocFreq(terms)
	if err != nil {
		t.Fatal(err)
	}
	if gotN != wantN || len(gotDF) != len(wantDF) {
		t.Fatalf("DocFreq: got %v %d, want %v %d", gotDF, gotN, wantDF, wantN)
	}
	for term, n := range wantDF {
		if gotDF[term] != n {
			t.Errorf("DocFreq %s: got %d, want %d", term, gotDF[term], n)
		}
	}
}

// Scope discovery, origin tiers, named counts and the repository pitfalls are answered on the server as the local
// base answers them.
func TestPostgresQueriesMatchTheLocalBase(t *testing.T) {
	local, remote := searchPair(t)
	for name, read := range map[string]func(*Store) ([]string, error){
		"RepoScopes":  (*Store).RepoScopes,
		"OriginTiers": (*Store).OriginTiers,
	} {
		want, err := read(local)
		if err != nil {
			t.Fatal(err)
		}
		got, err := read(remote)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s: got %v, want %v", name, got, want)
		}
	}
	for _, name := range []string{"memcue", "loffe", "github.com/juev/memcue", "absent"} {
		want, err := local.CountNamed(name)
		if err != nil {
			t.Fatal(err)
		}
		got, err := remote.CountNamed(name)
		if err != nil || got != want {
			t.Errorf("CountNamed(%s): got %d %v, want %d", name, got, err, want)
		}
	}
	scopes := []string{"repo:github.com/juev/memcue", "host:loffe"}
	want, err := local.ActiveIn("pitfall", scopes)
	if err != nil {
		t.Fatal(err)
	}
	got, err := remote.ActiveIn("pitfall", scopes)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || len(got) != len(want) {
		t.Fatalf("ActiveIn: got %d, want %d (2)", len(got), len(want))
	}
	for i := range want {
		if got[i].ID != want[i].ID || got[i].Content != want[i].Content {
			t.Errorf("ActiveIn %d: got %s, want %s", i, got[i].Content, want[i].Content)
		}
	}
	current, err := remote.Current()
	if err != nil || len(current) != 7 {
		t.Fatalf("Current: %d %v", len(current), err)
	}
}

// A correction and a retirement take effect in the server's search at once, and a restore brings the fact back.
func TestPostgresSearchFollowsWrites(t *testing.T) {
	_, remote := searchPair(t)
	f, err := remote.AddWith("Fresh sequoia fact", "memcue 2026-11-09", "", Meta{Kind: "event"})
	if err != nil {
		t.Fatal(err)
	}
	find := func(term string) int {
		t.Helper()
		hits, err := remote.Search([]string{term}, 5)
		if err != nil {
			t.Fatal(err)
		}
		return len(hits)
	}
	if find("sequoia") != 1 {
		t.Fatal("a new fact is not searchable at once")
	}
	f, err = remote.Correct(f.ID, "Fresh redwood fact", f.Version)
	if err != nil {
		t.Fatal(err)
	}
	if find("sequoia") != 0 || find("redwood") != 1 {
		t.Fatal("a correction did not replace the search terms")
	}
	f, err = remote.Retire(f.ID, f.Version)
	if err != nil {
		t.Fatal(err)
	}
	if find("redwood") != 0 {
		t.Fatal("a retired fact is still found")
	}
	if _, err = remote.Restore(f.ID); err != nil {
		t.Fatal(err)
	}
	if find("redwood") != 1 {
		t.Fatal("a restored fact is not found again")
	}
	if _, err = remote.Correct(f.ID, "stale", f.Version); !errors.Is(err, ErrChanged) {
		t.Fatalf("stale correction: %v", err)
	}
}

// A base of schema 1 is refused with the way out, not read as an empty or broken one.
func TestPostgresRefusesSchema1(t *testing.T) {
	dsn := emptyPostgres(t)
	ctx := context.Background()
	c, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close(ctx) }()
	if _, err := c.Exec(ctx, `CREATE SCHEMA memcue;
 CREATE TABLE memcue.version (singleton boolean PRIMARY KEY CHECK(singleton), version integer NOT NULL);
 INSERT INTO memcue.version VALUES(true,1);
 CREATE TABLE memcue.facts (id text NOT NULL, version integer NOT NULL CHECK(version>0), payload jsonb NOT NULL, PRIMARY KEY(id,version));`); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenPostgres(dsn); !errors.Is(err, ErrPostgresSchema1) {
		t.Fatalf("schema 1: %v", err)
	}
	state, err := InspectPostgres(ctx, dsn)
	if err != nil || state.Version != 1 {
		t.Fatalf("inspect schema 1: %+v %v", state, err)
	}
}

// Borrowed handles share the owner's connection: closing one leaves the owner working, closing the owner ends both.
func TestPostgresBorrowSharesOneConnection(t *testing.T) {
	_, remote := searchPair(t)
	borrowed := remote.Borrow()
	var pid uint32
	read := func(s *Store) error {
		_, err := remoteRead(s, func(ctx context.Context, c *pgx.Conn) (struct{}, error) {
			if pid == 0 {
				pid = c.PgConn().PID()
			} else if c.PgConn().PID() != pid {
				t.Errorf("borrowed handle opened another connection: %d, owner %d", c.PgConn().PID(), pid)
			}
			return struct{}{}, nil
		})
		return err
	}
	if err := read(remote); err != nil {
		t.Fatal(err)
	}
	if err := read(borrowed); err != nil {
		t.Fatal(err)
	}
	if err := borrowed.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := remote.Current(); err != nil {
		t.Fatalf("closing a borrowed handle closed the owner: %v", err)
	}
	if err := remote.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := borrowed.Current(); err == nil {
		t.Fatal("a borrowed handle outlived its owner")
	}
}
