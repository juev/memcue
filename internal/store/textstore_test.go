package store

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func openTextAt(t *testing.T, dir, cache, author string, at time.Time) *Store {
	t.Helper()
	s, err := OpenText(dir, cache)
	if err != nil {
		t.Fatal(err)
	}
	s.author = author
	s.now = func() time.Time { return at }
	return s
}

// textMatches fails unless the file of every fact holds exactly the history the cache holds.
func textMatches(t *testing.T, s *Store) {
	t.Helper()
	rows, err := s.Rows()
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string][]Fact{}
	for _, f := range rows {
		byID[f.ID] = append(byID[f.ID], f)
	}
	for id, h := range byID {
		file, err := ReadFactFile(FactPath(s.Text(), id))
		if err != nil {
			t.Fatal(err)
		}
		if !sameHistory(file, h) {
			t.Errorf("%s: file %+v, cache %+v", id, file, h)
		}
	}
	files, _ := filepath.Glob(filepath.Join(s.Text(), "*", "*.jsonl"))
	if len(files) != len(byID) {
		t.Errorf("%d fact files for %d facts", len(files), len(byID))
	}
}

// Every write reaches the file of the fact it touched (#530).
func TestTextHistoryFollowsEveryWrite(t *testing.T) {
	dir, cache := t.TempDir(), filepath.Join(t.TempDir(), "facts.db")
	s := openTextAt(t, dir, cache, "mac", t0)
	defer func() { _ = s.Close() }()
	f, err := s.AddWith("Порт db1 — 5432", "x 2026-10-19", "", Meta{Kind: "environment"})
	if err != nil {
		t.Fatal(err)
	}
	g, err := s.AddWith("Порт db2 — 6543; бэкап в 3:00", "x 2026-10-19", "", Meta{})
	if err != nil {
		t.Fatal(err)
	}
	steps := []func() error{
		func() error { _, err := s.Correct(f.ID, "Порт db1 — 5433"); return err },
		func() error { _, err := s.Retag(f.ID, 2, "corrected origin", "host:mac"); return err },
		func() error { _, err := s.Relabel(f.ID, 3, "pitfall", "global", ""); return err },
		func() error { _, err := s.Retag(f.ID, 4, "y 2026-10-20", "repo:y"); return err },
		func() error { _, err := s.Retire(f.ID); return err },
		func() error { _, err := s.Restore(f.ID); return err },
		func() error {
			_, err := s.SplitFact(g.ID, 1, []string{"Порт db2 — 6543", "Бэкап db2 в 3:00"})
			return err
		},
	}
	for i, step := range steps {
		if err := step(); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		textMatches(t, s)
	}
}

// Versions git writes into the files reach the cache at the next open: a changed fact, a new fact and a fact whose
// file a merge renumbered.
func TestOpenTakesInWhatGitWrote(t *testing.T) {
	dir, cache := t.TempDir(), filepath.Join(t.TempDir(), "facts.db")
	s := openTextAt(t, dir, cache, "mac", t0)
	f, err := s.AddWith("Hermes работает на модели deepseek-flash", "x", "", Meta{})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	// What a merge on the other machine would leave in the working tree: the file written anew.
	h, _ := ReadFactFile(FactPath(dir, f.ID))
	v2 := h[0]
	v2.Version, v2.Content, v2.Author, v2.Created = 2, "Hermes работает на deepseek-pro", "server", t0.Add(time.Hour)
	if err := WriteFactFile(FactPath(dir, f.ID), append(h, v2)); err != nil {
		t.Fatal(err)
	}
	n := Fact{ID: "01JTEXTFROMSERVER000000000", Version: 1, Content: "Updater держит два rollback-образа",
		Created: t0.Add(2 * time.Hour), Status: Active, Author: "server"}
	if err := WriteFactFile(FactPath(dir, n.ID), []Fact{n}); err != nil {
		t.Fatal(err)
	}

	s = openTextAt(t, dir, cache, "mac", t0)
	defer func() { _ = s.Close() }()
	if got, err := s.Get(f.ID); err != nil || got.Content != v2.Content || got.Version != 2 {
		t.Errorf("changed fact %+v %v", got, err)
	}
	if len(ids(t, s, "deepseek pro")) != 1 || len(ids(t, s, "rollback образа updater")) != 1 {
		t.Error("what git wrote is not searchable")
	}
	textMatches(t, s)
}

