package store

import (
	"fmt"
	"os"
	"path/filepath"
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
	f, err := s.AddWith("Порт db1 — 5432", []string{"db1"}, "x 2026-10-19", "", Meta{Kind: "environment"})
	if err != nil {
		t.Fatal(err)
	}
	g, err := s.AddWith("Порт db2 — 6543; бэкап в 3:00", nil, "x 2026-10-19", "", Meta{})
	if err != nil {
		t.Fatal(err)
	}
	steps := []func() error{
		func() error { _, err := s.Correct(f.ID, "Порт db1 — 5433", []string{"db1"}); return err },
		func() error { _, err := s.SetQueries(f.ID, 2, []string{"какой порт у db1"}); return err },
		func() error { _, err := s.Relabel(f.ID, 3, "pitfall", "global", ""); return err },
		func() error { _, err := s.Retag(f.ID, 4, "y 2026-10-20", "repo:y"); return err },
		func() error { _, err := s.Retire(f.ID); return err },
		func() error { _, err := s.Restore(f.ID); return err },
		func() error { _, err := s.SplitFact(g.ID, 1, []string{"Порт db2 — 6543", "Бэкап db2 в 3:00"}); return err },
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
	f, err := s.AddWith("Hermes работает на модели deepseek-flash", []string{"hermes"}, "x", "", Meta{})
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

// A version the cache committed while its file missed it (the process stopped between the two writes) reaches the
// file at the next open.
func TestOpenWritesWhatTheFileMissed(t *testing.T) {
	dir, cache := t.TempDir(), filepath.Join(t.TempDir(), "facts.db")
	s := openTextAt(t, dir, cache, "mac", t0)
	f, err := s.AddWith("Порт db1 — 5432", nil, "x", "", Meta{})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	plain := openAt(t, cache, "mac", t0.Add(time.Hour)) // writes the cache only, as a stopped process left it
	if _, err := plain.Correct(f.ID, "Порт db1 — 5433", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := plain.AddWith("Новый факт без файла", nil, "x", "", Meta{}); err != nil {
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

// The cache is disposable: a missing one is built from the text, an unreadable one is set aside and built again.
func TestCacheIsBuiltFromTheText(t *testing.T) {
	dir, cache := t.TempDir(), filepath.Join(t.TempDir(), "facts.db")
	s := openTextAt(t, dir, cache, "mac", t0)
	f, _ := s.AddWith("Порт db1 — 5432", []string{"db1"}, "x", "", Meta{Kind: "environment"})
	_, _ = s.Correct(f.ID, "Порт db1 — 5433", []string{"db1"})
	_, _ = s.AddWith("Второй факт", nil, "x", "2026-12-01", Meta{Scope: "global"})
	want := dump(t, s)
	_ = s.Close()

	for _, damage := range []func() error{
		func() error { return os.Remove(cache) },
		func() error { return os.WriteFile(cache, []byte("not a database at all, just text"), 0o600) },
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
	if aside, _ := filepath.Glob(cache + ".unreadable-*"); len(aside) != 1 {
		t.Errorf("unreadable cache set aside: %v", aside)
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
				f, err := s.AddWith(fmt.Sprintf("Факт %d от писателя %d", i, w), nil, "x", "", Meta{})
				if err != nil {
					errs <- err
					return
				}
				if _, err := s.Correct(f.ID, fmt.Sprintf("Факт %d от писателя %d, исправлен", i, w), nil); err != nil {
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
	if _, err := o.Correct(f.ID, "Hermes работает на deepseek-pro", nil); err != nil {
		t.Fatal(err)
	}
	_ = o.Close()
	dir, cache := t.TempDir(), clone(t, b)
	s := openTextAt(t, dir, cache, "mac", t0.Add(time.Hour))
	defer func() { _ = s.Close() }()
	if _, err := s.Correct(f.ID, "Hermes работает на deepseek-flash, fallback Codex", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Merge(other); err != nil {
		t.Fatal(err)
	}
	textMatches(t, s)
}
