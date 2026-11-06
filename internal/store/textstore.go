package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
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
	textRowsDirty = "dirty"     // a write to the files failed since the last open brought them together
)

// OpenText opens the base whose history is the text at dir, with its cache at cache. A cache that does not open (a
// newer schema, a damaged file) is set aside and built again from the text: it holds nothing the text lacks once the
// last open brought them together. So is a cache of an older schema: the memcue that filled it read the files without
// the fields added since (channel, #625), and merged by versions its copy of such a version and the file's would
// count as two.
func OpenText(dir, cache string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if v, err := cacheSchema(cache); err == nil && v > 0 && v < schemaVersion {
		if err := setAside(dir, cache, v); err != nil {
			return nil, fmt.Errorf("text history %s: %w", dir, err)
		}
	}
	if err := buildCache(dir, cache); err != nil {
		return nil, fmt.Errorf("text history %s: %w", dir, err)
	}
	s, err := openFile(cache)
	if err != nil {
		aside := fmt.Sprintf("%s.unreadable-%d", cache, time.Now().Unix())
		if renameErr := os.Rename(cache, aside); renameErr != nil {
			return nil, err
		}
		if err := buildCache(dir, cache); err != nil {
			return nil, fmt.Errorf("text history %s: %w", dir, err)
		}
		if s, err = openFile(cache); err != nil {
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

// buildCache builds the cache at cache from the text history when there is none (#635). It builds under another
// name and renames the file into place once the build has committed: a caller's timeout that kills a build of
// seconds then leaves no cache, and the next open builds it again, where a build in place left a hot journal that
// every read-only open failed on. Parallel first opens build once: the others wait on the lock and find the cache.
func buildCache(dir, cache string) error {
	if _, err := os.Stat(cache); err == nil || !errors.Is(err, os.ErrNotExist) {
		return err
	}
	lock := cache + ".lock"
	if err := os.MkdirAll(filepath.Dir(cache), 0o700); err != nil {
		return err
	}
	unlock, err := lockFile(lock)
	if err != nil {
		return err
	}
	defer unlock()
	if _, err := os.Stat(cache); err == nil || !errors.Is(err, os.ErrNotExist) {
		return err // built by another process while this one waited
	}
	build := cache + ".build"
	for _, name := range []string{build, build + "-journal", build + "-wal", build + "-shm"} {
		if err := os.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err // a build stopped midway: start again
		}
	}
	s, err := openFile(build)
	if err != nil {
		return err
	}
	s.text, s.textLock = dir, lock
	if err := s.syncTextLocked(); err != nil {
		_ = s.Close()
		return err
	}
	if err := s.Close(); err != nil {
		return err
	}
	return os.Rename(build, cache)
}

// setAside moves a cache of the older schema v out of the way, to be built again from the text (#625). A version the
// cache committed but its file did not get (the append after the commit failed, #640) is in no file yet: the next
// open of the same schema would write it, and setting the cache aside would lose it. So the versions the files lack
// go into them first, under the lock of the text.
func setAside(dir, cache string, v int) error {
	unlock, err := lockFile(cache + ".lock")
	if err != nil {
		return err
	}
	defer unlock()
	if now, err := cacheSchema(cache); err != nil || now != v {
		return err // set aside by another process while this one waited
	}
	if err := rescueVersions(dir, cache); err != nil {
		return err
	}
	return os.Rename(cache, fmt.Sprintf("%s.schema%d-%d", cache, v, time.Now().Unix()))
}

// rescueVersions appends to the fact files the versions the cache at cache holds past the last version of each file.
// It reads every file rather than trust the stamp textRowsStamp: an older memcue saved the count after a write that
// succeeded even when an earlier one had failed, so its stamp can claim versions no file holds (#640). A set-aside
// happens once an upgrade, while the build that follows reads every file anyway. A version numbered at or below a
// file's last one stays out: a file that git changed since holds a history only a merge could bring together, and
// this memcue would not merge with a cache of another schema. A cache it cannot read (a hot journal) is set aside as
// before and kept next to the new one: an open must not fail on it.
func rescueVersions(dir, cache string) error {
	r, err := OpenReadOnly(cache)
	if err != nil {
		return nil
	}
	defer func() { _ = r.Close() }()
	all, err := r.Rows()
	if err != nil {
		return err
	}
	byID := map[string][]Fact{}
	var ids []string
	for _, f := range all {
		if _, ok := byID[f.ID]; !ok {
			ids = append(ids, f.ID)
		}
		byID[f.ID] = append(byID[f.ID], f)
	}
	for _, id := range ids {
		path := FactPath(dir, id)
		file, err := ReadFactFile(path)
		if err != nil {
			return err
		}
		last := 0
		for _, f := range file {
			last = max(last, f.Version)
		}
		var missing []Fact
		for _, f := range byID[id] {
			if f.Version > last {
				missing = append(missing, f)
			}
		}
		if len(missing) > 0 {
			if err := appendFacts(path, missing); err != nil {
				return err
			}
		}
	}
	return nil
}

// cacheSchema is the schema version of the cache file at path as it is, 0 for a file that is not there.
func cacheSchema(path string) (int, error) {
	if _, err := os.Stat(path); err != nil {
		return 0, nil
	}
	dsn, _, err := readOnlyDataSource(path)
	if err != nil {
		return 0, err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return 0, err
	}
	defer func() { _ = db.Close() }()
	var v int
	err = db.QueryRow(`PRAGMA user_version`).Scan(&v)
	return v, err
}

// Text is the directory of the text history, empty for a base that is one SQLite file.
func (s *Store) Text() string { return s.text }

// lockText holds the files of the base for this process until the returned function runs: an append and the
// rewrite of the same file by another process must not interleave.
func (s *Store) lockText() (func(), error) {
	return lockFile(s.textLock)
}

// lockFile holds an exclusive lock on the file at path, created when missing, until the returned function runs.
func lockFile(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
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

// textWritten appends the versions the cache has just committed to their files. A failure marks the stamp dirty, and
// the next open writes what is missing; the write itself has already succeeded, and repeating it would store a second
// fact. A later write that succeeds leaves a dirty stamp as it is: saving its count would tell the next open that the
// files hold every version, the one that failed included (#640).
func (s *Store) textWritten(versions ...Fact) {
	if s.text == "" || len(versions) == 0 {
		return
	}
	dirty := func() { _ = saveStamp(s.db, textRowsStamp, textRowsDirty) }
	unlock, err := s.lockText()
	if err != nil {
		dirty()
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
			dirty()
			return
		}
	}
	if v, err := s.stamp(textRowsStamp); err != nil || v == textRowsDirty {
		return
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

// syncText brings the cache and the text history together (see the comment at the top of this file). Everything it
// takes into the cache goes in one transaction: a cache built from the text takes in every fact, and a commit for each
// of them made the build take seconds (#552). The files are written after the commit and the stamps after the files,
// so an open stopped between them leaves stamps that make the next open look again.
func (s *Store) syncText() error {
	unlock, err := s.lockText()
	if err != nil {
		return err
	}
	defer unlock()
	return s.syncTextLocked()
}

// syncTextLocked is syncText for a caller that holds the lock of the text.
func (s *Store) syncTextLocked() error {
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
	rows, err := s.stamp(textRowsStamp)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	files := map[string][]Fact{} // path → the history its file must hold
	for name, mtime := range dirs {
		if saved[name] == mtime {
			continue
		}
		if err := s.importTextDir(tx, filepath.Join(s.text, name), files); err != nil {
			return err
		}
	}
	var n int
	if err := tx.QueryRow(`SELECT count(*) FROM facts`).Scan(&n); err != nil {
		return err
	}
	if rows != strconv.Itoa(n) {
		if err := s.exportText(tx, files); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	for path, versions := range files {
		if err := WriteFactFile(path, versions); err != nil {
			return err
		}
	}
	// The facts without a source are relabelled once the text is in the cache, under the lock of this open.
	relabelled, err := s.agentSources()
	if err != nil {
		return err
	}
	for _, f := range relabelled {
		if err := appendFacts(FactPath(s.text, f.ID), []Fact{f}); err != nil {
			return err
		}
	}
	// A stamp is written only when it changes: each write is a commit, and an open that found nothing new would pay
	// two of them on every search (#557).
	if !maps.Equal(dirs, saved) {
		stamp, err := json.Marshal(dirs)
		if err != nil {
			return err
		}
		if err := saveStamp(s.db, textDirsStamp, string(stamp)); err != nil {
			return err
		}
	}
	if rows == strconv.Itoa(n) && len(relabelled) == 0 {
		return nil
	}
	return s.saveTextRows()
}

// importTextDir brings the fact files of one directory into the cache, and notes in files the ones that lack versions
// of the cache.
func (s *Store) importTextDir(tx *sql.Tx, dir string, files map[string][]Fact) error {
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
		if e.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%s: a fact file must not be a symbolic link", path)
		}
		id := strings.TrimSuffix(name, ".jsonl")
		if !validID(id) || filepath.Base(dir) != id[len(id)-1:] {
			return fmt.Errorf("%s: not a fact file: the name must be a fact id, in the directory of its last character", path)
		}
		file, err := ReadFactFile(path)
		if err != nil {
			return err
		}
		for _, f := range file {
			if f.ID != id {
				return fmt.Errorf("%s: holds a version of fact %q, not of %s", path, f.ID, id)
			}
		}
		if len(file) == 0 {
			continue
		}
		cached, err := queryOn(tx, `SELECT `+s.cols("")+` FROM facts WHERE id = ? ORDER BY version`, file[0].ID)
		if err != nil {
			return err
		}
		if err := bringTogether(tx, file[0].ID, cached, file, path, files); err != nil {
			return err
		}
	}
	return nil
}

// validID says whether id can name a fact file (#641): 26 characters of digits and capital letters, the shape of a
// ULID. An id from a file of the text decides the path of every later write of that fact, so a separator, a dot or
// an empty id must not get that far. Every id memcue writes is a ULID; the check is looser than ULID parsing only so
// that it names what keeps a path inside the history.
func validID(id string) bool {
	if len(id) != 26 {
		return false
	}
	for _, r := range id {
		if (r < '0' || r > '9') && (r < 'A' || r > 'Z') {
			return false
		}
	}
	return true
}

// exportText notes in files every fact whose file lacks versions the cache holds.
func (s *Store) exportText(tx *sql.Tx, files map[string][]Fact) error {
	rows, err := queryOn(tx, `SELECT `+s.cols("")+` FROM facts ORDER BY id, version`)
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
		if _, ok := files[path]; ok {
			continue // already merged with its file in this open
		}
		file, err := ReadFactFile(path)
		if err != nil {
			return err
		}
		if sameHistory(file, byID[id]) {
			continue
		}
		if err := bringTogether(tx, id, byID[id], file, path, files); err != nil {
			return err
		}
	}
	return nil
}

// bringTogether merges the history of id in its file with the one in the cache: the cache gets the versions of the
// file inside tx, and files notes the merged history for a file that lacks some of it.
func bringTogether(tx *sql.Tx, id string, cached, file []Fact, path string, files map[string][]Fact) error {
	if sameHistory(file, cached) {
		return nil
	}
	h := MergeHistories(cached, file)
	if h.Changed() {
		if err := replaceTail(tx, id, h); err != nil {
			return err
		}
		if err := reindex(tx, id); err != nil {
			return err
		}
	}
	if !sameHistory(file, h.Versions) {
		files[path] = h.Versions
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
