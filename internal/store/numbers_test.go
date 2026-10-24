package store

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/juev/memcue/internal/text"
)

// A base indexed before MEMCUE_NUMBER_TERMS existed must get the numeric terms on the next open: the counts
// match, so nothing but numbersMissing notices that a prompt naming a version would find nothing.
func TestNumbersAreIndexedOnNextOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "facts.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{"Стенд собран под Go 1.27 и только под него.", "Старый стенд жил на Go 1.20."} {
		if _, err := s.Add(content, nil, "notes 2026-10-19"); err != nil {
			t.Fatal(err)
		}
	}
	// The index as it looks with the flag off: no numeric terms at all.
	var stems string
	if err := s.db.QueryRow(`SELECT stems FROM fact_index ORDER BY rid LIMIT 1`).Scan(&stems); err != nil {
		t.Fatal(err)
	}
	for _, term := range strings.Fields(stems) {
		if text.IsNumberTerm(term) {
			t.Fatalf("numbers indexed with the flag off: %q", stems)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	t.Setenv("MEMCUE_NUMBER_TERMS", "1")
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	hits, err := reopened.Search(text.Terms("1.27"), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || !strings.Contains(hits[0].Content, "1.27") {
		t.Fatalf("hits = %+v, want the Go 1.27 fact alone", hits)
	}
}

// Without the flag a base keeps its word-only index: reopening adds nothing, so the shipped behaviour cannot
// change just because numbers became indexable somewhere else.
func TestNumbersStayOutWithoutTheFlag(t *testing.T) {
	path := filepath.Join(t.TempDir(), "facts.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add("Стенд собран под Go 1.27 и только под него.", nil, "notes 2026-10-19"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	hits, err := reopened.Search(text.Terms("go 1.27"), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits = %+v, want the fact found by its word", hits)
	}
	var stems string
	if err := reopened.db.QueryRow(`SELECT stems FROM fact_index`).Scan(&stems); err != nil {
		t.Fatal(err)
	}
	for _, term := range strings.Fields(stems) {
		if text.IsNumberTerm(term) {
			t.Fatalf("numbers indexed with the flag off: %q", stems)
		}
	}
}
