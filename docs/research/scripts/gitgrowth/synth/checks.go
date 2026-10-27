//go:build research

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/juev/memcue/internal/store"
	"github.com/juev/memcue/internal/text"
)

// runRoundTrip checks that the prototype text encoding carries a base without loss: export, decode, import into a new
// file, and compare every stored column, the bytes of a second export, the search results, the index and the card. It
// also counts what `memcue dump`, today's textconv, leaves out.
func runRoundTrip(args []string) error {
	fl := flag.NewFlagSet("roundtrip", flag.ContinueOnError)
	src := fl.String("db", "", "base to start from (a copy is used)")
	dir := fl.String("out", "", "new scratch directory")
	memcue := fl.String("memcue", "", "memcue binary, to compare memcue dump of both bases")
	result := fl.String("result", "", "JSON file for the results")
	if err := fl.Parse(args); err != nil {
		return err
	}
	if *src == "" || *dir == "" || *result == "" {
		return errors.New("roundtrip: -db, -out and -result are required")
	}
	if err := newDir(*dir); err != nil {
		return err
	}
	orig := filepath.Join(*dir, "orig.db")
	if err := copyFile(*src, orig); err != nil {
		return err
	}
	if err := addEdgeCases(orig); err != nil {
		return err
	}
	res := map[string]any{}
	t0 := time.Now()
	rows, err := exportRows(orig)
	if err != nil {
		return err
	}
	data := encodeJSONL(rows)
	res["export_ms"] = time.Since(t0).Milliseconds()
	res["rows"], res["jsonl_bytes"] = len(rows), len(data)
	decoded, err := decodeJSONL(data)
	if err != nil {
		return err
	}
	rebuilt := filepath.Join(*dir, "rebuilt.db")
	t0 = time.Now()
	if err := importRows(decoded, rebuilt); err != nil {
		return err
	}
	res["import_and_index_ms"] = time.Since(t0).Milliseconds()
	again, err := exportRows(rebuilt)
	if err != nil {
		return err
	}
	res["rows_equal"] = reflect.DeepEqual(rows, again)
	res["jsonl_bytes_equal"] = bytes.Equal(data, encodeJSONL(again))

	a, err := store.Open(orig)
	if err != nil {
		return err
	}
	defer func() { _ = a.Close() }()
	b, err := store.Open(rebuilt)
	if err != nil {
		return err
	}
	defer func() { _ = b.Close() }()
	sa, err := a.IndexStats()
	if err != nil {
		return err
	}
	sb, err := b.IndexStats()
	if err != nil {
		return err
	}
	res["index_stats_equal"], res["index_live"] = sa == sb, sa.Live
	g := newGen(99)
	cur, err := a.Current()
	if err != nil {
		return err
	}
	same, queries := 0, 300
	for i := range queries {
		f := cur[(i*7919)%len(cur)]
		ws := strings.Fields(f.Content)
		q := strings.Join([]string{ws[g.r.IntN(len(ws))], ws[g.r.IntN(len(ws))], g.word()}, " ")
		ha, ea := a.Search(text.Stems(q), 80)
		hb, eb := b.Search(text.Stems(q), 80)
		if ea == nil && eb == nil && hitsEqual(ha, hb) {
			same++
		}
	}
	res["search_same_results"] = fmt.Sprintf("%d/%d", same, queries)
	// The facts the session card is built from, not the rendered card.
	ca, _, err := a.CardFacts([]string{"repo:proj-01", "repo:proj-02"}, "")
	if err != nil {
		return err
	}
	cb, _, err := b.CardFacts([]string{"repo:proj-01", "repo:proj-02"}, "")
	if err != nil {
		return err
	}
	res["card_equal"] = reflect.DeepEqual(ca, cb)

	var evidence, author, fraction, retiredThenActive int
	last := map[string]string{}
	for _, r := range rows {
		if r.Evidence != "" {
			evidence++
		}
		if r.Author != "" {
			author++
		}
		if strings.Contains(r.Created, ".") {
			fraction++
		}
		if last[r.ID] == store.Retired && r.Status == store.Active {
			retiredThenActive++
		}
		last[r.ID] = r.Status
	}
	res["rows_with_evidence"], res["rows_with_author"], res["rows_with_subsecond_created"] = evidence, author, fraction
	res["reopened_versions"] = retiredThenActive
	if *memcue != "" {
		da, err := exec.Command(*memcue, "dump", orig).Output()
		if err != nil {
			return fmt.Errorf("memcue dump: %w", err)
		}
		db, err := exec.Command(*memcue, "dump", rebuilt).Output()
		if err != nil {
			return fmt.Errorf("memcue dump: %w", err)
		}
		res["memcue_dump_equal"] = bytes.Equal(da, db)
		res["memcue_dump_bytes"] = len(da)
		res["memcue_dump_has_evidence"] = evidenceInDump(rows, da)
	}
	_, errTrunc := decodeJSONL(data[:len(data)-20])
	conflicted := append([]byte("<<<<<<< ours\n"), data...)
	_, errConflict := decodeJSONL(conflicted)
	res["truncated_refused"], res["conflict_markers_refused"] = errTrunc != nil, errConflict != nil
	out, _ := json.MarshalIndent(res, "", " ")
	return writeResult(*result, append(out, '\n'))
}

