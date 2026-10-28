package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// A base with a text history keeps its facts in the files of dir (see text.go) and its SQLite file as a cache that
// search reads (#530). Every write goes to the cache and then to the file of each fact it touched. Opening the base
// brings the two together in both directions: versions git brought into the files go into the cache, and versions the
// cache holds but a file lacks (a process stopped between the two writes) go into the file.
//
// Finding what git changed must cost almost nothing, since every search opens the base. Git writes a file anew when it
// checks one out or merges it, which changes the modification time of its directory; memcue itself only appends to a
// file, which does not. So the stamp textDirsStamp keeps the modification time of every directory as the last open saw
// it, and an open reads only the directories whose time moved. A new fact file of this memcue moves its directory too,
// and the next open reads that directory once and finds nothing new.

const (
	textDirsStamp = "text-dirs" // JSON object: directory name → modification time in nanoseconds
	textRowsStamp = "text-rows" // versions of the cache known to be in the files
)

// OpenText opens the base whose history is the text at dir, with its cache at cache. A cache that does not open (a
// newer schema, a damaged file) is set aside and built again from the text: it holds nothing the text lacks once the
// last open brought them together.
func OpenText(dir, cache string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s, err := Open(cache)
	if err != nil {
		aside := fmt.Sprintf("%s.unreadable-%d", cache, time.Now().Unix())
		if renameErr := os.Rename(cache, aside); renameErr != nil {
			return nil, err
		}
		if s, err = Open(cache); err != nil {
			return nil, err
		}
	}
	s.text, s.textLock = dir, cache+".lock"
	if err := s.syncText(); err != nil {
		_ = s.Close()
		return nil, fmt.Errorf("text history %s: %w", dir, err)
	}
	return s, nil
}

// Text is the directory of the text history, empty for a base that is one SQLite file.
func (s *Store) Text() string { return s.text }

// lockText holds the files of the base for this process until the returned function runs: an append and the
// rewrite of the same file by another process must not interleave.
func (s *Store) lockText() (func(), error) {
	f, err := os.OpenFile(s.textLock, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}

// textWritten appends the versions the cache has just committed to their files. A failure leaves the stamp behind,
// and the next open writes what is missing; the write itself has already succeeded, and repeating it would store a
// second fact.
func (s *Store) textWritten(versions ...Fact) {
	if s.text == "" || len(versions) == 0 {
		return
	}
	unlock, err := s.lockText()
	if err != nil {
		return
	}
	defer unlock()
	var ids []string
	byID := map[string][]Fact{}
	for _, f := range versions {
		if _, ok := byID[f.ID]; !ok {
			ids = append(ids, f.ID)
		}
		byID[f.ID] = append(byID[f.ID], f)
	}
	for _, id := range ids {
		if err := appendFacts(FactPath(s.text, id), byID[id]); err != nil {
			return
		}
	}
	_ = s.saveTextRows()
}

// textRewritten replaces the files of ids with their history in the cache, after a merge renumbered it.
func (s *Store) textRewritten(ids []string) error {
	if s.text == "" || len(ids) == 0 {
		return nil
	}
	unlock, err := s.lockText()
	if err != nil {
		return err
	}
	defer unlock()
	for _, id := range ids {
		h, err := s.History(id)
		if err != nil {
			return err
		}
		if err := WriteFactFile(FactPath(s.text, id), h); err != nil {
			return err
		}
	}
	return s.saveTextRows()
}

func (s *Store) saveTextRows() error {
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM facts`).Scan(&n); err != nil {
		return err
	}
	return saveStamp(s.db, textRowsStamp, strconv.Itoa(n))
}

func (s *Store) stamp(name string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM stamps WHERE name = ?`, name).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// textDirs is the modification time of every fact directory of the text history.
func textDirs(dir string) (map[string]int64, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := map[string]int64{}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return nil, err
		}
		out[e.Name()] = info.ModTime().UnixNano()
	}
	return out, nil
}

// syncText brings the cache and the text history together (see the comment at the top of this file).
func (s *Store) syncText() error {
	unlock, err := s.lockText()
	if err != nil {
		return err
	}
	defer unlock()
	// The times are read before the files: a directory git changes during the reading keeps a newer time than the
	// stamp, and the next open reads it again.
	dirs, err := textDirs(s.text)
	if err != nil {
		return err
	}
	saved := map[string]int64{}
	if v, err := s.stamp(textDirsStamp); err != nil {
		return err
	} else if v != "" {
		_ = json.Unmarshal([]byte(v), &saved)
	}
	for name, mtime := range dirs {
		if saved[name] == mtime {
			continue
		}
		if err := s.importTextDir(filepath.Join(s.text, name)); err != nil {
			return err
		}
	}
	rows, err := s.stamp(textRowsStamp)
	if err != nil {
		return err
	}
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM facts`).Scan(&n); err != nil {
		return err
	}
	if rows != strconv.Itoa(n) {
		if err := s.exportText(); err != nil {
			return err
		}
	}
	stamp, err := json.Marshal(dirs)
	if err != nil {
		return err
	}
	if err := saveStamp(s.db, textDirsStamp, string(stamp)); err != nil {
		return err
	}
	return s.saveTextRows()
}

// importTextDir brings the fact files of one directory into the cache.
func (s *Store) importTextDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		path := filepath.Join(dir, name)
		file, err := ReadFactFile(path)
		if err != nil {
			return err
		}
		if len(file) == 0 {
			continue
		}
		if err := s.bringTogether(file[0].ID, file, path); err != nil {
			return err
		}
	}
	return nil
}

// exportText writes into the files every version the cache holds and they lack.
func (s *Store) exportText() error {
	rows, err := s.Rows()
	if err != nil {
		return err
	}
	byID := map[string][]Fact{}
	var ids []string
	for _, f := range rows {
		if _, ok := byID[f.ID]; !ok {
			ids = append(ids, f.ID)
		}
		byID[f.ID] = append(byID[f.ID], f)
	}
	for _, id := range ids {
		path := FactPath(s.text, id)
		file, err := ReadFactFile(path)
		if err != nil {
			return err
		}
		if sameHistory(file, byID[id]) {
			continue
		}
		if err := s.bringTogether(id, file, path); err != nil {
			return err
		}
	}
	return nil
}

// bringTogether merges the history of id in its file with the one in the cache and writes the result wherever it
// differs: the cache gets the versions of the file, the file the versions of the cache.
func (s *Store) bringTogether(id string, file []Fact, path string) error {
	cached, err := s.History(id)
	if err != nil {
		return err
	}
	if sameHistory(file, cached) {
		return nil
	}
	h := MergeHistories(cached, file)
	if h.Added > 0 {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		if err := replaceTail(tx, id, h); err != nil {
			return err
		}
		if err := reindex(tx, id); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	if !sameHistory(file, h.Versions) {
		return WriteFactFile(path, h.Versions)
	}
	return nil
}

// sameHistory reports whether a and b hold the same versions under the same numbers.
func sameHistory(a, b []Fact) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ID != b[i].ID || a[i].Version != b[i].Version || !same(a[i], b[i]) {
			return false
		}
	}
	return true
}
