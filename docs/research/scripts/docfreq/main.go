//go:build research

// Command docfreq times the ways of weighing the prompt terms for #337 on a copy of a base: today's one MATCH per
// term (Store.DocFreq), one statement of UNION ALL counts, and one pass over fact_index.stems in Go with text.Covers.
// Every prompt is a fresh connection, as the hook is a fresh process. It stops if a way's counts differ from
// today's. One JSON line per prompt goes to stdout, the medians by number of terms to stderr. Experiment only.
//
// usage: go run -tags research ./docs/research/scripts/docfreq FACTS.db eval.json > docfreq.jsonl
package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/juev/memcue/internal/text"
)

type row struct {
	ID     string  `json:"id"`
	Terms  int     `json:"terms"`
	Match  float64 `json:"match_ms"` // one MATCH per term, today
	Union  float64 `json:"union_ms"` // one statement
	Scan   float64 `json:"scan_ms"`  // one pass over the stems
	Differ string  `json:"differ,omitempty"`
}

func main() {
	dsn := "file:" + os.Args[1] + "?mode=ro&_pragma=busy_timeout(5000)"
	b, err := os.ReadFile(os.Args[2])
	if err != nil {
		panic(err)
	}
	var in struct {
		Prompts []struct {
			ID   string `json:"id"`
			Text string `json:"text"`
		} `json:"prompts"`
	}
	if err := json.Unmarshal(b, &in); err != nil {
		panic(err)
	}
	enc := json.NewEncoder(os.Stdout)
	var rows []row
	for _, p := range in.Prompts {
		prompt := p.Text
		if r := []rune(prompt); len(r) > 4000 {
			prompt = string(r[:4000])
		}
		terms := text.Terms(prompt)
		if len(terms) == 0 {
			continue
		}
		r := row{ID: p.ID, Terms: len(terms)}
		want, d := timed(dsn, terms, match)
		r.Match = d
		got, d := timed(dsn, terms, union)
		r.Union = d
		if !maps.Equal(want, got) {
			r.Differ = "union"
		}
		got, d = timed(dsn, terms, scan)
		r.Scan = d
		if !maps.Equal(want, got) {
			r.Differ += " scan"
		}
		rows = append(rows, r)
		if err := enc.Encode(r); err != nil {
			panic(err)
		}
	}
	for _, b := range [][2]int{{1, 5}, {6, 10}, {11, 20}, {21, 40}, {41, 80}, {81, 1 << 20}} {
		var m, u, s []float64
		for _, r := range rows {
			if r.Terms >= b[0] && r.Terms <= b[1] {
				m, u, s = append(m, r.Match), append(u, r.Union), append(s, r.Scan)
			}
		}
		if len(m) > 0 {
			fmt.Fprintf(os.Stderr, "terms %d-%d: %d prompts; median ms match %.2f, union %.2f, scan %.2f; p95 %.2f %.2f %.2f\n",
				b[0], b[1], len(m), q(m, .5), q(u, .5), q(s, .5), q(m, .95), q(u, .95), q(s, .95))
		}
	}
	differ := 0
	for _, r := range rows {
		if r.Differ != "" {
			differ++
		}
	}
	fmt.Fprintf(os.Stderr, "prompts %d, counts differ from today's in %d\n", len(rows), differ)
}

// timed opens a fresh connection, as the hook does, and times one way of counting on it.
func timed(dsn string, terms []string, way func(*sql.DB, []string) map[string]int) (map[string]int, float64) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		panic(err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Ping(); err != nil {
		panic(err)
	}
	start := time.Now()
	got := way(db, terms)
	return got, float64(time.Since(start).Microseconds()) / 1000
}

func match(db *sql.DB, terms []string) map[string]int {
	df := map[string]int{}
	for _, t := range terms {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM fact_fts WHERE fact_fts MATCH ?`, text.Query(t)).Scan(&n); err != nil {
			panic(err)
		}
		df[t] = n
	}
	return df
}

func union(db *sql.DB, terms []string) map[string]int {
	parts := make([]string, len(terms))
	args := make([]any, len(terms))
	for i, t := range terms {
		parts[i] = fmt.Sprintf(`SELECT %d, count(*) FROM fact_fts WHERE fact_fts MATCH ?`, i)
		args[i] = text.Query(t)
	}
	rs, err := db.Query(strings.Join(parts, " UNION ALL "), args...)
	if err != nil {
		panic(err)
	}
	defer func() { _ = rs.Close() }()
	df := map[string]int{}
	for rs.Next() {
		var i, n int
		if err := rs.Scan(&i, &n); err != nil {
			panic(err)
		}
		df[terms[i]] = n
	}
	return df
}

func scan(db *sql.DB, terms []string) map[string]int {
	rs, err := db.Query(`SELECT stems FROM fact_index`)
	if err != nil {
		panic(err)
	}
	defer func() { _ = rs.Close() }()
	df := make(map[string]int, len(terms))
	for _, t := range terms {
		df[t] = 0
	}
	for rs.Next() {
		var stems string
		if err := rs.Scan(&stems); err != nil {
			panic(err)
		}
		fields := strings.Fields(stems)
		for _, t := range terms {
			if slices.ContainsFunc(fields, func(st string) bool { return text.Covers(t, st) }) {
				df[t]++
			}
		}
	}
	return df
}

func q(v []float64, p float64) float64 {
	v = slices.Clone(v)
	slices.Sort(v)
	return v[int(float64(len(v)-1)*p)]
}
