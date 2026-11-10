package store

import (
	"bytes"
	"crypto/sha256"
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
// file, which would not, so an append sets the directory's time itself: another cache of the same history must read
// the new version too (#709). So the stamp textDirsStamp keeps the modification time of every directory as the last
// open saw it, and an open reads only the directories whose time moved. A write of this memcue moves its directory
// too, and the next open reads that directory once and finds nothing new.

const (
	textDirsStamp = "text-dirs" // JSON object: directory name → modification time in nanoseconds
	textRowsStamp = "text-rows" // versions of the cache known to be in the files
	textRowsDirty = "dirty"     // a write to the files failed since the last open brought them together
)

// OpenText opens a text history and its derived SQLite cache. Readable older caches
// are retained after rescuing pending versions. Unreadable/newer caches are refused:
// silently rebuilding one could lose versions whose JSONL write never completed.
// Legacy auxiliary JSONL is backed up outside the current history and rewritten
// under the text lock. See docs/content-only-upgrade.md for coordinated rollout.
func OpenText(dir, cache string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	v, err := cacheSchema(cache)
	if err != nil {
		return nil, fmt.Errorf("inspect text cache %s: %w", cache, err)
	}
	if v > schemaVersion {
		return nil, fmt.Errorf("text cache has schema %d, this memcue knows %d: update memcue", v, schemaVersion)
	}
	if v > 0 && v < schemaVersion {
		if err := setAside(dir, cache, v); err != nil {
			return nil, fmt.Errorf("text history %s: %w", dir, err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(cache), 0o700); err != nil {
		return nil, err
	}
	if err := buildCache(dir, cache); err != nil {
		return nil, fmt.Errorf("text history %s: %w", dir, err)
	}
	s, err := openFile(cache)
	if err != nil {
		return nil, fmt.Errorf("open text cache %s: %w", cache, err)
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
	backup, err := legacyTextBackupPath(dir, cache)
	if err != nil {
		return err
	}
	dirs, err := textDirs(dir)
	if err != nil {
		return err
	}
	if err := stripLegacyText(dir, backup, dirs); err != nil {
		return err
	}
	if err := rescueVersions(dir, cache); err != nil {
		return err
	}
	// Link creation is exclusive: even a same-second retirement cannot replace
	// earlier rollback evidence. Keep the source until its saved name exists.
	base := fmt.Sprintf("%s.schema%d-%d", cache, v, time.Now().Unix())
	for n := 0; ; n++ {
		saved := base
		if n > 0 {
			saved = fmt.Sprintf("%s-%d", base, n)
		}
		if err := os.Link(cache, saved); errors.Is(err, os.ErrExist) {
			continue
		} else if err != nil {
			return err
		}
		return os.Remove(cache)
	}
}

// rescueVersions unions pending cache writes into the files, including divergent writes below the file tail.
// It reads every file rather than trusting a row-count stamp, which cannot prove
// that individual versions reached disk. Missing schema fields are projections,
// not independent writes. Read failures are returned before retiring the cache.
func rescueVersions(dir, cache string) error {
	r, err := OpenReadOnly(cache)
	if err != nil {
		return fmt.Errorf("rescue pending cache versions: %w", err)
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
		cached := byID[id]
		identified := false
		used := map[int]bool{}
		bind := func(i, j int) {
			g := file[j]
			if g.WriteID == "" && cached[i].WriteID != "" {
				g.WriteID = cached[i].WriteID
				file[j] = g
				identified = true
			}
			cached[i] = g
			used[j] = true
		}
		// A common sequence of complete available legacy data establishes a
		// projection, not a guessed match based only on a version number.
		prefix := 0
		for prefix < len(cached) && prefix < len(file) && sameCacheProjection(cached[prefix], file[prefix], r.absent, true) {
			bind(prefix, prefix)
			prefix++
		}
		missingLabels := false
		for name := range r.absent {
			if name != "write_id" {
				missingLabels = true
			}
		}
		for i := prefix; i < len(cached); i++ {
			var matches []int
			for j, g := range file {
				if !used[j] && sameCacheProjection(cached[i], g, r.absent, false) {
					matches = append(matches, j)
				}
			}
			if missingLabels && len(matches) > 1 {
				return fmt.Errorf("ambiguous legacy cache projection for %s: keep the old cache and reconcile its original history", id)
			}
			if len(matches) > 0 {
				bind(i, matches[0])
				continue
			}
			if missingLabels {
				// Losing labels can change occurrence classes. After divergence,
				// do not invent correspondence to an otherwise matching record.
				for j, g := range file {
					if !used[j] && sameCacheProjection(cached[i], g, r.absent, true) {
						return fmt.Errorf("ambiguous legacy cache occurrence for %s: keep the old cache and reconcile its original history", id)
					}
				}
			}
		}
		h := MergeHistories(file, cached)
		if h.Changed() || identified {
			if err := WriteFactFile(path, h.Versions); err != nil {
				return err
			}
		}
	}
	return nil
}

// cacheSchema is the schema version of the cache file at path as it is, 0 for a file that is not there.
func cacheSchema(path string) (int, error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return 0, nil
	} else if err != nil {
		return 0, err
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
	changed := map[string]int64{}
	for name, mtime := range dirs {
		if saved[name] != mtime {
			changed[name] = mtime
		}
	}
	backup, err := legacyTextBackupPath(s.text, strings.TrimSuffix(s.textLock, ".lock"))
	if err != nil {
		return err
	}
	if err := stripLegacyText(s.text, backup, changed); err != nil {
		return err
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

// stripLegacyText rewrites legacy lines under the text lock. Backups live outside the
// current history, keyed by the original bytes, so retries never overwrite evidence.
// Each replacement is atomic; failure is returned and the next open resumes the pass.
func stripLegacyText(dir, backup string, dirs map[string]int64) error {
	for name := range dirs {
		entries, err := os.ReadDir(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		for _, e := range entries {
			if e.IsDir() || strings.HasPrefix(e.Name(), ".") || !strings.HasSuffix(e.Name(), ".jsonl") {
				continue
			}
			path := filepath.Join(dir, name, e.Name())
			if e.Type()&fs.ModeSymlink != 0 {
				return fmt.Errorf("%s: a fact file must not be a symbolic link", path)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			legacy := false
			for _, line := range bytes.Split(data, []byte("\n")) {
				if len(bytes.TrimSpace(line)) == 0 {
					continue
				}
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(line, &fields); err != nil {
					return fmt.Errorf("%s: %w", path, err)
				}
				if _, ok := fields["keywords"]; ok {
					legacy = true
				}
				if _, ok := fields["queries"]; ok {
					legacy = true
				}
			}
			if !legacy {
				continue
			}
			facts, err := ParseFacts(data)
			if err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			if err := os.MkdirAll(backup, 0o700); err != nil {
				return err
			}
			saved := filepath.Join(backup, fmt.Sprintf("%x.jsonl", sha256.Sum256(data)))
			f, err := os.OpenFile(saved, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if errors.Is(err, os.ErrExist) {
				old, readErr := os.ReadFile(saved)
				if readErr != nil {
					return readErr
				}
				if !bytes.Equal(old, data) {
					return fmt.Errorf("legacy backup differs: %s", saved)
				}
			} else if err != nil {
				return err
			} else {
				_, writeErr := f.Write(data)
				if writeErr == nil {
					writeErr = f.Sync()
				}
				closeErr := f.Close()
				if writeErr != nil {
					_ = os.Remove(saved)
					return writeErr
				}
				if closeErr != nil {
					return closeErr
				}
			}
			if err := WriteFactFile(path, facts); err != nil {
				return err
			}
		}
	}
	return nil
}

// A caller may keep its cache inside the history. Never put old JSONL there:
// it would become current input on the next directory scan.
func legacyTextBackupPath(dir, cache string) (string, error) {
	history, err := physicalPath(dir)
	if err != nil {
		return "", err
	}
	backup, err := physicalPath(cache + ".schema7-text-backup")
	if err != nil {
		return "", err
	}
	inside := func(path string) bool {
		rel, err := filepath.Rel(history, path)
		return err != nil || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	}
	if inside(backup) {
		// Choose the sibling of the physical history, not of its lexical alias.
		backup, err = physicalPath(history + ".schema7-text-backup")
		if err != nil {
			return "", err
		}
	}
	if inside(backup) {
		return "", fmt.Errorf("legacy backup must be physically outside history: %s", backup)
	}
	return backup, nil
}

// Resolve existing components even when the destination's final components have
// not been created. Broken links and non-directory parents remain errors.
func physicalPath(path string) (string, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	var tail []string
	for {
		_, err := os.Lstat(path)
		if err == nil {
			resolved, err := filepath.EvalSymlinks(path)
			if err != nil {
				return "", err
			}
			for i := len(tail) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, tail[i])
			}
			return resolved, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		tail = append(tail, filepath.Base(path))
		parent := filepath.Dir(path)
		if parent == path {
			return "", err
		}
		path = parent
	}
}

// sameCacheProjection compares only fields the old cache could actually store.
func sameCacheProjection(cached, file Fact, absent map[string]string, occurrencesMayBeProjected bool) bool {
	base := func(id string) string { first, _, _ := strings.Cut(id, "."); return first }
	if file.WriteID == "" && (cached.WriteID == projectedWriteID(file, "", "") || occurrencesMayBeProjected && base(cached.WriteID) == projectedWriteID(file, "", "")) {
		file.WriteID = cached.WriteID
	}
	if occurrencesMayBeProjected && base(cached.WriteID) == base(file.WriteID) {
		file.WriteID = cached.WriteID
	}
	if _, missing := absent["expires"]; missing {
		file.Expires = ""
	}
	if _, missing := absent["kind"]; missing {
		file.Kind = ""
	}
	if _, missing := absent["scope"]; missing {
		file.Scope = ""
	}
	if _, missing := absent["source"]; missing {
		file.Source = ""
	}
	if _, missing := absent["evidence"]; missing {
		file.Evidence = ""
	}
	if _, missing := absent["review"]; missing {
		file.Review = ""
	}
	if _, missing := absent["prompt_version"]; missing {
		file.PromptVersion = 0
	}
	if _, missing := absent["channel"]; missing {
		file.Channel = ""
	}
	return same(cached, file)
}