func hitsEqual(a, b []store.Hit) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ID != b[i].ID || a[i].Version != b[i].Version || a[i].BM25 != b[i].BM25 {
			return false
		}
	}
	return true
}

func evidenceInDump(rows []Row, dump []byte) string {
	n, found := 0, 0
	for _, r := range rows {
		if len(r.Evidence) < 40 {
			continue
		}
		n++
		if bytes.Contains(dump, []byte(r.Evidence)) {
			found++
		}
	}
	return fmt.Sprintf("%d/%d", found, n)
}

// addEdgeCases writes the states a round trip must keep: a reopened fact, an expiry, text with a newline, a tab, quotes
// and a backslash, and two rows of an older base (a candidate with a prompt version) that today's writes no longer
// produce, inserted directly.
func addEdgeCases(path string) error {
	s, err := store.Open(path)
	if err != nil {
		return err
	}
	f, err := s.AddWith("line one\nline two\twith \"quotes\" and a back\\slash, ёлка", []string{"a, b", "c"},
		"proj-01/edge 2026-10-27", "2026-12-31", store.Meta{Kind: "rule", Scope: "repo:proj-01", Source: "user",
			Evidence: "the user said so, verbatim: <b>&amp;</b>"})
	if err != nil {
		_ = s.Close()
		return err
	}
	if _, err := s.Retire(f.ID); err != nil {
		_ = s.Close()
		return err
	}
	if _, err := s.Restore(f.ID); err != nil {
		_ = s.Close()
		return err
	}
	if err := s.Close(); err != nil {
		return err
	}
	db, err := openRaw(path, false)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return insertRows(db, []Row{{ID: "01JZZZZZZZZZZZZZZZZZZZZZZZ", Version: 1, Content: "legacy candidate", Created: "2026-01-02T03:04:05.000000006Z",
		Status: store.Active, Author: "old", Review: store.Candidate, PromptVersion: 3, Origin: "proj-02 2026-01-02"},
		{ID: "01JZZZZZZZZZZZZZZZZZZZZZZY", Version: 1, Content: "legacy provisional", Created: "2026-01-02T03:04:05Z",
			Status: store.Active, Author: "old", Review: store.Provisional, Expires: "2026-02-01"}})
}

// runImport is the child the interrupt check kills: import a JSONL file into a base.
func runImport(args []string) error {
	fl := flag.NewFlagSet("import", flag.ContinueOnError)
	in := fl.String("in", "", "JSONL file")
	dst := fl.String("db", "", "base to write")
	if err := fl.Parse(args); err != nil {
		return err
	}
	// import replaces the base at -db; it only ever writes into a directory synth created, so a mistyped path cannot
	// replace a live base.
	if _, err := os.Lstat(filepath.Join(filepath.Dir(*dst), scratchMarker)); err != nil {
		return fmt.Errorf("import: %s is not in a directory synth created", *dst)
	}
	data, err := os.ReadFile(*in)
	if err != nil {
		return err
	}
	rows, err := decodeJSONL(data)
	if err != nil {
		return err
	}
	return importRows(rows, *dst)
}

