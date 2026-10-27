//go:build research

package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/juev/memcue/internal/store"

	_ "modernc.org/sqlite"
)

// Row is one row of the facts table exactly as it is stored: the prototype text encoding is these sixteen columns,
// one JSON object per line, in this order. Nothing is parsed or normalised, so the round trip compares stored values.
type Row struct {
	ID            string `json:"id"`
	Version       int    `json:"version"`
	Content       string `json:"content"`
	Keywords      string `json:"keywords"`
	Origin        string `json:"origin"`
	Created       string `json:"created"`
	Status        string `json:"status"`
	Author        string `json:"author"`
	Expires       string `json:"expires"`
	Kind          string `json:"kind"`
	Scope         string `json:"scope"`
	Source        string `json:"source"`
	Evidence      string `json:"evidence"`
	Review        string `json:"review"`
	PromptVersion int    `json:"prompt_version"`
	Queries       string `json:"queries"`
}

const columns = `id, version, content, keywords, origin, created, status, author, expires, kind, scope, source, evidence,
	review, prompt_version, queries`

func openRaw(path string, readOnly bool) (*sql.DB, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: abs}
	u.RawQuery = "_pragma=busy_timeout(5000)&_pragma=journal_mode(DELETE)"
	if readOnly {
		u.RawQuery = "_pragma=busy_timeout(5000)&mode=ro"
	}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

// exportRows reads every version of every fact in one statement, which SQLite runs inside one read transaction: a
// consistent snapshot even while another process writes.
func exportRows(path string) ([]Row, error) {
	db, err := openRaw(path, true)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	return queryRows(db)
}

func queryRows(db *sql.DB) ([]Row, error) {
	rs, err := db.Query(`SELECT ` + columns + ` FROM facts ORDER BY id, version`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()
	var out []Row
	for rs.Next() {
		var r Row
		if err := rs.Scan(&r.ID, &r.Version, &r.Content, &r.Keywords, &r.Origin, &r.Created, &r.Status, &r.Author,
			&r.Expires, &r.Kind, &r.Scope, &r.Source, &r.Evidence, &r.Review, &r.PromptVersion, &r.Queries); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rs.Err()
}

func encodeLine(r Row) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(r) // a Row of strings and ints always encodes
	return b.Bytes()
}

// encodeJSONL writes rows sorted by id and version, one per line: the same base always gives the same bytes.
func encodeJSONL(rows []Row) []byte {
	sorted := append([]Row(nil), rows...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].ID != sorted[j].ID {
			return sorted[i].ID < sorted[j].ID
		}
		return sorted[i].Version < sorted[j].Version
	})
	var b bytes.Buffer
	for _, r := range sorted {
		b.Write(encodeLine(r))
	}
	return b.Bytes()
}

// decodeJSONL is strict: an unknown field, a missing line end or a duplicate (id, version) is an error, so a
// truncated or hand-merged file is refused instead of imported in part.
func decodeJSONL(data []byte) ([]Row, error) {
	if len(data) > 0 && data[len(data)-1] != '\n' {
		return nil, errors.New("jsonl: last line is not terminated (truncated file?)")
	}
	var out []Row
	seen := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for n := 1; sc.Scan(); n++ {
		dec := json.NewDecoder(bytes.NewReader(sc.Bytes()))
		dec.DisallowUnknownFields()
		var r Row
		if err := dec.Decode(&r); err != nil {
			return nil, fmt.Errorf("jsonl line %d: %w", n, err)
		}
		key := fmt.Sprintf("%s/%d", r.ID, r.Version)
		if seen[key] {
			return nil, fmt.Errorf("jsonl line %d: duplicate version %s", n, key)
		}
		seen[key] = true
		out = append(out, r)
	}
	return out, sc.Err()
}

// importRows builds a base from rows at dst: a new file next to it gets the schema from store.Open, the rows in one
// transaction, and its search index from a second store.Open (the index check sees no index rows and rebuilds). Only
// a complete file is renamed over dst, so an interrupted import leaves dst as it was and a stale .importing file that
// the next import removes.
func importRows(rows []Row, dst string) error {
	tmp := dst + ".importing"
	for _, p := range []string{tmp, tmp + "-journal"} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	s, err := store.Open(tmp)
	if err != nil {
		return err
	}
	if err := s.Close(); err != nil {
		return err
	}
	db, err := openRaw(tmp, false)
	if err != nil {
		return err
	}
	if err := insertRows(db, rows); err != nil {
		_ = db.Close()
		return err
	}
	if err := db.Close(); err != nil {
		return err
	}
	if s, err = store.Open(tmp); err != nil {
		return err
	}
	if err := s.Close(); err != nil {
		return err
	}
	if err := syncFile(tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return err
	}
	return syncFile(filepath.Dir(dst))
}

