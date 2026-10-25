package store

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/juev/memcue/internal/text"
)

// A base indexed word-only, by a release before the number terms or with MEMCUE_NUMBER_TERMS=0, must get the
// numeric terms on the next open with no variable set: the counts match, so nothing but the stamp of the rows notices that
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

// A row an older memcue wrote into a numbered base, on another machine of the same base, carries no number term and
// no stamp. The next open stems that row again, and only that row; a base whose rows are all stamped is left as it is,
// so a repaired base does not pay for the repair on every open (#390).
func TestRowOfAnOlderMemcueIsRestampedAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "facts.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	old, err := s.Add("Старый стенд жил на Go 1.20.", nil, "notes 2026-10-19")
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.Add("Стенд собран под Go 1.27 и только под него.", nil, "notes 2026-10-19")
	if err != nil {
		t.Fatal(err)
	}
	var otherRid int64
	if err := s.db.QueryRow(`SELECT rid FROM fact_index WHERE id = ?`, other.ID).Scan(&otherRid); err != nil {
		t.Fatal(err)
	}
	// The row as an older memcue writes it: word-only stems, and the numbers column it does not know left at 0.
	var rowid int64
	var stems string
	if err := s.db.QueryRow(`SELECT rid, stems FROM fact_index WHERE id = ?`, old.ID).Scan(&rowid, &stems); err != nil {
		t.Fatal(err)
	}
	var kept []string
	for _, term := range strings.Fields(stems) {
		if !text.IsNumberTerm(term) {
			kept = append(kept, term)
		}
	}
	wordOnly := strings.Join(kept, " ")
	for _, q := range []string{
		`INSERT INTO fact_fts(fact_fts, rowid, stems) SELECT 'delete', rid, stems FROM fact_index WHERE rid = ?`,
		`UPDATE fact_index SET stems = ?2, numbers = 0 WHERE rid = ?1`,
		`INSERT INTO fact_fts(rowid, stems) VALUES (?1, ?2)`,
	} {
		if _, err := s.db.Exec(q, rowid, wordOnly); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := numberedRows(t, reopened); got != 2 {
		t.Fatalf("%d rows carry a number term after reopening, want 2", got)
	}
	hits, err := reopened.Search(text.Terms("1.20"), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || !strings.Contains(hits[0].Content, "1.20") {
		t.Fatalf("hits = %+v, want the Go 1.20 fact alone", hits)
	}
	var otherAfter int64
	if err := reopened.db.QueryRow(`SELECT rid FROM fact_index WHERE id = ?`, other.ID).Scan(&otherAfter); err != nil {
		t.Fatal(err)
	}
	if otherAfter != otherRid {
		t.Fatalf("the stamped row moved from rid %d to %d: the whole index was rebuilt", otherRid, otherAfter)
	}
	before := rids(t, reopened)
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}

	again, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = again.Close() })
	if after := rids(t, again); after != before {
		t.Fatalf("index rows %s after another open, want %s: a stamped base was stemmed again", after, before)
	}
}

// rids lists the index rows as id=rid pairs: a row stemmed again gets a new rid.
func rids(t *testing.T, s *Store) string {
	t.Helper()
	rows, err := s.db.Query(`SELECT id, rid, numbers FROM fact_index ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var id string
		var rid, numbers int64
		if err := rows.Scan(&id, &rid, &numbers); err != nil {
			t.Fatal(err)
		}
		if numbers != text.NumberTermsVersion {
			t.Fatalf("row %s stamped %d, want %d", id, numbers, text.NumberTermsVersion)
		}
		out = append(out, fmt.Sprintf("%s=%d", id, rid))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(out, " ")
}

// A lexer change reaches the rows a base already has through the stamp: a row stemmed by the previous version of
// the number terms, here without the term of a version after ":", gets it on the next open (#391).
func TestOlderNumberTermsAreRestemmedOnOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "facts.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	f, err := s.Add("Правила проверять образом victoriametrics/vmalert:v1.106.1 с -dryRun.", nil, "notes 2026-10-25")
	if err != nil {
		t.Fatal(err)
	}
	// The row as version 1 of the number terms left it: no term for the tag, stamped 1.
	var rowid int64
	var stems string
	if err := s.db.QueryRow(`SELECT rid, stems FROM fact_index WHERE id = ?`, f.ID).Scan(&rowid, &stems); err != nil {
		t.Fatal(err)
	}
	old := strings.Join(slices.DeleteFunc(strings.Fields(stems), text.IsNumberTerm), " ")
	for _, q := range []string{
		`INSERT INTO fact_fts(fact_fts, rowid, stems) SELECT 'delete', rid, stems FROM fact_index WHERE rid = ?`,
		`UPDATE fact_index SET stems = ?2, numbers = 1 WHERE rid = ?1`,
		`INSERT INTO fact_fts(rowid, stems) VALUES (?1, ?2)`,
	} {
		if _, err := s.db.Exec(q, rowid, old); err != nil {
			t.Fatal(err)
		}
	}
	if hits, err := s.Search(text.Terms("v1.106.1"), 10); err != nil || len(hits) != 0 {
		t.Fatalf("before reopening Search(v1.106.1) = %+v, %v; want nothing", hits, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	hits, err := reopened.Search(text.Terms("v1.106.1"), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].ID != f.ID {
		t.Fatalf("Search(v1.106.1) = %+v, want the vmalert fact", hits)
	}
	rids(t, reopened) // every row stamped with the current version
}
