// Research-only executable. Production storage and command defaults are unchanged.
package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/juev/memcue/internal/recall"
	"github.com/juev/memcue/internal/store"
	"github.com/juev/memcue/internal/text"
)

// Bound at build time by build_bridge.py, verified before any replay.
var productionIdentity, bridgeSourceSHA256 string

type Case struct {
	ID    string       `json:"id"`
	Query recall.Query `json:"query"`
}
type Fixture struct {
	Facts []map[string]any `json:"facts"`
	Cases []Case           `json:"cases"`
}
type NormalCase struct {
	ID      string       `json:"id"`
	Terms   []string     `json:"terms"`
	PGQuery string       `json:"pg_query"`
	Filter  store.Filter `json:"filter"`
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
func main() {
	if len(os.Args) == 2 && os.Args[1] == "identity" {
		must(json.NewEncoder(os.Stdout).Encode(map[string]string{"production_digest": productionIdentity, "bridge_source_sha256": bridgeSourceSHA256}))
		return
	}
	if len(os.Args) == 5 && os.Args[1] == "corpus" {
		corpus(os.Args[2], os.Args[3], os.Args[4])
		return
	}
	if len(os.Args) == 3 && os.Args[1] == "search-db" {
		s, err := store.OpenReadOnly(os.Args[2])
		must(err)
		defer func() { must(s.Close()) }()
		var q recall.Query
		must(json.NewDecoder(os.Stdin).Decode(&q))
		start := time.Now()
		hits, err := recall.Search(s, q)
		must(err)
		must(json.NewEncoder(os.Stdout).Encode(map[string]any{"hits": hits, "ms": time.Since(start).Seconds() * 1000}))
		return
	}
	if len(os.Args) == 3 && os.Args[1] == "normalize" {
		raw, e := io.ReadAll(os.Stdin)
		must(e)
		if os.Args[2] == "stems" {
			must(json.NewEncoder(os.Stdout).Encode(strings.Join(text.Stems(string(raw)), " ")))
			return
		}
		var q recall.Query
		must(json.Unmarshal(raw, &q))
		terms := text.Terms(q.Text)
		f, e := q.Filter()
		must(e)
		var ts []string
		for _, t := range terms {
			p, prefix := text.Prefix(t)
			if prefix {
				p += ":*"
			}
			ts = append(ts, p)
		}
		sep := " | "
		if q.AllWords {
			sep = " & "
		}
		must(json.NewEncoder(os.Stdout).Encode(NormalCase{Terms: terms, PGQuery: strings.Join(ts, sep), Filter: f}))
		return
	}
	if len(os.Args) != 3 {
		panic("usage: sqlite_bridge fixture.json PRIVATE_OUTPUT_DIR")
	}
	raw, err := os.ReadFile(os.Args[1])
	must(err)
	var input Fixture
	must(json.Unmarshal(raw, &input))
	out := outputPath(os.Args[2])
	// Require a new directory before producing any output.
	must(os.Mkdir(out, 0700))
	normalized := map[string]any{}
	stems := map[string]string{}
	fullStems := map[string]string{}
	for _, f := range input.Facts {
		id := f["id"].(string)
		stems[id] = strings.Join(text.Stems(f["content"].(string)), " ")
		fullStems[id] = strings.Join(text.Stems(f["content"].(string)+"\n"+f["keywords"].(string)+"\n"+f["queries"].(string)), " ")
	}
	var cases []NormalCase
	for _, c := range input.Cases {
		terms := text.Terms(c.Query.Text)
		f, e := c.Query.Filter()
		must(e)
		var ts []string
		for _, t := range terms {
			p, prefix := text.Prefix(t)
			if prefix {
				p += ":*"
			}
			ts = append(ts, p)
		}
		sep := " | "
		if c.Query.AllWords {
			sep = " & "
		}
		cases = append(cases, NormalCase{c.ID, terms, strings.Join(ts, sep), f})
	}
	normalized["stems"], normalized["full_stems"], normalized["cases"] = stems, fullStems, cases
	write(filepath.Join(out, "normalized.json"), normalized)
	var results []map[string]any
	for _, arm := range []string{"sqlite-full", "sqlite-content"} {
		path := filepath.Join(out, arm+".db")
		file, e := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
		must(e)
		must(file.Close())
		s, e := store.Open(path)
		must(e)
		must(s.Close())
		db, e := sql.Open("sqlite", path)
		must(e)
		// Insert the same frozen history, including auxiliary fields, into both arms.
		tx, e := db.Begin()
		must(e)
		for _, f := range input.Facts {
			var vals []any
			cols := []string{"id", "version", "content", "keywords", "origin", "created", "status", "author", "expires", "kind", "scope", "source", "evidence", "review", "prompt_version", "queries", "channel"}
			for _, c := range cols {
				vals = append(vals, f[c])
			}
			_, e = tx.Exec("INSERT INTO facts("+strings.Join(cols, ",")+") VALUES ("+strings.TrimSuffix(strings.Repeat("?,", len(cols)), ",")+")", vals...)
			must(e)
		}
		must(tx.Commit())
		must(db.Close())
		s, e = store.Open(path)
		must(e)
		before, e := s.Rows()
		must(e)
		if arm == "sqlite-content" {
			db, e = sql.Open("sqlite", path)
			must(e)
			tx, e := db.Begin()
			must(e)
			_, e = tx.Exec("INSERT INTO fact_fts(fact_fts) VALUES ('delete-all')")
			must(e)
			rows, e := tx.Query("SELECT rid,id FROM fact_index")
			must(e)
			type row struct {
				rid int
				id  string
			}
			var indices []row
			for rows.Next() {
				var r row
				must(rows.Scan(&r.rid, &r.id))
				indices = append(indices, r)
			}
			must(rows.Err())
			must(rows.Close())
			for _, r := range indices {
				_, e = tx.Exec("UPDATE fact_index SET stems=? WHERE rid=?", stems[r.id], r.rid)
				must(e)
				_, e = tx.Exec("INSERT INTO fact_fts(rowid,stems) VALUES (?,?)", r.rid, stems[r.id])
				must(e)
			}
			must(tx.Commit())
			must(db.Close())
		}
		for rep := 0; rep < 2; rep++ {
			for _, c := range input.Cases {
				start := time.Now()
				hits, e := recall.Search(s, c.Query)
				must(e)
				elapsed := time.Since(start).Seconds() * 1000
				list := []map[string]any{}
				for _, h := range hits {
					list = append(list, map[string]any{"id": h.ID, "version": h.Version, "score": h.BM25})
				}
				results = append(results, map[string]any{"arm": arm, "case": c.ID, "rep": rep, "hits": list, "sql_and_recall_ms": elapsed})
			}
		}
		after, e := s.Rows()
		must(e)
		a, _ := json.Marshal(before)
		b, _ := json.Marshal(after)
		if string(a) != string(b) {
			panic("source history changed")
		}
		write(filepath.Join(out, arm+"-history.json"), before)
		must(s.Close())
	}
	write(filepath.Join(out, "sqlite-results.json"), results)
	fmt.Printf("SQLite controls complete: %d requests\n", len(results))
}

// Only reads the explicitly supplied immutable copy. OpenText is deliberately
// avoided: its reconciliation can export missing cache rows back to text.
func corpus(factsDir, casesFile, output string) {
	var cases []Case
	raw, err := os.ReadFile(casesFile)
	must(err)
	must(json.Unmarshal(raw, &cases))
	var facts []map[string]any
	seen := map[string]bool{}
	err = filepath.WalkDir(factsDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink in frozen input")
		}
		if entry.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".jsonl") {
			return fmt.Errorf("unexpected frozen input file")
		}
		rows, err := store.ReadFactFile(path)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return fmt.Errorf("empty fact file")
		}
		if filepath.Clean(path) != filepath.Clean(store.FactPath(factsDir, rows[0].ID)) || seen[rows[0].ID] {
			return fmt.Errorf("fact identity/path collision")
		}
		seen[rows[0].ID] = true
		for _, f := range rows {
			facts = append(facts, map[string]any{"id": f.ID, "version": f.Version, "content": f.Content,
				"keywords": strings.Join(f.Keywords, ", "), "queries": strings.Join(f.Queries, ", "),
				"origin": f.Origin, "created": f.Created.Format(time.RFC3339Nano), "status": f.Status,
				"author": f.Author, "expires": f.Expires, "kind": f.Kind, "scope": f.Scope,
				"source": f.Source, "evidence": f.Evidence, "review": f.Review,
				"prompt_version": f.PromptVersion, "channel": f.Channel})
		}
		return nil
	})
	must(err)
	sort.Slice(facts, func(i, j int) bool {
		if facts[i]["id"] != facts[j]["id"] {
			return facts[i]["id"].(string) < facts[j]["id"].(string)
		}
		return facts[i]["version"].(int) < facts[j]["version"].(int)
	})
	out := outputPath(output)
	must(os.Mkdir(out, 0700))
	write(filepath.Join(out, "fixture.json"), Fixture{Facts: facts, Cases: cases})
	fmt.Printf("Native frozen history: %d unique versions, %d IDs\n", len(facts), len(seen))
}
func outputPath(path string) string {
	absolute, e := filepath.Abs(path)
	must(e)
	for parent := filepath.Dir(absolute); ; parent = filepath.Dir(parent) {
		info, e := os.Lstat(parent)
		must(e)
		if info.Mode()&os.ModeSymlink != 0 {
			panic("refuse symlink in output path")
		}
		if parent == filepath.Dir(parent) {
			break
		}
	}
	return absolute
}

func write(path string, v any) {
	path = outputPath(path)
	b, e := json.MarshalIndent(v, "", "  ")
	must(e)
	file, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	must(e)
	defer func() { _ = file.Close() }()
	_, e = file.Write(b)
	must(e)
	must(file.Close())
}
