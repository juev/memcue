// Package store keeps the fact base: one SQLite file that is the only copy of the facts and is synced between
// machines by git.
//
// Rows are never deleted or rewritten. A fact is a chain of versions under one ULID: a correction adds a version, a
// removal adds a version with status retired. Two copies of the base therefore merge by taking the union of their
// (id, version) rows (see Merge). The FTS index over the latest active versions lives in the same file and is
// derived: Rebuild recreates it from the rows.
package store

import (
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/juev/memcue/internal/text"

	_ "modernc.org/sqlite"
)

// Bump when the schema or the way facts become stems changes; Open migrates or rebuilds the derived index.
// The expires column was added without a bump: every reader names its columns, so a base with it stays readable by an
// older memcue, and Open adds it to a base that predates it (ensureExpires).
const schemaVersion = 1

const schema = `
CREATE TABLE IF NOT EXISTS facts (
	id       TEXT NOT NULL,
	version  INTEGER NOT NULL,
	content  TEXT NOT NULL,
	keywords TEXT NOT NULL DEFAULT '',
	origin   TEXT NOT NULL DEFAULT '',
	created  TEXT NOT NULL,
	status   TEXT NOT NULL CHECK (status IN ('active', 'retired')),
	author   TEXT NOT NULL DEFAULT '',
	expires  TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (id, version)
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS fact_index (rid INTEGER PRIMARY KEY, id TEXT NOT NULL UNIQUE, stems TEXT NOT NULL);
CREATE VIRTUAL TABLE IF NOT EXISTS fact_fts USING fts5(stems, content='fact_index', content_rowid='rid',
	tokenize='unicode61 remove_diacritics 0');
`

// Status of a fact version.
const (
	Active  = "active"
	Retired = "retired"
)

// Fact is one version of a fact.
type Fact struct {
	ID       string
	Version  int
	Content  string
	Keywords []string
	Origin   string // where it was learnt: "<repository> <date>"
	Created  time.Time
	Status   string
	Author   string // machine that wrote the version
	Expires  string // last day the fact is worth recalling, "YYYY-MM-DD"; empty never expires
}

// ErrNotFound: no fact with that id.
var ErrNotFound = errors.New("fact not found")

// Store is an open fact base.
type Store struct {
	db     *sql.DB
	author string
	now    func() time.Time
}

// Open opens or creates the base at path. The rollback journal (not WAL) keeps the file whole between writes, so
// git never takes half of one.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(DELETE)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	host, _ := os.Hostname()
	s := &Store{db: db, author: host, now: time.Now}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := s.ensureExpires(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// ensureExpires adds the expires column to a base written before it existed. The column is additive: an older memcue
// reads the base as it did, because every query names the columns it wants.
func (s *Store) ensureExpires() error {
	rows, err := s.db.Query(`PRAGMA table_info(facts)`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var (
			cid, notnull, pk int
			name, typ        string
			dflt             any
		)
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return err
		}
		if name == "expires" {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = s.db.Exec(`ALTER TABLE facts ADD COLUMN expires TEXT NOT NULL DEFAULT ''`)
	return err
}

// Close closes the base.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	var v int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return err
	}
	if v == schemaVersion {
		return nil
	}
	if v > schemaVersion {
		return fmt.Errorf("fact base has schema %d, this memcue knows %d: update memcue", v, schemaVersion)
	}
	if _, err := s.db.Exec(schema); err != nil {
		return err
	}
	if err := s.Rebuild(); err != nil {
		return err
	}
	_, err := s.db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, schemaVersion))
	return err
}

// Add stores a new fact as version 1.
func (s *Store) Add(content string, keywords []string, origin string) (Fact, error) {
	return s.AddExpiring(content, keywords, origin, "")
}