// Two adapters may keep two caches of one text history. A correction written through one is appended to an existing
// file, which does not change the directory time an open checks, so the other cache never read it (#709). The
// append now moves that time, and the other cache's next open finds the correction for Get and for search.
func TestAnotherCacheReadsAnAppendedCorrection(t *testing.T) {
	dir := t.TempDir()
	cacheA, cacheB := filepath.Join(t.TempDir(), "a.db"), filepath.Join(t.TempDir(), "b.db")
	a := openTextAt(t, dir, cacheA, "codex", t0)
	f, err := a.AddWith("Orchard deploy needs the copper gate", "x", "", Meta{Kind: "pitfall"})
	if err != nil {
		t.Fatal(err)
	}
	b := openTextAt(t, dir, cacheB, "claude", t0)
	if got, err := b.Get(f.ID); err != nil || got.Version != 1 {
		t.Fatalf("the second cache did not take in the new fact: %+v %v", got, err)
	}
	_ = b.Close()
	if _, err := a.Correct(f.ID, "Orchard deploy needs the amber gate"); err != nil {
		t.Fatal(err)
	}
	_ = a.Close()

	b = openTextAt(t, dir, cacheB, "claude", t0)
	defer func() { _ = b.Close() }()
	if got, err := b.Get(f.ID); err != nil || got.Version != 2 || got.Content != "Orchard deploy needs the amber gate" {
		t.Errorf("the second cache missed the appended correction: %+v %v", got, err)
	}
	if len(ids(t, b, "amber gate")) != 1 {
		t.Error("the appended correction is not searchable through the second cache")
	}
}

// A version the cache committed while its file missed it (the process stopped between the two writes) reaches the
// file at the next open.
func TestOpenWritesWhatTheFileMissed(t *testing.T) {
	dir, cache := t.TempDir(), filepath.Join(t.TempDir(), "facts.db")
	s := openTextAt(t, dir, cache, "mac", t0)
	f, err := s.AddWith("Порт db1 — 5432", "x", "", Meta{})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	plain := openAt(t, cache, "mac", t0.Add(time.Hour)) // writes the cache only, as a stopped process left it
	if _, err := plain.Correct(f.ID, "Порт db1 — 5433"); err != nil {
		t.Fatal(err)
	}
	if _, err := plain.AddWith("Новый факт без файла", "x", "", Meta{}); err != nil {
		t.Fatal(err)
	}
	_ = plain.Close()

	s = openTextAt(t, dir, cache, "mac", t0)
	defer func() { _ = s.Close() }()
	textMatches(t, s)
	if h, _ := ReadFactFile(FactPath(dir, f.ID)); len(h) != 2 || h[1].Content != "Порт db1 — 5433" {
		t.Errorf("file %+v", h)
	}
}