func insertRows(db *sql.DB, rows []Row) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	st, err := tx.Prepare(`INSERT INTO facts(` + columns + `) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if _, err := st.Exec(r.ID, r.Version, r.Content, r.Keywords, r.Origin, r.Created, r.Status, r.Author, r.Expires,
			r.Kind, r.Scope, r.Source, r.Evidence, r.Review, r.PromptVersion, r.Queries); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func syncFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// The event log is the change-oriented prototype: every fact version is an immutable file named by the hash of its
// bytes, events/<last two chars of the id>/<id>/<hash>.json, holding the row without its version number and the hash
// of the event it was written on top of. Two machines only ever add files with distinct names, so git merges the
// directory without conflicts; version numbers are derived (see materialize).
type Event struct {
	Parent string `json:"parent"`
	Row    Row    `json:"row"`
}

func eventBytes(e Event) []byte {
	e.Row.Version = 0
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(e)
	return b.Bytes()
}

func eventHash(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])[:32]
}

func eventPath(root, id, hash string) string {
	return filepath.Join(root, "events", id[len(id)-2:], id, hash+".json")
}

// EventMap ties the versions of a derived base to their events: "id/version" -> hash. It is local state, rebuilt by
// every materialize, and it is how record tells the versions this machine wrote since then from the ones it got.
type EventMap map[string]string

func mapKey(id string, version int) string { return fmt.Sprintf("%s/%d", id, version) }

// record writes an event for every row of the base that the map does not know, parent being the event of the version
// before it, and returns how many it wrote. A version a machine writes is always the latest, so its parent is the
// version it saw as current.
func record(root string, rows []Row, m EventMap) (int, error) {
	n := 0
	for _, r := range rows { // ordered by id, version
		key := mapKey(r.ID, r.Version)
		if _, ok := m[key]; ok {
			continue
		}
		parent := ""
		if r.Version > 1 {
			p, ok := m[mapKey(r.ID, r.Version-1)]
			if !ok {
				return n, fmt.Errorf("record %s: version %d has no event", r.ID, r.Version-1)
			}
			parent = p
		}
		b := eventBytes(Event{Parent: parent, Row: r})
		h := eventHash(b)
		p := eventPath(root, r.ID, h)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return n, err
		}
		if err := os.WriteFile(p, b, 0o600); err != nil {
			return n, err
		}
		m[key] = h
		n++
	}
	return n, nil
}

// loadEvents reads every event file under root/events and checks that its name is the hash of its bytes.
func loadEvents(root string) (map[string]Event, error) {
	out := map[string]Event{}
	err := filepath.WalkDir(filepath.Join(root, "events"), func(p string, d os.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".json") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		h := strings.TrimSuffix(filepath.Base(p), ".json")
		if eventHash(b) != h {
			return fmt.Errorf("event %s: content does not match its name", p)
		}
		var e Event
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&e); err != nil {
			return fmt.Errorf("event %s: %w", p, err)
		}
		out[h] = e
		return nil
	})
	return out, err
}

// materialize derives the rows of a base from the events: per fact, versions in causal order (an event after its
// parent), and among the events whose parent is placed, the earliest by creation time, then author, then content, then
// hash. On two lines of changes that diverged from a common version this is the order store.Merge gives their tails;
// it is a prototype rule, checked against Merge in the semantics run, not a proof.
func materialize(events map[string]Event) ([]Row, EventMap, error) {
	byID := map[string][]string{}
	for h, e := range events {
		byID[e.Row.ID] = append(byID[e.Row.ID], h)
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	m := EventMap{}
	var rows []Row
	for _, id := range ids {
		children := map[string][]string{}
		for _, h := range byID[id] {
			p := events[h].Parent
			if p != "" {
				if _, ok := events[p]; !ok {
					return nil, nil, fmt.Errorf("fact %s: event %s has a missing parent", id, h)
				}
			}
			children[p] = append(children[p], h)
		}
		ready := append([]string(nil), children[""]...)
		v := 0
		for len(ready) > 0 {
			sort.Slice(ready, func(i, j int) bool { return less(events[ready[i]], ready[i], events[ready[j]], ready[j]) })
			h := ready[0]
			ready = append(ready[1:], children[h]...)
			v++
			r := events[h].Row
			r.Version = v
			rows = append(rows, r)
			m[mapKey(id, v)] = h
		}
		if v != len(byID[id]) {
			return nil, nil, fmt.Errorf("fact %s: %d of %d events reachable (cycle?)", id, v, len(byID[id]))
		}
	}
	return rows, m, nil
}

func less(a Event, ah string, b Event, bh string) bool {
	// RFC3339Nano drops trailing zeros of the fraction, so the stored text does not sort as time: parse it, as Merge
	// does through scan.
	ta, _ := time.Parse(time.RFC3339Nano, a.Row.Created)
	tb, _ := time.Parse(time.RFC3339Nano, b.Row.Created)
	if !ta.Equal(tb) {
		return ta.Before(tb)
	}
	if a.Row.Author != b.Row.Author {
		return a.Row.Author < b.Row.Author
	}
	if a.Row.Content != b.Row.Content {
		return a.Row.Content < b.Row.Content
	}
	return ah < bh
}

func saveMap(path string, m EventMap) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

func loadMap(path string) (EventMap, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	m := EventMap{}
	return m, json.Unmarshal(b, &m)
}