// AddExpiring stores a new fact as version 1 with the last day it is worth recalling ("YYYY-MM-DD"; empty never
// expires).
func (s *Store) AddExpiring(content string, keywords []string, origin, expires string) (Fact, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return Fact{}, errors.New("empty fact")
	}
	if err := ValidExpiry(expires); err != nil {
		return Fact{}, err
	}
	id, err := ulid.New(ulid.Timestamp(s.now()), rand.Reader)
	if err != nil {
		return Fact{}, err
	}
	f := Fact{ID: id.String(), Version: 1, Content: content, Keywords: keywords, Origin: origin,
		Created: s.now().UTC(), Status: Active, Author: s.author, Expires: expires}
	return f, s.write(f)
}

// Correct adds a version with new content and keywords; the origin stays.
func (s *Store) Correct(id, content string, keywords []string) (Fact, error) {
	return s.CorrectExpiring(id, content, keywords, nil)
}

// CorrectExpiring adds a version with new content, keywords and optionally a new expiry: nil keeps the current one, a
// pointer to "" clears it.
func (s *Store) CorrectExpiring(id, content string, keywords []string, expires *string) (Fact, error) {
	cur, err := s.Get(id)
	if err != nil {
		return Fact{}, err
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return Fact{}, errors.New("empty fact")
	}
	f := cur
	f.Version, f.Content, f.Keywords, f.Created, f.Status, f.Author = cur.Version+1, content, keywords,
		s.now().UTC(), Active, s.author
	if expires != nil {
		if err := ValidExpiry(*expires); err != nil {
			return Fact{}, err
		}
		f.Expires = *expires
	}
	return f, s.write(f)
}

// ValidExpiry accepts "" (never expires) or a "YYYY-MM-DD" date.
func ValidExpiry(expires string) error {
	if expires == "" {
		return nil
	}
	if _, err := time.Parse(time.DateOnly, expires); err != nil {
		return fmt.Errorf("expiry %q is not a YYYY-MM-DD date", expires)
	}
	return nil
}

// Expired says whether a fact passed its last day by the given date ("YYYY-MM-DD"). An empty expiry never expires.
func Expired(expires, today string) bool {
	return expires != "" && today != "" && expires < today
}

// Retire adds a version that takes the fact out of recall; its content stays readable in the history.
func (s *Store) Retire(id string) (Fact, error) {
	cur, err := s.Get(id)
	if err != nil {
		return Fact{}, err
	}
	f := cur
	f.Version, f.Created, f.Status, f.Author = cur.Version+1, s.now().UTC(), Retired, s.author
	return f, s.write(f)
}

func (s *Store) write(f Fact) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`INSERT INTO facts(id, version, content, keywords, origin, created, status, author, expires)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, f.ID, f.Version, f.Content, strings.Join(f.Keywords, ", "), f.Origin,
		f.Created.Format(time.RFC3339Nano), f.Status, f.Author, f.Expires); err != nil {
		return err
	}
	if err := reindex(tx, f.ID); err != nil {
		return err
	}
	return tx.Commit()
}

// reindex puts the latest version of id into the FTS index, or takes it out when that version is retired.
func reindex(tx *sql.Tx, id string) error {
	if _, err := tx.Exec(`INSERT INTO fact_fts(fact_fts, rowid, stems) SELECT 'delete', rid, stems FROM fact_index
		WHERE id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM fact_index WHERE id = ?`, id); err != nil {
		return err
	}
	var content, keywords, status string
	err := tx.QueryRow(`SELECT content, keywords, status FROM facts WHERE id = ? ORDER BY version DESC LIMIT 1`, id).
		Scan(&content, &keywords, &status)
	if errors.Is(err, sql.ErrNoRows) || status == Retired {
		return nil
	}
	if err != nil {
		return err
	}
	stems := strings.Join(text.Stems(content+"\n"+keywords), " ")
	res, err := tx.Exec(`INSERT INTO fact_index(id, stems) VALUES (?, ?)`, id, stems)
	if err != nil {
		return err
	}
	rid, err := res.LastInsertId()
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO fact_fts(rowid, stems) VALUES (?, ?)`, rid, stems)
	return err
}

