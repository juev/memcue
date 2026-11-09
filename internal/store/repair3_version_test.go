package store

import (
	"errors"
	"os"
	"sync"
	"testing"
)

func TestRepair3StaleCallerVersion(t *testing.T) {
	dsn := os.Getenv("MEMCUE_REPAIR_DSN")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	a, err := OpenPostgres(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	b, err := OpenPostgres(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = b.Close() }()
	f, err := a.AddWith("Synthetic expected version orchard", "fixture", "", Meta{Evidence: "retain evidence"})
	if err != nil {
		t.Fatal(err)
	}
	observed, err := b.Get(f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Correct(f.ID, "Synthetic updated orchard", f.Version); err != nil {
		t.Fatal(err)
	}
	if _, err = b.Correct(f.ID, "Synthetic stale correction", observed.Version); !errors.Is(err, ErrChanged) {
		t.Errorf("stale correction must conflict: %v", err)
	}
	if _, err = b.Retire(f.ID, observed.Version); !errors.Is(err, ErrChanged) {
		t.Errorf("stale retirement must conflict: %v", err)
	}
	h, err := a.History(f.ID)
	if err != nil || len(h) != 1 || h[0].Version != 2 || h[0].Content != "Synthetic updated orchard" {
		t.Fatalf("stale operations changed the fact: %+v %v", h, err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	wg.Add(2)
	go func() { defer wg.Done(); _, e := a.Correct(f.ID, "Synthetic concurrent correction", 2); results <- e }()
	go func() { defer wg.Done(); _, e := b.Retire(f.ID, 2); results <- e }()
	wg.Wait()
	close(results)
	ok, conflicts := 0, 0
	for e := range results {
		if e == nil {
			ok++
		} else if errors.Is(e, ErrChanged) {
			conflicts++
		} else {
			t.Fatal(e)
		}
	}
	if ok != 1 || conflicts != 1 {
		t.Fatalf("concurrent correct/retire: accepted=%d conflicts=%d", ok, conflicts)
	}
}
