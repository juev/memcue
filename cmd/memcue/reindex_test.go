package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

// reindex rebuilds an index broken by hand and reports the counts before and after; -dry-run reports the same
// counts before and leaves the index as it found it (#383).
func TestReindexRebuildsAndDryRunDoesNot(t *testing.T) {
	env(t)
	t.Setenv("MEMCUE_NUMBER_TERMS", "")
	for _, content := range []string{
		"Стенд собран под Go 1.27 и только под него.",
		"Релиз выкатили 2026-10-19 вечером.",
		"Gradle вне make требует JAVA_HOME.",
	} {
		if err := runAdd([]string{"-origin", "notes 2026-10-19", "--", content}, &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
	}
	db, err := sql.Open("sqlite", "file:"+os.Getenv("MEMCUE_FACTS"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, q := range []string{`DELETE FROM fact_index`, `INSERT INTO fact_fts(fact_fts) VALUES ('delete-all')`} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	rows := func() int {
		t.Helper()
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM fact_index`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	var out bytes.Buffer
	if err := runReindex([]string{"-dry-run", "-json"}, &out); err != nil {
		t.Fatal(err)
	}
	var dry map[string]any
	if err := json.Unmarshal(out.Bytes(), &dry); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	want := map[string]any{"dry_run": true, "live": 3.0, "rows_before": 0.0, "numbered_before": 0.0,
		"unnumbered_before": 2.0}
	if fmt.Sprint(dry) != fmt.Sprint(want) {
		t.Errorf("reindex -dry-run -json = %v, want %v", dry, want)
	}
	if n := rows(); n != 0 {
		t.Fatalf("reindex -dry-run left %d index rows, want the broken 0", n)
	}
	out.Reset()
	if err := runReindex([]string{"-dry-run"}, &out); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "live facts: 3\nindex rows: 0\nrows with a number term: 0\n"+
		"live facts with a number but no number term: 2\n"; got != want {
		t.Errorf("reindex -dry-run printed %q, want %q", got, want)
	}

	out.Reset()
	if err := runReindex(nil, &out); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "live facts: 3\nindex rows: 0 -> 3\nrows with a number term: 0 -> 2\n"; got != want {
		t.Errorf("reindex printed %q, want %q", got, want)
	}
	if n := rows(); n != 3 {
		t.Fatalf("%d index rows after reindex, want 3", n)
	}
	out.Reset()
	if err := runReindex([]string{"-json"}, &out); err != nil {
		t.Fatal(err)
	}
	var done map[string]any
	if err := json.Unmarshal(out.Bytes(), &done); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	want = map[string]any{"dry_run": false, "live": 3.0, "rows_before": 3.0, "rows_after": 3.0,
		"numbered_before": 2.0, "numbered_after": 2.0, "unnumbered_before": 0.0, "unnumbered_after": 0.0}
	if fmt.Sprint(done) != fmt.Sprint(want) {
		t.Errorf("reindex -json = %v, want %v", done, want)
	}
	out.Reset()
	if err := runSearch([]string{"2026-10-19"}, &out); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); strings.Count(got, "\n") != 0 || !strings.Contains(got, "Релиз") {
		t.Errorf("search 2026-10-19 after reindex = %q, want the release fact alone", got)
	}
}

// Without -n search hands over five facts, as the plugins do (#384).
func TestSearchHandsOverFiveByDefault(t *testing.T) {
	env(t)
	for i := range 7 {
		content := fmt.Sprintf("Gradle проекта номер %c собирается через make.", 'A'+i)
		if err := runAdd([]string{"-origin", "notes 2026-10-19", "--", content}, &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	if err := runSearch([]string{"-json", "gradle make"}, &out); err != nil {
		t.Fatal(err)
	}
	var hits []json.RawMessage
	if err := unmarshalFacts(out.Bytes(), &hits); err != nil {
		t.Fatal(err)
	}
	if len(hits) != 5 {
		t.Errorf("search without -n returned %d facts, want 5", len(hits))
	}
	out.Reset()
	if err := runSearch([]string{"-json", "-n", "7", "gradle make"}, &out); err != nil {
		t.Fatal(err)
	}
	if err := unmarshalFacts(out.Bytes(), &hits); err != nil {
		t.Fatal(err)
	}
	if len(hits) != 7 {
		t.Errorf("search -n 7 returned %d facts, want 7", len(hits))
	}
}
