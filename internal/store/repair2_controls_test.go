package store

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"os"
	"sync"
	"testing"
)

func TestRepair2RetiredFactsStayOutOfQueries(t *testing.T) {
	dsn := os.Getenv("MEMCUE_REPAIR_DSN")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	s, err := OpenPostgres(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	c, err := pgx.Connect(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close(context.Background()) }()
	if _, err = c.Exec(context.Background(), `TRUNCATE memcue.facts; INSERT INTO memcue.facts(id,version,payload,terms) SELECT 'retired-'||i,3,jsonb_build_object('ID','retired-'||i,'Version',3,'Content','synthetic cedar','Created','2026-01-01T00:00:00Z','Status','retired','Scope','repo:retired'),'{cedar}' FROM generate_series(1,1000) i`); err != nil {
		t.Fatal(err)
	}
	rows, err := s.Rows()
	if err != nil || len(rows) != 1000 || rows[0].Version != 3 {
		t.Fatalf("retired facts are not read whole: %d %v", len(rows), err)
	}
	scopes, err := s.RepoScopes()
	if err != nil || len(scopes) != 0 {
		t.Fatalf("retired fact leaked scope: %v %v", scopes, err)
	}
	hits, err := s.Search([]string{"cedar"}, 5)
	if err != nil || len(hits) != 0 {
		t.Fatalf("retired fact found by search: %d %v", len(hits), err)
	}
}
func TestRepair2ConcurrentExpectedVersion(t *testing.T) {
	dsn := os.Getenv("MEMCUE_REPAIR_DSN")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	s, err := OpenPostgres(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	c, err := pgx.Connect(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close(context.Background()) }()
	if _, err = c.Exec(context.Background(), `TRUNCATE memcue.facts`); err != nil {
		t.Fatal(err)
	}
	f, err := s.AddWith("synthetic concurrent labels", "fixture", "", Meta{Evidence: "retain"})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := s.Relabel(f.ID, 1, "rule", "repo:concurrent", ""); results <- e }()
	}
	wg.Wait()
	close(results)
	accepted, conflicts := 0, 0
	for e := range results {
		if e == nil {
			accepted++
		} else if errors.Is(e, ErrChanged) {
			conflicts++
		} else {
			t.Fatal(e)
		}
	}
	if accepted != 1 || conflicts != 1 {
		t.Fatalf("concurrent CAS: accepted=%d conflicts=%d", accepted, conflicts)
	}
	h, err := s.History(f.ID)
	if err != nil || len(h) != 1 || h[0].Version != 2 || h[0].Evidence != "retain" {
		t.Fatalf("history/metadata lost: %v", err)
	}
}
