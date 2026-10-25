package store

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/juev/memcue/internal/text"
)

// A base indexed word-only, by a release before the number terms or with MEMCUE_NUMBER_TERMS=0, must get the
// numeric terms on the next open with no variable set: the counts match, so nothing but numbersIncomplete notices that
// a prompt naming a version would find nothing.
func TestNumbersAreIndexedOnNextOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "facts.db")
	t.Setenv("MEMCUE_NUMBER_TERMS", "0")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{"Стенд собран под Go 1.27 и только под него.", "Старый стенд жил на Go 1.20."} {
		if _, err := s.Add(content, nil, "notes 2026-10-19"); err != nil {
			t.Fatal(err)
		}
	}
	// The index as an older release left it: no numeric terms at all.
	assertWordOnly(t, s)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	t.Setenv("MEMCUE_NUMBER_TERMS", "")
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	var numbered int
	if err := reopened.db.QueryRow(`SELECT count(*) FROM fact_index WHERE instr(stems, ' nm') > 0`).
		Scan(&numbered); err != nil {
		t.Fatal(err)
	}
	if numbered != 2 {
		t.Fatalf("%d index rows carry a number term after reopening, want 2", numbered)
	}
	hits, err := reopened.Search(text.Terms("1.27"), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || !strings.Contains(hits[0].Content, "1.27") {
		t.Fatalf("hits = %+v, want the Go 1.27 fact alone", hits)
	}
}

// With MEMCUE_NUMBER_TERMS=0 a base keeps its word-only index: reopening rebuilds nothing, so the opt-out holds on
// the same base.
func TestNumbersStayOutWithTheOptOut(t *testing.T) {
	t.Setenv("MEMCUE_NUMBER_TERMS", "0")
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
	assertWordOnly(t, reopened)
}

func assertWordOnly(t *testing.T, s *Store) {
	t.Helper()
	rows, err := s.db.Query(`SELECT stems FROM fact_index`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var stems string
		if err := rows.Scan(&stems); err != nil {
			t.Fatal(err)
		}
		for _, term := range strings.Fields(stems) {
			if text.IsNumberTerm(term) {
				t.Fatalf("numbers indexed with MEMCUE_NUMBER_TERMS=0: %q", stems)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

// A base indexed word-only and then given one numbered row, as a newer release writes its next fact, is still
// repaired on open: the other rows keep no number term, and a prompt naming their version must find them (#383).
// With the opt-out the same base is left as it is.
func TestPartlyNumberedIndexIsRepairedOnOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "facts.db")
	t.Setenv("MEMCUE_NUMBER_TERMS", "0")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	newer, err := s.Add("Стенд собран под Go 1.27 и только под него.", nil, "notes 2026-10-19")
	if err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{"Старый стенд жил на Go 1.20.", "Резолвер проверять через dig @1.1.1.1."} {
		if _, err := s.Add(content, nil, "notes 2026-10-19"); err != nil {
			t.Fatal(err)
		}
	}
	// The one row a newer release wrote, with its number term in both tables.
	var rowid int64
	var stems string
	if err := s.db.QueryRow(`SELECT rowid, stems FROM fact_index WHERE id = ?`, newer.ID).Scan(&rowid, &stems); err != nil {
		t.Fatal(err)
	}
	stems += " nm1x27"
	if _, err := s.db.Exec(`UPDATE fact_index SET stems = ? WHERE rowid = ?`, stems, rowid); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE fact_fts SET stems = ? WHERE rowid = ?`, stems, rowid); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// The opt-out keeps the base as it is: a rebuild would take the term off the newer row.
	off, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := numberedRows(t, off); got != 1 {
		t.Fatalf("%d rows carry a number term with MEMCUE_NUMBER_TERMS=0, want the hand-written 1: rebuilt", got)
	}
	if err := off.Close(); err != nil {
		t.Fatal(err)
	}

	t.Setenv("MEMCUE_NUMBER_TERMS", "")
	ro, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	before, err := ro.IndexStats()
	if err != nil {
		t.Fatal(err)
	}
	if err := ro.Close(); err != nil {
		t.Fatal(err)
	}
	if want := (IndexStats{Live: 3, Rows: 3, Numbered: 1, Unnumbered: 2}); before != want {
		t.Fatalf("before the repair %+v, want %+v", before, want)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	after, err := reopened.IndexStats()
	if err != nil {
		t.Fatal(err)
	}
	if want := (IndexStats{Live: 3, Rows: 3, Numbered: 3}); after != want {
		t.Fatalf("after reopening %+v, want %+v", after, want)
	}
	for query, want := range map[string]string{"1.20": "Go 1.20", "1.1.1.1": "dig @1.1.1.1", "1.27": "Go 1.27"} {
		hits, err := reopened.Search(text.Terms(query), 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) != 1 || !strings.Contains(hits[0].Content, want) {
			t.Errorf("Search(%q) = %+v, want the %s fact alone", query, hits, want)
		}
	}
}

func numberedRows(t *testing.T, s *Store) int {
	t.Helper()
	st, err := s.IndexStats()
	if err != nil {
		t.Fatal(err)
	}
	return st.Numbered
}
