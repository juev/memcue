package store

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// emptyPostgres creates a database of its own on the isolated fixture, so a copy finds no memcue schema, and drops it
// after the test.
func emptyPostgres(t *testing.T) string {
	t.Helper()
	admin := os.Getenv("MEMCUE_TEST_POSTGRES_DSN")
	if admin == "" {
		t.Skip("requires an isolated PostgreSQL fixture whose user may create databases")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close(ctx) }()
	name := fmt.Sprintf("memcue_copy_%d", time.Now().UnixNano())
	if _, err := c.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Skipf("fixture user cannot create databases: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		c, err := pgx.Connect(ctx, admin)
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = c.Close(ctx) }()
		if _, err := c.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
	})
	u, err := url.Parse(admin)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	return u.String()
}

// localHistory is a local base with every kind of history a copy must keep: corrections, a retirement, an expiry,
// metadata and a second fact written later.
func localHistory(t *testing.T) []Fact {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "facts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	a, err := s.AddWith("Synthetic cedar CODE-1 stays on port 55432", "fixture 2026-11-09", "", Meta{Kind: "environment", Scope: "repo:fixture", Source: "tool", Evidence: "synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Correct(a.ID, "Synthetic cedar CODE-1 moved to port 55433", a.Version); err != nil {
		t.Fatal(err)
	}
	b, err := s.AddWith("Синтетическое правило: писать тесты первыми", "fixture", "2099-01-01", Meta{Kind: "rule", Scope: "global", Source: "user"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Retire(b.ID, b.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddWith("Synthetic third fact on the research shelf", "fixture", "", Meta{Kind: "event", Scope: "shelf:research"}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.Rows()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 5 {
		t.Fatalf("fixture history has %d versions, want 5", len(rows))
	}
	return rows
}

// The copy keeps every version of every fact: PostgreSQL then reads the same history, current facts and search.
func TestCopyToPostgresKeepsTheWholeHistory(t *testing.T) {
	dsn := emptyPostgres(t)
	rows := localHistory(t)
	ctx := context.Background()
	before, err := InspectPostgres(ctx, dsn)
	if err != nil || before.Schema || before.Rows != 0 {
		t.Fatalf("empty database inspected as %+v, %v", before, err)
	}
	if err := CopyToPostgres(ctx, dsn, rows); err != nil {
		t.Fatal(err)
	}
	s, err := OpenPostgres(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	got, err := s.Rows()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(rows) {
		t.Fatalf("PostgreSQL holds %d versions, want %d", len(got), len(rows))
	}
	for i := range rows {
		if !got[i].Created.Equal(rows[i].Created) {
			t.Errorf("version %d created %v, want %v", i, got[i].Created, rows[i].Created)
		}
		got[i].Created, rows[i].Created = time.Time{}, time.Time{}
		if got[i] != rows[i] {
			t.Errorf("version %d\n got %#v\nwant %#v", i, got[i], rows[i])
		}
	}
	hits, err := s.Search([]string{"cedar"}, 5)
	if err != nil || len(hits) != 1 || !strings.Contains(hits[0].Content, "55433") {
		t.Fatalf("search after the copy: %+v %v", hits, err)
	}
	f, err := s.AddWith("Synthetic fact written after the copy", "fixture", "", Meta{Kind: "event"})
	if err != nil {
		t.Fatalf("PostgreSQL does not take writes after the copy: %v", err)
	}
	if f.Version != 1 {
		t.Fatalf("new fact version %d", f.Version)
	}
}

// A base that already holds facts is never written: the copy refuses before it adds a single version.
func TestCopyToPostgresRefusesABaseInUse(t *testing.T) {
	dsn := emptyPostgres(t)
	s, err := OpenPostgres(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddWith("Synthetic fact already in the shared base", "fixture", "", Meta{}); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	ctx := context.Background()
	if err := CopyToPostgres(ctx, dsn, localHistory(t)); !errors.Is(err, ErrTargetNotEmpty) {
		t.Fatalf("err %v, want ErrTargetNotEmpty", err)
	}
	state, err := InspectPostgres(ctx, dsn)
	if err != nil || !state.Schema || state.Rows != 1 {
		t.Fatalf("base after the refusal: %+v %v", state, err)
	}
}

// Inspecting creates nothing: the report of a dry run leaves an empty database without a schema.
func TestInspectPostgresWritesNothing(t *testing.T) {
	dsn := emptyPostgres(t)
	ctx := context.Background()
	for range 2 {
		state, err := InspectPostgres(ctx, dsn)
		if err != nil || state.Schema {
			t.Fatalf("inspect: %+v %v", state, err)
		}
	}
}

// A history the PostgreSQL client could not read back is refused before any connection.
func TestCopyToPostgresRefusesAHistoryOverTheLimit(t *testing.T) {
	rows := make([]Fact, 100001)
	for i := range rows {
		rows[i] = Fact{ID: fmt.Sprintf("F%06d", i), Version: 1}
	}
	err := CopyToPostgres(context.Background(), "postgres://u@127.0.0.1:1/db?sslmode=disable", rows)
	if err == nil || !strings.Contains(err.Error(), "100000") {
		t.Fatalf("err %v, want the history limit", err)
	}
}