// An open that cannot write a file it found behind the cache (git checked out an older one) leaves its stamps as they
// were, so the next open reads the directory again and writes the file (#552).
func TestOpenThatCouldNotWriteAFileTriesAgain(t *testing.T) {
	dir, cache := t.TempDir(), filepath.Join(t.TempDir(), "facts.db")
	s := openTextAt(t, dir, cache, "mac", t0)
	f, err := s.AddWith("Порт db1 — 5432", "x", "", Meta{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Correct(f.ID, "Порт db1 — 5433"); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	path := FactPath(dir, f.ID)
	h, _ := ReadFactFile(path)
	if err := WriteFactFile(path, h[:1]); err != nil { // the older file, written anew as git writes it
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(path), 0o500); err != nil {
		t.Fatal(err)
	}
	if s, err := OpenText(dir, cache); err == nil {
		_ = s.Close()
		t.Fatal("an open wrote into a read-only directory")
	}
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}

	s = openTextAt(t, dir, cache, "mac", t0)
	defer func() { _ = s.Close() }()
	textMatches(t, s)
}

// changeCounter is the file change counter of a SQLite file, which every write transaction moves in the rollback
// journal mode the store uses.
func changeCounter(t *testing.T, path string) uint32 {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return binary.BigEndian.Uint32(b[24:28])
}

// An open that finds nothing new writes nothing: every search opens the base, and two stamp commits made a search
// 2-4 ms slower than one on a file base (#557). An open after git changed a directory still takes the change in.
func TestOpenWithNothingNewWritesNothing(t *testing.T) {
	dir, cache := t.TempDir(), filepath.Join(t.TempDir(), "facts.db")
	s := openTextAt(t, dir, cache, "mac", t0)
	f, err := s.AddWith("Порт db1 — 5432", "x", "", Meta{})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	s = openTextAt(t, dir, cache, "mac", t0) // reads the directory the new file moved
	_ = s.Close()

	before := changeCounter(t, cache)
	for range 3 {
		s = openTextAt(t, dir, cache, "mac", t0)
		_ = s.Close()
	}
	if after := changeCounter(t, cache); after != before {
		t.Errorf("opens with nothing new wrote the cache: change counter %d → %d", before, after)
	}

	h, _ := ReadFactFile(FactPath(dir, f.ID))
	v2 := h[0]
	v2.Version, v2.Content, v2.Created = 2, "Порт db1 — 5433", t0.Add(time.Hour)
	if err := WriteFactFile(FactPath(dir, f.ID), append(h, v2)); err != nil {
		t.Fatal(err)
	}
	s = openTextAt(t, dir, cache, "mac", t0)
	defer func() { _ = s.Close() }()
	if got, err := s.Get(f.ID); err != nil || got.Content != v2.Content {
		t.Errorf("after git changed the file: %+v %v", got, err)
	}
}

// A missing cache is rebuilt from the complete text. Unreadable caches require explicit recovery (see content_text_test.go).
func TestCacheIsBuiltFromTheText(t *testing.T) {
	dir, cache := t.TempDir(), filepath.Join(t.TempDir(), "facts.db")
	s := openTextAt(t, dir, cache, "mac", t0)
	f, _ := s.AddWith("Порт db1 — 5432", "x", "", Meta{Kind: "environment"})
	_, _ = s.Correct(f.ID, "Порт db1 — 5433")
	_, _ = s.AddWith("Второй факт", "x", "2026-12-01", Meta{Scope: "global"})
	want := dump(t, s)
	_ = s.Close()

	for _, damage := range []func() error{
		func() error { return os.Remove(cache) },
	} {
		if err := damage(); err != nil {
			t.Fatal(err)
		}
		s = openTextAt(t, dir, cache, "mac", t0)
		if got := dump(t, s); got != want {
			t.Errorf("rebuilt cache:\n%s\nwant\n%s", got, want)
		}
		if len(ids(t, s, "db1")) != 1 {
			t.Error("the rebuilt cache does not find the fact")
		}
		_ = s.Close()
	}

}

// Writers in parallel lose no line of any file.
func TestTextHistoryUnderParallelWriters(t *testing.T) {
	dir, cache := t.TempDir(), filepath.Join(t.TempDir(), "facts.db")
	s0 := openTextAt(t, dir, cache, "mac", t0)
	_ = s0.Close()
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for w := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := OpenText(dir, cache)
			if err != nil {
				errs <- err
				return
			}
			defer func() { _ = s.Close() }()
			for i := range 10 {
				f, err := s.AddWith(fmt.Sprintf("Факт %d от писателя %d", i, w), "x", "", Meta{})
				if err != nil {
					errs <- err
					return
				}
				if _, err := s.Correct(f.ID, fmt.Sprintf("Факт %d от писателя %d, исправлен", i, w)); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	s := openTextAt(t, dir, cache, "mac", t0)
	defer func() { _ = s.Close() }()
	textMatches(t, s)
	if rows, _ := s.Rows(); len(rows) != 80 {
		t.Errorf("%d versions, want 80", len(rows))
	}
}

// A merge of a copy into a base with a text history rewrites the files of the facts it renumbered.
func TestMergeRewritesTheFactFiles(t *testing.T) {
	b, f := base(t)
	other := clone(t, b)
	o := openAt(t, other, "server", t0.Add(2*time.Hour))
	if _, err := o.Correct(f.ID, "Hermes работает на deepseek-pro"); err != nil {
		t.Fatal(err)
	}
	_ = o.Close()
	dir, cache := t.TempDir(), clone(t, b)
	s := openTextAt(t, dir, cache, "mac", t0.Add(time.Hour))
	defer func() { _ = s.Close() }()
	if _, err := s.Correct(f.ID, "Hermes работает на deepseek-flash, fallback Codex"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Merge(other); err != nil {
		t.Fatal(err)
	}
	textMatches(t, s)
}

// A cache an older memcue built from the text lacks the channel of the labelled versions (#625): opened by this
// memcue, it is set aside and built again, so the labelled version is read whole and not merged as a second one.
func TestCacheOfAnOlderSchemaIsBuiltAgain(t *testing.T) {
	dir, cache := t.TempDir(), filepath.Join(t.TempDir(), "facts.db")
	s := openTextAt(t, dir, cache, "mac", t0)
	f, err := s.AddWith("Commit messages are written in English.", "x", "",
		Meta{Kind: "rule", Scope: "global", Source: "user"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.setChannel(f.ID, f.Version, behaviour); err != nil {
		t.Fatal(err)
	}
	want := dump(t, s)
	// What the older memcue leaves: the rows without the column, under its schema version.
	for _, q := range []string{`ALTER TABLE facts DROP COLUMN channel`, `PRAGMA user_version = 6`} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	_ = s.Close()

	s = openTextAt(t, dir, cache, "mac", t0)
	defer func() { _ = s.Close() }()
	if got := dump(t, s); got != want {
		t.Errorf("rebuilt cache:\n%s\nwant\n%s", got, want)
	}
	hist, err := s.History(f.ID)
	if err != nil || len(hist) != 2 || hist[1].Channel != behaviour {
		t.Errorf("history %+v (%v), want the two versions with the label on the second", hist, err)
	}
	if aside, _ := filepath.Glob(cache + ".schema6-*"); len(aside) != 1 {
		t.Errorf("older cache set aside: %v", aside)
	}
}

// A cache build stopped midway leaves no cache, so no read-only open fails on a hot journal of it, and the next open
// builds it from the start (#635). The build here stops on a damaged fact file, the way a caller's timeout stops it
// on a large base; the leftovers of a killed build, a half-built file and its journal, go before the next build.
func TestAStoppedCacheBuildLeavesNoCache(t *testing.T) {
	dir, cache := t.TempDir(), filepath.Join(t.TempDir(), "facts.db")
	active, _, _, _, _ := unlabelled(t, dir)
	good, err := os.ReadFile(FactPath(dir, active.ID))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(FactPath(dir, active.ID), []byte("{not json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if s, err := OpenText(dir, cache); err == nil {
		_ = s.Close()
		t.Fatal("a build over a damaged file succeeded")
	}
	if _, err := os.Stat(cache); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a stopped build left a cache: %v", err)
	}
	if s, err := OpenReadOnly(cache); err == nil {
		_ = s.Close()
		t.Fatal("a read-only open found a cache")
	} else if strings.Contains(err.Error(), "readonly") {
		t.Fatalf("a read-only open met a hot journal: %v", err)
	}
	if err := os.WriteFile(cache+".build-journal", []byte("a journal of a killed build"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(FactPath(dir, active.ID), good, 0o600); err != nil {
		t.Fatal(err)
	}
	s := openTextAt(t, dir, cache, "laptop", t0)
	textMatches(t, s)
	if got, err := s.Get(active.ID); err != nil || got.Content != "Порт db1 — 5433" {
		t.Errorf("built cache: %+v %v", got, err)
	}
	_ = s.Close()
	for _, name := range []string{cache + ".build", cache + ".build-journal"} {
		if _, err := os.Stat(name); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s left behind: %v", name, err)
		}
	}
	r, err := OpenReadOnly(cache)
	if err != nil {
		t.Fatalf("read-only open of the built cache: %v", err)
	}
	_ = r.Close()
}

// A version the cache committed while its file could not take it is written to the text before a cache of an older
// schema is set aside (#640): the new cache is built from the text, and the version would be lost otherwise.
func TestASetAsideCacheGivesTheTextWhatItLacks(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes into a read-only directory")
	}
	dir, cache := t.TempDir(), filepath.Join(t.TempDir(), "facts.db")
	s := openTextAt(t, dir, cache, "mac", t0)
	kept, err := s.AddWith("Порт db1 — 5432", "x 2026-10-19", "", Meta{})
	if err != nil {
		t.Fatal(err)
	}
	// the files cannot take the next write: it reaches the cache only
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	bucket := filepath.Dir(FactPath(dir, kept.ID))
	if err := os.Chmod(bucket, 0o500); err != nil {
		t.Fatal(err)
	}
	onlyCache, err := s.AddWith("Caddy слушает порт 8443", "x 2026-10-19", "", Meta{})
	if err != nil {
		t.Fatal(err)
	}
	corrected, err := s.Correct(kept.ID, "Порт db1 — 5433")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(bucket, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if file, _ := ReadFactFile(FactPath(dir, onlyCache.ID)); len(file) != 0 {
		t.Fatalf("the write reached the text: %+v", file)
	}
	// the cache was written by an older memcue: the next open sets it aside
	if _, err := s.db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, schemaVersion-1)); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	s = openTextAt(t, dir, cache, "laptop", t0)
	defer func() { _ = s.Close() }()
	if got, err := s.Get(onlyCache.ID); err != nil || got.Content != onlyCache.Content {
		t.Errorf("the fact only the old cache held: %+v %v", got, err)
	}
	if got, err := s.Get(kept.ID); err != nil || got.Version != corrected.Version || got.Content != "Порт db1 — 5433" {
		t.Errorf("the correction only the old cache held: %+v %v", got, err)
	}
	textMatches(t, s)
}

// A write whose file failed stays owed after a later write succeeds (#640): the later write used to save the count of
// the cache, the next open then saw the files as complete, and the version stayed in the cache only, out of git and
// off the other machines.
func TestAFailedFileWriteIsNotHiddenByALaterOne(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes into a read-only directory")
	}
	dir, cache := t.TempDir(), filepath.Join(t.TempDir(), "facts.db")
	s := openTextAt(t, dir, cache, "mac", t0)
	kept, err := s.AddWith("Порт db1 — 5432", "x 2026-10-19", "", Meta{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	bucket := filepath.Dir(FactPath(dir, kept.ID))
	if err := os.Chmod(bucket, 0o500); err != nil {
		t.Fatal(err)
	}
	// a new fact needs a new file and fails; a correction appends to the file it has and succeeds
	lost, err := s.AddWith("Caddy слушает порт 8443", "x 2026-10-19", "", Meta{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Correct(kept.ID, "Порт db1 — 5433"); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(bucket, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if file, _ := ReadFactFile(FactPath(dir, lost.ID)); len(file) != 0 {
		t.Fatalf("the failed write reached the text: %+v", file)
	}
	_ = s.Close()
	s = openTextAt(t, dir, cache, "laptop", t0)
	defer func() { _ = s.Close() }()
	if file, err := ReadFactFile(FactPath(dir, lost.ID)); err != nil || len(file) != 1 || file[0].Content != lost.Content {
		t.Errorf("the next open did not write the failed version: %+v %v", file, err)
	}
	textMatches(t, s)
}

// A fact file whose name is not a fact id, that sits in the wrong directory, that holds another fact or that is a
// symbolic link is refused at the open, before its id can decide where a later write goes (#641); the refusal names
// the file and not what it holds.
func TestAnOpenRefusesAFileThatIsNotAFactFile(t *testing.T) {
	const id = "01JSOURCE00000000000000001"
	secret := "SECRET-OUTSIDE-THE-HISTORY"
	fact := func(id, content string) []Fact {
		return []Fact{{ID: id, Version: 1, Content: content, Created: t0, Status: Active, Author: "mac"}}
	}
	cases := []struct {
		name  string
		place func(t *testing.T, dir string)
	}{
		{"a short name", func(t *testing.T, dir string) {
			writeFile(t, filepath.Join(dir, "1", "01.jsonl"), fact("01", "x"))
		}},
		{"dots in the name", func(t *testing.T, dir string) {
			writeFile(t, filepath.Join(dir, "1", "01JSOURCE0..0000000000001.jsonl"), fact("01JSOURCE0..0000000000001", "x"))
		}},
		{"a lowercase name", func(t *testing.T, dir string) {
			writeFile(t, filepath.Join(dir, "1", "01jsource00000000000000001.jsonl"), fact("01jsource00000000000000001", "x"))
		}},
		{"the wrong directory", func(t *testing.T, dir string) {
			writeFile(t, filepath.Join(dir, "2", id+".jsonl"), fact(id, "x"))
		}},
		{"another fact inside", func(t *testing.T, dir string) {
			writeFile(t, FactPath(dir, id), fact("../../../escape/01JSOURCE0000000000000001", "x"))
		}},
		{"a symbolic link out of the history", func(t *testing.T, dir string) {
			outside := filepath.Join(t.TempDir(), "secret.jsonl")
			writeFile(t, outside, fact(id, secret))
			if err := os.MkdirAll(filepath.Dir(FactPath(dir, id)), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, FactPath(dir, id)); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir, cache := t.TempDir(), filepath.Join(t.TempDir(), "facts.db")
			c.place(t, dir)
			s, err := OpenText(dir, cache)
			if err == nil {
				_ = s.Close()
				t.Fatal("opened")
			}
			if !strings.Contains(err.Error(), dir) || strings.Contains(err.Error(), secret) {
				t.Errorf("error %q: want the file named and nothing it holds", err)
			}
		})
	}
	// a fact file as memcue writes it opens
	dir, cache := t.TempDir(), filepath.Join(t.TempDir(), "facts.db")
	writeFile(t, FactPath(dir, id), fact(id, "Порт db1 — 5432"))
	s := openTextAt(t, dir, cache, "mac", t0)
	defer func() { _ = s.Close() }()
	if got, err := s.Get(id); err != nil || got.Content != "Порт db1 — 5432" {
		t.Errorf("a valid file: %+v %v", got, err)
	}
}

func writeFile(t *testing.T, path string, versions []Fact) {
	t.Helper()
	if err := WriteFactFile(path, versions); err != nil {
		t.Fatal(err)
	}
}