// runInterrupt kills an import at random moments and checks that the target base is always either the previous base
// or the complete new one, and that a repeated import completes.
func runInterrupt(args []string) error {
	fl := flag.NewFlagSet("interrupt", flag.ContinueOnError)
	src := fl.String("db", "", "base whose export is imported")
	dir := fl.String("out", "", "new scratch directory")
	n := fl.Int("n", 30, "kills")
	result := fl.String("result", "", "JSON file for the results")
	if err := fl.Parse(args); err != nil {
		return err
	}
	if err := newDir(*dir); err != nil {
		return err
	}
	rows, err := exportRows(*src)
	if err != nil {
		return err
	}
	jsonl := filepath.Join(*dir, "facts.jsonl")
	if err := os.WriteFile(jsonl, encodeJSONL(rows), 0o600); err != nil {
		return err
	}
	// The previous base is a smaller one: the first half of the facts.
	target := filepath.Join(*dir, "target.db")
	half := rows[:len(rows)/2]
	for len(half) > 0 && half[len(half)-1].ID == rows[len(half)].ID {
		half = half[:len(half)-1]
	}
	if err := importRows(half, target); err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	t0 := time.Now()
	if out, err := exec.Command(self, "import", "-in", jsonl, "-db", filepath.Join(*dir, "timing.db")).CombinedOutput(); err != nil {
		return fmt.Errorf("timing import: %v: %s", err, out)
	}
	full := time.Since(t0)
	r := rand.New(rand.NewPCG(1, 2))
	res := map[string]any{"import_ms": full.Milliseconds(), "kills": *n}
	var old, complete, broken, stale int
	for range *n {
		cmd := exec.Command(self, "import", "-in", jsonl, "-db", target)
		if err := cmd.Start(); err != nil {
			return err
		}
		time.Sleep(time.Duration(r.Int64N(int64(full))))
		_ = cmd.Process.Signal(syscall.SIGKILL)
		_ = cmd.Wait()
		if _, err := os.Stat(target + ".importing"); err == nil {
			stale++
		}
		got, err := exportRows(target)
		switch {
		case err != nil:
			broken++
		case reflect.DeepEqual(got, half):
			old++
		case reflect.DeepEqual(got, rows):
			complete++
			if err := importRows(half, target); err != nil { // back to the previous base for the next kill
				return err
			}
		default:
			broken++
		}
	}
	if out, err := exec.Command(self, "import", "-in", jsonl, "-db", target).CombinedOutput(); err != nil {
		return fmt.Errorf("final import: %v: %s", err, out)
	}
	got, err := exportRows(target)
	if err != nil {
		return err
	}
	res["target_previous_base"], res["target_complete"], res["target_other_or_unreadable"] = old, complete, broken
	res["stale_tmp_left"], res["final_import_complete"] = stale, reflect.DeepEqual(got, rows)
	out, _ := json.MarshalIndent(res, "", " ")
	return writeResult(*result, append(out, '\n'))
}

// runRace measures what a reader that takes no lock — git add, git commit -a, a backup by cp — gets while memcue
// writes: it copies the bytes of the file over and over during a stream of writes and checks each copy, against a
// consistent snapshot taken the same way the prototype export does.
func runRace(args []string) error {
	fl := flag.NewFlagSet("race", flag.ContinueOnError)
	src := fl.String("db", "", "base to start from (a copy is used)")
	dir := fl.String("out", "", "new scratch directory")
	dur := fl.Duration("for", 20*time.Second, "how long to write")
	result := fl.String("result", "", "JSON file for the results")
	if err := fl.Parse(args); err != nil {
		return err
	}
	if err := newDir(*dir); err != nil {
		return err
	}
	base := filepath.Join(*dir, "facts.db")
	if err := copyFile(*src, base); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), *dur)
	defer cancel()
	var (
		wg     sync.WaitGroup
		writes int
		werr   error
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		s, err := store.Open(base)
		if err != nil {
			werr = err
			return
		}
		defer func() { _ = s.Close() }()
		g := newGen(5)
		fs := &facts{version: map[string]int{}}
		cur, err := s.Current()
		if err != nil {
			werr = err
			return
		}
		for _, f := range cur {
			fs.set(f)
		}
		for ctx.Err() == nil {
			if _, err := g.apply(s, fs); err != nil {
				werr = err
				return
			}
			writes++
		}
	}()
	var copies, ok, malformed, otherErr, journalSeen, snapshots, snapshotsOK int
	check := filepath.Join(*dir, "copy.db")
	for ctx.Err() == nil {
		_, jerr := os.Stat(base + "-journal")
		b, err := os.ReadFile(base)
		if err != nil {
			continue
		}
		copies++
		if jerr == nil {
			journalSeen++
		}
		_ = os.Remove(check)
		if err := os.WriteFile(check, b, 0o600); err != nil {
			return err
		}
		switch verdict(check) {
		case "ok":
			ok++
		case "malformed":
			malformed++
		default:
			otherErr++
		}
		snapshots++
		if rows, err := exportRows(base); err == nil && contiguous(rows) {
			snapshotsOK++
		}
	}
	wg.Wait()
	if werr != nil {
		return werr
	}
	res := map[string]any{"seconds": dur.Seconds(), "writes": writes, "raw_copies": copies, "raw_copy_ok": ok,
		"raw_copy_malformed": malformed, "raw_copy_other_error": otherErr, "journal_present_at_copy": journalSeen,
		"snapshot_exports": snapshots, "snapshot_exports_ok": snapshotsOK}
	out, _ := json.MarshalIndent(res, "", " ")
	return writeResult(*result, append(out, '\n'))
}

// verdict opens a copy read-only and runs the integrity check, as the next machine would on the file git gave it.
func verdict(path string) string {
	db, err := openRaw(path, true)
	if err != nil {
		return "error"
	}
	defer func() { _ = db.Close() }()
	var v string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&v); err != nil {
		if strings.Contains(err.Error(), "malformed") || strings.Contains(err.Error(), "not a database") {
			return "malformed"
		}
		return "error"
	}
	if v != "ok" {
		return "malformed"
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM facts`).Scan(&n); err != nil {
		return "error"
	}
	return "ok"
}