// Rebuild recreates the FTS index from the latest versions of all facts.
func (s *Store) Rebuild() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM fact_index`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO fact_fts(fact_fts) VALUES ('delete-all')`); err != nil {
		return err
	}
	rows, err := tx.Query(`SELECT DISTINCT id FROM facts`)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, id := range ids {
		if err := reindex(tx, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

const columns = `id, version, content, keywords, origin, created, status, author, expires`

func scan(row interface{ Scan(...any) error }) (Fact, error) {
	var f Fact
	var keywords, created string
	if err := row.Scan(&f.ID, &f.Version, &f.Content, &keywords, &f.Origin, &created, &f.Status, &f.Author,
		&f.Expires); err != nil {
		return Fact{}, err
	}
	if keywords != "" {
		f.Keywords = strings.Split(keywords, ", ")
	}
	f.Created, _ = time.Parse(time.RFC3339Nano, created)
	return f, nil
}

// Get returns the latest version of a fact, active or retired.
func (s *Store) Get(id string) (Fact, error) {
	f, err := scan(s.db.QueryRow(`SELECT `+columns+` FROM facts WHERE id = ? ORDER BY version DESC LIMIT 1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Fact{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return f, err
}

// History returns every version of a fact, oldest first.
func (s *Store) History(id string) ([]Fact, error) {
	return s.query(`SELECT `+columns+` FROM facts WHERE id = ? ORDER BY version`, id)
}

// Rows returns every version of every fact, ordered by id and version.
func (s *Store) Rows() ([]Fact, error) {
	return s.query(`SELECT ` + columns + ` FROM facts ORDER BY id, version`)
}

// Active returns the latest version of every fact that is not retired.
func (s *Store) Active() ([]Fact, error) {
	return s.query(`SELECT ` + qualified("f") + ` FROM facts f JOIN fact_index i ON i.id = f.id
		WHERE f.version = (SELECT max(version) FROM facts WHERE id = f.id) ORDER BY f.id`)
}

func qualified(alias string) string {
	cols := strings.Split(columns, ", ")
	for i, c := range cols {
		cols[i] = alias + "." + c
	}
	return strings.Join(cols, ", ")
}

func (s *Store) query(q string, args ...any) ([]Fact, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Fact
	for rows.Next() {
		f, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// Hit is an active fact found by Search, with the stems it was indexed by.
type Hit struct {
	Fact
	Stems []string
}

// Search returns up to limit active facts holding any of the terms, best BM25 first.
func (s *Store) Search(terms []string, limit int) ([]Hit, error) {
	if len(terms) == 0 {
		return nil, nil
	}
	q := make([]string, len(terms))
	for i, t := range terms {
		q[i] = text.Query(t)
	}
	rows, err := s.db.Query(`SELECT i.stems, `+qualified("f")+`
		FROM fact_fts JOIN fact_index i ON i.rid = fact_fts.rowid
		JOIN facts f ON f.id = i.id AND f.version = (SELECT max(version) FROM facts WHERE id = i.id)
		WHERE fact_fts MATCH ? ORDER BY bm25(fact_fts) LIMIT ?`, strings.Join(q, " OR "), limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Hit
	for rows.Next() {
		var h Hit
		var stems, keywords, created string
		if err := rows.Scan(&stems, &h.ID, &h.Version, &h.Content, &keywords, &h.Origin, &created, &h.Status,
			&h.Author, &h.Expires); err != nil {
			return nil, err
		}
		if keywords != "" {
			h.Keywords = strings.Split(keywords, ", ")
		}
		h.Created, _ = time.Parse(time.RFC3339Nano, created)
		h.Stems = strings.Fields(stems)
		out = append(out, h)
	}
	return out, rows.Err()
}

// DocFreq returns how many active facts hold each term, and the number of active facts.
func (s *Store) DocFreq(terms []string) (map[string]int, int, error) {
	df := make(map[string]int, len(terms))
	for _, t := range terms {
		var n int
		if err := s.db.QueryRow(`SELECT count(*) FROM fact_fts WHERE fact_fts MATCH ?`, text.Query(t)).Scan(&n); err != nil {
			return nil, 0, err
		}
		df[t] = n
	}
	var total int
	if err := s.db.QueryRow(`SELECT count(*) FROM fact_index`).Scan(&total); err != nil {
		return nil, 0, err
	}
	return df, total, nil
}
