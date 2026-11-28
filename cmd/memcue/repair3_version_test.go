package main

import (
	"bytes"
	"errors"
	"github.com/juev/memcue/internal/store"
	"os"
	"testing"
)

func TestRepair3CLIObservedVersion(t *testing.T) {
	dsn := os.Getenv("MEMCUE_REPAIR_CLI_DSN")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	t.Setenv("MEMCUE_POSTGRES_DSN", dsn)
	s, e := store.OpenPostgres(dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = s.Close() }()
	f, e := s.AddWith("Synthetic CLI version orchard", "fixture", "", store.Meta{})
	if e != nil {
		t.Fatal(e)
	}
	var out bytes.Buffer
	if e = runCorrect([]string{"-expected-version", "1", f.ID, "Synthetic CLI updated orchard"}, &out); e != nil {
		t.Fatalf("observed CLI update: %v", e)
	}
	if e = runCorrect([]string{"-expected-version", "1", f.ID, "Synthetic stale orchard"}, &out); !errors.Is(e, store.ErrChanged) {
		t.Errorf("stale CLI correct: %v", e)
	}
	if e = runRetire([]string{"-expected-version", "1", "-reason", "The fixture no longer applies.", f.ID}, &out); !errors.Is(e, store.ErrChanged) {
		t.Errorf("stale CLI retire: %v", e)
	}
	h, e := s.History(f.ID)
	if e != nil || len(h) != 2 {
		t.Fatalf("stale CLI appended: %d %v", len(h), e)
	}
}
