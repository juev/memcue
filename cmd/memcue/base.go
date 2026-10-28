package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/juev/memcue/internal/store"
)

// factBase is where the facts of this process live (#530): one SQLite file, the format before the text history and
// still read until every machine has moved, or a directory of text history whose SQLite cache sits in the state
// directory, outside git.
type factBase struct {
	path string // the SQLite file, or the directory of the text history
	text bool
}

// factsBase is the base factsPath names. A directory is a text history. A file name ending in .db is the old format,
// unless the directory of the same name without .db exists: then the base has moved to text, on this machine or on
// another one whose move git has brought here, and the file, if git left it, is no longer read. A path that does not
// exist and does not end in .db names a text history yet to be written.
func factsBase() factBase {
	p := factsPath()
	if isDir(p) {
		return factBase{path: p, text: true}
	}
	if strings.HasSuffix(p, ".db") {
		if t := strings.TrimSuffix(p, ".db"); isDir(t) {
			return factBase{path: t, text: true}
		}
		return factBase{path: p}
	}
	if _, err := os.Stat(p); err == nil {
		return factBase{path: p} // a SQLite file under another name
	}
	return factBase{path: p, text: true}
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// exists says whether the base has been written: a check that must not create it looks here.
func (b factBase) exists() bool {
	_, err := os.Stat(b.path)
	return err == nil
}

// cachePath is the SQLite cache of a text history: facts.db in the state directory of the base.
func cachePath(b factBase) (string, error) {
	dir, err := stateDirOf(b)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "facts.db"), nil
}

// openBase opens the base of this process for reading and writing.
func openBase() (*store.Store, error) {
	b := factsBase()
	if !b.text {
		return store.Open(b.path)
	}
	cache, err := cachePath(b)
	if err != nil {
		return nil, err
	}
	return store.OpenText(b.path, cache)
}

// openBaseReadOnly opens the base without changing it, for a report that must see the base as it is. A text history
// is read through its cache, which an ordinary open builds first when there is none yet.
func openBaseReadOnly() (*store.Store, error) {
	b := factsBase()
	if !b.text {
		return store.OpenReadOnly(b.path)
	}
	cache, err := cachePath(b)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(cache); os.IsNotExist(err) {
		s, err := store.OpenText(b.path, cache)
		if err != nil {
			return nil, err
		}
		if err := s.Close(); err != nil {
			return nil, err
		}
	}
	return store.OpenReadOnly(cache)
}

// runMigrateText moves a base of the old format to a text history (#530): the directory next to the file, named as
// the file without .db, gets a file per fact, the base becomes the cache of that history in the state directory, and
// the file is removed, so that git carries the removal to the other machines along with the text. A second run, on a
// base already moved, takes in a file an interrupted run or a stale copy left beside the text, and removes it.
func runMigrateText(out io.Writer) error {
	b := factsBase()
	if b.text {
		return takeInLeftover(b, out)
	}
	if !b.exists() {
		return fmt.Errorf("migrate -text: no fact base at %s", b.path)
	}
	if !strings.HasSuffix(b.path, ".db") {
		return fmt.Errorf("migrate -text: the base %s must be named *.db, so other machines find its text by the name", b.path)
	}
	old, err := store.Open(b.path) // brings an older schema up to date before the copy
	if err != nil {
		return err
	}
	text := factBase{path: strings.TrimSuffix(b.path, ".db"), text: true}
	cache, err := cachePath(text)
	if err != nil {
		_ = old.Close()
		return err
	}
	// The copy goes beside the cache and is renamed into place: a cache left by an earlier attempt is replaced, since
	// the file is still the base.
	tmp := cache + ".migrate"
	_ = os.Remove(tmp)
	err = old.Backup(tmp)
	if cerr := old.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, cache)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("migrate -text: copy the base to %s: %w", cache, err)
	}
	s, err := store.OpenText(text.path, cache) // the cache holds versions the empty text lacks: the open writes them all
	if err != nil {
		return err
	}
	facts, versions, err := countText(s)
	if cerr := s.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if err := os.Remove(b.path); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "%d facts (%d versions) written to %s\ncache %s\nremoved %s\n"+
		"commit %s and the removal of %s; other machines pick the text up after a pull\n",
		facts, versions, text.path, cache, b.path, text.path, filepath.Base(b.path)); err != nil {
		return err
	}
	return runInstall(nil, out) // routes the fact files to the merge driver, or says the base is outside git
}

// takeInLeftover is migrate -text on a base already moved: a file of the old format beside the text is merged into it
// and removed.
func takeInLeftover(b factBase, out io.Writer) error {
	left := b.path + ".db"
	if _, err := os.Stat(left); os.IsNotExist(err) {
		_, err := fmt.Fprintf(out, "the base is already a text history at %s\n", b.path)
		return err
	}
	s, err := openBase()
	if err != nil {
		return err
	}
	st, err := s.Merge(left)
	if cerr := s.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("migrate -text: take in %s: %w", left, err)
	}
	if err := os.Remove(left); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "took %d versions from %s into the text history at %s and removed it\n", st.Added, left,
		b.path)
	return err
}

// countText counts the facts and versions the text history of s holds.
func countText(s *store.Store) (facts, versions int, err error) {
	files, err := filepath.Glob(filepath.Join(s.Text(), "*", "*.jsonl"))
	if err != nil {
		return 0, 0, err
	}
	for _, path := range files {
		h, err := store.ReadFactFile(path)
		if err != nil {
			return 0, 0, err
		}
		facts++
		versions += len(h)
	}
	return facts, versions, nil
}
