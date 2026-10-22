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
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/juev/memcue/internal/text"

	_ "modernc.org/sqlite"
)

// Bump when the schema or the way facts become stems changes; Open migrates or rebuilds the derived index.
// Version 5 adds the search queries of a fact (queries) to what the index holds. An older memcue must refuse such a
// base: its merge and its writes would drop the queries, and its rebuild would leave them out of the index.
// Version 4 adds fact_vectors: the optional vectors of the active facts, one per model tag, kept next to the facts
// because a laptop without an embeddings endpoint simply has none of them (memcue embed, removed in #250).
// Version 3 changes the stems again: a versioned name such as PostgreSQL-16 keeps its word and only the number is
// dropped, while an uppercase ticket key such as PROJ-1234 stays one exact term. Opening an older base rebuilds the
// derived index once. Version 2 first made ticket keys exact and added the columns after version 1 (expires, kind,
// scope, source, evidence, review, prompt_version); an older memcue refuses a newer base instead of reading
// candidates as active facts.
const schemaVersion = 5

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
	kind     TEXT NOT NULL DEFAULT '',
	scope    TEXT NOT NULL DEFAULT '',
	source   TEXT NOT NULL DEFAULT '',
	evidence TEXT NOT NULL DEFAULT '',
	review   TEXT NOT NULL DEFAULT '',
	prompt_version INTEGER NOT NULL DEFAULT 0,
	queries  TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (id, version)
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS fact_index (rid INTEGER PRIMARY KEY, id TEXT NOT NULL UNIQUE, stems TEXT NOT NULL);
CREATE VIRTUAL TABLE IF NOT EXISTS fact_fts USING fts5(stems, content='fact_index', content_rowid='rid',
	tokenize='unicode61 remove_diacritics 0');
CREATE TABLE IF NOT EXISTS fact_vectors (
	id        TEXT NOT NULL,
	tag       TEXT NOT NULL,
	dim       INTEGER NOT NULL,
	text_hash TEXT NOT NULL,
	vec       BLOB NOT NULL,
	PRIMARY KEY (id, tag)
) WITHOUT ROWID;
`

// indexDDL holds the add-only indexes. Open runs it on every base, not only when the schema version changes, so a
// base created by an older memcue gets an index added without a version bump and without a full Rebuild.
const indexDDL = `
CREATE INDEX IF NOT EXISTS facts_created ON facts(created);
`

// Status of a fact version.
const (
	Active  = "active"
	Retired = "retired"
)

// Candidate is the review state of an active fact that waits for the user's approval: it is stored but not indexed,
// so recall never gives it.
const (
	Candidate   = "candidate"   // extracted, waits for a decision, not recalled
	Provisional = "provisional" // auto-approved for a TTL, recalled after approved facts
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
	// Queries are the words and short phrases a user would ask about the fact with, written by the main model; the
	// index holds them next to the content and the keywords. Empty means the fact has not been expanded yet.
	Queries []string
	Meta
}

// Meta is what a fact is and what it rests on; every field may be empty, as in facts written before it existed.
type Meta struct {
	Kind          string // rule, decision, pitfall or environment
	Scope         string // where it holds: global, host:<name>, repo:<name>
	Source        string // who said it: user, user_confirmed, tool, agent, note, memory
	Evidence      string // the words of the source the fact rests on, verbatim
	Review        string // Candidate while it waits, Provisional for the auto TTL, empty when approved
	PromptVersion int    // version of the note prompt that produced a migrated fact; 0 for other facts
}

// FactInput is one new fact for AddBatch: the same fields AddWith takes, without the generated id, author or time.
type FactInput struct {
	Content  string
	Keywords []string
	Origin   string
	Expires  string
	Queries  []string
	Meta
}

// ErrNotFound: no fact with that id.
var ErrNotFound = errors.New("fact not found")

// ErrNotCandidate: the fact is not waiting for approval (it is active already, or retired).
var ErrNotCandidate = errors.New("fact is not a candidate")

// ErrRetired: the fact was retired; a correction cannot bring it back.
var ErrRetired = errors.New("fact is retired")

// ErrAlreadyCandidate: undo would put a fact back into review when it is already there.
var ErrAlreadyCandidate = errors.New("fact is already a candidate")

// ErrChanged is returned when a fact got a new version after the caller read it.
var ErrChanged = errors.New("fact changed since it was read")

// Store is an open fact base.
type Store struct {
	db     *sql.DB
	author string
	now    func() time.Time
	// absent are the columns an older base read without migration lacks, with the value they read as; Open adds
	// them, so it is empty for a writable store.
	absent map[string]string
}

// Open opens or creates the base at path. The rollback journal (not WAL) keeps the file whole between writes, so
// git never takes half of one.
func Open(path string) (*Store, error) {
	dsn, file, err := dataSource(path)
	if err != nil {
		return nil, err
	}
	if file != "" {
		if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
			return nil, err
		}
		if err := privateFile(file); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", dsn)
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
	if err := s.ensureColumns(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := s.ensureIndexes(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := s.ensureIndex(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// OpenReadOnly opens an existing base for reading without creating it, tightening its mode, migrating it or
// rebuilding its index. It is for the git textconv: a historical file must not be changed by a diff. A missing file,
// a file that is not a memcue base, and a base from a newer memcue are errors. An older base is read as it is, the
// columns added since it was written reading as their defaults.
func OpenReadOnly(path string) (*Store, error) {
	dsn, _, err := readOnlyDataSource(path)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open %s read-only: %w", path, err)
	}
	if version == 0 {
		_ = db.Close()
		return nil, fmt.Errorf("%s is not a memcue fact base", path)
	}
	if version > schemaVersion {
		_ = db.Close()
		return nil, fmt.Errorf("fact base has schema %d, this memcue knows %d: update memcue", version, schemaVersion)
	}
	have, err := factColumns(db)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open %s read-only: %w", path, err)
	}
	absent := map[string]string{}
	for _, c := range added {
		if !have[c.name] {
			absent[c.name] = c.def
		}
	}
	host, _ := os.Hostname()
	return &Store{db: db, author: host, now: time.Now, absent: absent}, nil
}

// dataSource returns the SQLite data source for path and the file it names, or "" for an in-memory base. A file path
// is made absolute and escaped as a URI, so characters that are special in a URI stay part of the name. A "file:" URI
// is refused: a base must be a path this process can own and tighten, and a half-parsed URI used to create a directory
// named "file:" in the working directory.
func dataSource(path string) (dsn, file string, err error) {
	return sqliteSource(path, false)
}

// readOnlyDataSource is dataSource with mode=ro: the file must exist and SQLite must not write to it.
func readOnlyDataSource(path string) (dsn, file string, err error) {
	return sqliteSource(path, true)
}

func sqliteSource(path string, readOnly bool) (dsn, file string, err error) {
	switch {
	case path == "":
		return "", "", errors.New("store: empty base path")
	case path == ":memory:":
		if readOnly {
			return "", "", errors.New("store: a read-only base must be a file")
		}
		return "file::memory:?_pragma=busy_timeout(5000)&_pragma=journal_mode(DELETE)", "", nil
	case strings.HasPrefix(path, "file:"):
		return "", "", fmt.Errorf("store: %q is a URI: give the base as a file path", path)
	}
	file, err = filepath.Abs(path)
	if err != nil {
		return "", "", err
	}
	u := url.URL{Scheme: "file", Path: file}
	if readOnly {
		u.RawQuery = "_pragma=busy_timeout(5000)&mode=ro"
	} else {
		u.RawQuery = "_pragma=busy_timeout(5000)&_pragma=journal_mode(DELETE)"
	}
	return u.String(), file, nil
}

// privateFile creates the base with the mode of ~/.claude/memory (0600) and tightens a base written by an older
// memcue as 0644. The file may carry facts and evidence, so it must not be world-readable even inside a private
// directory.
func privateFile(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

// addedColumn is a column a base may predate. Each is additive: an older memcue reads the base as it did, because
// every query names the columns it wants.
type addedColumn struct {
	name, typ, def string
}

var added = []addedColumn{
	{"expires", "TEXT", "''"},
	{"kind", "TEXT", "''"},
	{"scope", "TEXT", "''"},
	{"source", "TEXT", "''"},
	{"evidence", "TEXT", "''"},
	{"review", "TEXT", "''"},
	{"prompt_version", "INTEGER", "0"},
	{"queries", "TEXT", "''"},
}

// factColumns names the columns the facts table of db has.
func factColumns(db *sql.DB) (map[string]bool, error) {
	rows, err := db.Query(`PRAGMA table_info(facts)`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	have := map[string]bool{}
	for rows.Next() {
		var (
			cid, notnull, pk int
			name, typ        string
			dflt             any
		)
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return nil, err
		}
		have[name] = true
	}
	return have, rows.Err()
}

// ensureColumns adds to a base the columns written after it was created.
func (s *Store) ensureColumns() error {
	have, err := factColumns(s.db)
	if err != nil {
		return err
	}
	for _, c := range added {
		if !have[c.name] {
			if _, err := s.db.Exec(`ALTER TABLE facts ADD COLUMN ` + c.name + ` ` + c.typ + ` NOT NULL DEFAULT ` + c.def); err != nil {
				return err
			}
		}
	}
	return nil
}

// ensureIndexes adds the indexes an older base may lack. Every statement is idempotent, so this runs on each Open;
// it deliberately does not depend on the schema version, which only changes when the table layout or the stems do.
func (s *Store) ensureIndexes() error {
	_, err := s.db.Exec(indexDDL)
	return err
}

// ensureIndex repairs an index that no longer matches the active facts. The index is derived and the only thing
// Search reads, so a base whose index row was lost must get it back before a prompt is answered.
func (s *Store) ensureIndex() error {
	const active = `SELECT count(*) FROM facts f
		WHERE f.version = (SELECT max(version) FROM facts WHERE id = f.id) AND f.status = ?
			AND f.review IN ('', 'provisional') `
	var facts, indexed, fts, missing, stale int
	if err := s.db.QueryRow(active, Active).Scan(&facts); err != nil {
		return err
	}
	if err := s.db.QueryRow(`SELECT count(*) FROM fact_index`).Scan(&indexed); err != nil {
		return err
	}
	if err := s.db.QueryRow(`SELECT count(*) FROM fact_fts`).Scan(&fts); err != nil {
		return err
	}
	if facts == indexed && indexed == fts {
		if err := s.db.QueryRow(active+`AND NOT EXISTS (SELECT 1 FROM fact_index i WHERE i.id = f.id)`, Active).
			Scan(&missing); err != nil {
			return err
		}
		if err := s.db.QueryRow(`SELECT count(*) FROM fact_index i WHERE NOT EXISTS (
			SELECT 1 FROM facts f WHERE f.id = i.id AND f.version = (SELECT max(version) FROM facts WHERE id = f.id)
			AND f.status = ? AND f.review IN ('', 'provisional'))`, Active).Scan(&stale); err != nil {
			return err
		}
		if missing == 0 && stale == 0 {
			return nil
		}
	}
	return s.Rebuild()
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
	// An old base may lack columns the rebuild reads (review, prompt_version), so add them before touching the index.
	if err := s.ensureColumns(); err != nil {
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
	return s.AddWith(content, keywords, origin, expires, Meta{})
}

// AddWith stores a new fact as version 1 with what it is and what it rests on. A fact with Review Candidate is not
// recalled until it is approved.
func (s *Store) AddWith(content string, keywords []string, origin, expires string, m Meta) (Fact, error) {
	facts, err := s.AddBatch([]FactInput{{
		Content: content, Keywords: keywords, Origin: origin, Expires: expires, Meta: m,
	}})
	if err != nil {
		return Fact{}, err
	}
	return facts[0], nil
}

// entropy makes the random part of a ULID increase, so facts written in the same millisecond still order by creation.
// The source is locked: ulid.Monotonic is not safe for concurrent use, and AddBatch generates its ids before the
// transaction, outside the single connection's serialization. A base written without it can hold same-millisecond
// facts in random order, so the readers that mean "newest" also order by created.
var entropy io.Reader = &ulid.LockedMonotonicReader{MonotonicReader: ulid.Monotonic(rand.Reader, 0)}

// AddBatch stores several new facts in one transaction: either every fact is written and indexed, or none is. A
// retry after a database error therefore sees the same base, not a half-applied extraction.
func (s *Store) AddBatch(inputs []FactInput) ([]Fact, error) {
	facts := make([]Fact, 0, len(inputs))
	for _, in := range inputs {
		content := strings.TrimSpace(in.Content)
		if content == "" {
			return nil, errors.New("empty fact")
		}
		if err := ValidExpiry(in.Expires); err != nil {
			return nil, err
		}
		id, err := ulid.New(ulid.Timestamp(s.now()), entropy)
		if err != nil {
			return nil, err
		}
		facts = append(facts, Fact{ID: id.String(), Version: 1, Content: content, Keywords: in.Keywords,
			Origin: in.Origin, Created: s.now().UTC(), Status: Active, Author: s.author, Expires: in.Expires,
			Queries: in.Queries, Meta: in.Meta})
	}
	if len(facts) == 0 {
		return facts, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	for _, f := range facts {
		if err := insert(tx, f); err != nil {
			return nil, err
		}
		if err := reindex(tx, f.ID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return facts, nil
}

// Correct adds a version with new content and keywords; the origin stays.
func (s *Store) Correct(id, content string, keywords []string) (Fact, error) {
	return s.CorrectExpiring(id, content, keywords, nil)
}

// CorrectExpiring adds a version with new content, keywords and optionally a new expiry: nil keeps the current one, a
// pointer to "" clears it. A retired fact cannot be corrected: retirement is final, and bringing it back would undo a
// decision (a vet verdict, a review rejection or an explicit retire) without review. Add a new fact instead.
func (s *Store) CorrectExpiring(id, content string, keywords []string, expires *string) (Fact, error) {
	cur, err := s.Get(id)
	if err != nil {
		return Fact{}, err
	}
	if cur.Status == Retired {
		return Fact{}, fmt.Errorf("%w: %s; add a new fact instead", ErrRetired, id)
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return Fact{}, errors.New("empty fact")
	}
	f := cur
	f.Version, f.Content, f.Keywords, f.Created, f.Status, f.Author = cur.Version+1, content, keywords,
		s.now().UTC(), Active, s.author
	if content != cur.Content {
		f.Queries = nil // they were written for the old text; the next dream writes them again
	}
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

// Today returns the local calendar date ("YYYY-MM-DD") that expiries and the provisional TTL are judged by. An expiry
// is a day on the user's calendar, not a UTC instant: a fact that expires "today" is still recalled this evening in
// every time zone.
func Today() string { return TodayAt(time.Now()) }

// TodayAt returns the calendar date of t in t's own location. It is split from Today for tests that pin a time zone.
func TodayAt(t time.Time) string { return t.Format(time.DateOnly) }

// Approve adds a version that takes a candidate or provisional fact into recall. A fact that is neither is left
// alone: approving an active fact again, or bringing a retired one back, is not what an approval means.
func (s *Store) Approve(id string) (Fact, error) {
	return s.ApproveExpiring(id, "")
}

// ApproveExpiring is Approve with a last day the fact is worth recalling, decided at approval time; an empty expires
// keeps the candidate's own, while approving a provisional fact clears its TTL. It writes one version, not an approval
// followed by a correction.
func (s *Store) ApproveExpiring(id, expires string) (Fact, error) {
	cur, err := s.Get(id)
	if err != nil {
		return Fact{}, err
	}
	if cur.Status != Active || (cur.Review != Candidate && cur.Review != Provisional) {
		return Fact{}, fmt.Errorf("%w: %s is %s", ErrNotCandidate, id, state(cur))
	}
	if err := ValidExpiry(expires); err != nil {
		return Fact{}, err
	}
	f := cur
	if expires != "" {
		f.Expires = expires
	} else if cur.Review == Provisional {
		f.Expires = "" // the TTL belonged to the provisional state, not to a human approval
	}
	f.Version, f.Created, f.Author, f.Review = cur.Version+1, s.now().UTC(), s.author, ""
	return f, s.write(f)
}

// ApproveProvisional records an unsure candidate as provisional: recalled after approved facts, until expires, and
// not part of the trusted base for duplicate or coverage rules. It writes one version.
func (s *Store) ApproveProvisional(id, expires string) (Fact, error) {
	cur, err := s.Get(id)
	if err != nil {
		return Fact{}, err
	}
	if cur.Status != Active || cur.Review != Candidate {
		return Fact{}, fmt.Errorf("%w: %s is %s", ErrNotCandidate, id, state(cur))
	}
	if err := ValidExpiry(expires); err != nil {
		return Fact{}, err
	}
	f := cur
	if expires != "" {
		f.Expires = expires
	}
	f.Version, f.Created, f.Author, f.Review = cur.Version+1, s.now().UTC(), s.author, Provisional
	return f, s.write(f)
}

func state(f Fact) string {
	if f.Status == Active && f.Review != "" {
		return f.Review
	}
	return f.Status
}

// Candidates returns the latest version of every fact that waits for approval, newest first. Created comes first
// because a base written before the monotonic ids can hold same-millisecond facts in random id order.
func (s *Store) Candidates() ([]Fact, error) {
	return s.query(`SELECT `+s.cols("f")+` FROM facts f
		WHERE f.version = (SELECT max(version) FROM facts WHERE id = f.id) AND f.status = ? AND f.review = ?
		ORDER BY f.created DESC, f.id DESC`, Active, Candidate)
}

// CardFacts returns the facts a session-start card carries: global rules and the rules and decisions of the given
// repository, the ones the user stated first, then newest first, skipping facts whose expiry has passed. A stated rule
// goes first because the date says little on a migrated base, where most rules share the day of the migration (#229).
// It is a view of the active facts, not a second store: a fact appears only while it is active and unexpired. today
// is a local "YYYY-MM-DD" date; empty means today.
func (s *Store) CardFacts(tier, today string) ([]Fact, error) {
	if today == "" {
		today = Today()
	}
	active, err := s.Active()
	if err != nil {
		return nil, err
	}
	var out []Fact
	for _, f := range active {
		if Expired(f.Expires, today) {
			continue
		}
		switch {
		case f.Kind == "rule" && strings.EqualFold(f.Scope, "global"):
		case (f.Kind == "decision" || f.Kind == "rule") && tier != "" && strings.EqualFold(f.Scope, "repo:"+tier):
		default:
			continue
		}
		out = append(out, f)
	}
	stated := func(f Fact) bool { return f.Source == "user" || f.Source == "user_confirmed" }
	sort.SliceStable(out, func(i, j int) bool {
		if si, sj := stated(out[i]), stated(out[j]); si != sj {
			return si
		}
		if !out[i].Created.Equal(out[j].Created) {
			return out[i].Created.After(out[j].Created)
		}
		return out[i].ID > out[j].ID
	})
	return out, nil
}

// Reopen adds a version that puts a fact back into review: the undo of an approval or a rejection. The content and
// its metadata stay; only the review state changes, so the history keeps both decisions.
func (s *Store) Reopen(id string) (Fact, error) {
	cur, err := s.Get(id)
	if err != nil {
		return Fact{}, err
	}
	if cur.Status == Active && cur.Review == Candidate {
		return Fact{}, fmt.Errorf("%w: %s", ErrAlreadyCandidate, id)
	}
	f := cur
	f.Version, f.Created, f.Author, f.Status, f.Review = cur.Version+1, s.now().UTC(), s.author, Active, Candidate
	return f, s.write(f)
}

// Relabel adds a version of an active fact with the given kind, scope and expiry; the content and everything else
// stay, the creation time too: a label does not make an old fact new, which would move it ahead of recent facts in the
// card. version is the version the caller decided on: a fact edited since is refused with ErrChanged, so a label
// never overwrites a later edit.
func (s *Store) Relabel(id string, version int, kind, scope, expires string) (Fact, error) {
	cur, err := s.Get(id)
	if err != nil {
		return Fact{}, err
	}
	if cur.Version != version {
		return Fact{}, fmt.Errorf("%w: %s is at version %d, not %d", ErrChanged, id, cur.Version, version)
	}
	if cur.Status == Retired {
		return Fact{}, fmt.Errorf("%w: %s", ErrRetired, id)
	}
	if err := ValidExpiry(expires); err != nil {
		return Fact{}, err
	}
	f := cur
	f.Version, f.Author, f.Kind, f.Scope, f.Expires = cur.Version+1, s.author, kind, scope, expires
	return f, s.write(f)
}

// SetQueries adds a version of a fact with the search queries the main model wrote for it; the content and everything
// else stay. Like Relabel, it refuses a fact edited since version (ErrChanged) and a retired fact.
func (s *Store) SetQueries(id string, version int, queries []string) (Fact, error) {
	cur, err := s.Get(id)
	if err != nil {
		return Fact{}, err
	}
	if cur.Version != version {
		return Fact{}, fmt.Errorf("%w: %s is at version %d, not %d", ErrChanged, id, cur.Version, version)
	}
	if cur.Status == Retired {
		return Fact{}, fmt.Errorf("%w: %s", ErrRetired, id)
	}
	f := cur
	f.Version, f.Author, f.Queries = cur.Version+1, s.author, queries
	return f, s.write(f)
}

// ErrNotRetired is returned when a fact to restore is not retired.
var ErrNotRetired = errors.New("fact is not retired")

// Restore adds a version that brings a retired fact back with the review state it had: an approved fact is recalled
// again, a rejected candidate waits for review again. The content and labels stay; it is the undo of a retire made by
// a person, a vet verdict or a dream.
func (s *Store) Restore(id string) (Fact, error) {
	cur, err := s.Get(id)
	if err != nil {
		return Fact{}, err
	}
	if cur.Status != Retired {
		return Fact{}, fmt.Errorf("%w: %s is %s", ErrNotRetired, id, state(cur))
	}
	f := cur
	f.Version, f.Created, f.Author, f.Status = cur.Version+1, s.now().UTC(), s.author, Active
	return f, s.write(f)
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

// RetireCandidate retires a fact while it still waits for approval or is provisional. Unlike Retire, it refuses an
// approval that a human or another run made in the meantime, so a stale vet decision cannot undo it.
func (s *Store) RetireCandidate(id string) (Fact, error) {
	cur, err := s.Get(id)
	if err != nil {
		return Fact{}, err
	}
	if cur.Status != Active || (cur.Review != Candidate && cur.Review != Provisional) {
		return Fact{}, fmt.Errorf("%w: %s is %s", ErrNotCandidate, id, state(cur))
	}
	f := cur
	f.Version, f.Created, f.Status, f.Author = cur.Version+1, s.now().UTC(), Retired, s.author
	return f, s.write(f)
}

// SplitCandidate replaces a candidate that holds several claims with one candidate per part, in one transaction: the
// original is retired and every part keeps its origin, expiry and meta. The keywords stay with the original, since
// they were chosen for the whole text. Like RetireCandidate, it refuses a fact that is no longer a candidate.
func (s *Store) SplitCandidate(id string, parts []string) ([]Fact, error) {
	cur, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	if cur.Status != Active || cur.Review != Candidate {
		return nil, fmt.Errorf("%w: %s is %s", ErrNotCandidate, id, state(cur))
	}
	return s.split(cur, parts)
}

// SplitFact does for a fact in recall what SplitCandidate does for a candidate: the parts take its place with its
// origin, expiry and meta, so an approved fact gives approved parts and a provisional one provisional parts. It refuses
// a fact that got a new version since the caller read it (ErrChanged), a retired fact and a candidate, which vet
// splits.
func (s *Store) SplitFact(id string, version int, parts []string) ([]Fact, error) {
	cur, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	switch {
	case cur.Version != version:
		return nil, fmt.Errorf("%w: %s is at version %d, not %d", ErrChanged, id, cur.Version, version)
	case cur.Status != Active:
		return nil, fmt.Errorf("%w: %s", ErrRetired, id)
	case cur.Review == Candidate:
		return nil, fmt.Errorf("%w: %s", ErrAlreadyCandidate, id)
	}
	return s.split(cur, parts)
}

// split retires cur and writes its parts in one transaction.
func (s *Store) split(cur Fact, parts []string) ([]Fact, error) {
	if len(parts) < 2 {
		return nil, fmt.Errorf("split %s: %d part(s), want at least two", cur.ID, len(parts))
	}
	retired := cur
	retired.Version, retired.Created, retired.Status, retired.Author = cur.Version+1, s.now().UTC(), Retired, s.author
	out := make([]Fact, 0, len(parts))
	for _, p := range parts {
		content := strings.TrimSpace(p)
		if content == "" {
			return nil, fmt.Errorf("split %s: empty part", cur.ID)
		}
		pid, err := ulid.New(ulid.Timestamp(s.now()), entropy)
		if err != nil {
			return nil, err
		}
		out = append(out, Fact{ID: pid.String(), Version: 1, Content: content, Origin: cur.Origin,
			Created: s.now().UTC(), Status: Active, Author: s.author, Expires: cur.Expires, Meta: cur.Meta})
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	for _, f := range append([]Fact{retired}, out...) {
		if err := insert(tx, f); err != nil {
			return nil, err
		}
		if err := reindex(tx, f.ID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) write(f Fact) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := insert(tx, f); err != nil {
		return err
	}
	if err := reindex(tx, f.ID); err != nil {
		return err
	}
	return tx.Commit()
}

// insert writes one version as it is.
func insert(tx *sql.Tx, f Fact) error {
	_, err := tx.Exec(`INSERT INTO facts(`+columns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, f.ID, f.Version,
		f.Content, strings.Join(f.Keywords, ", "), f.Origin, f.Created.Format(time.RFC3339Nano), f.Status, f.Author,
		f.Expires, f.Kind, f.Scope, f.Source, f.Evidence, f.Review, f.PromptVersion, strings.Join(f.Queries, ", "))
	return err
}

// reindex puts the latest version of id into the FTS index, or takes it out when that version is retired or a
// candidate.
func reindex(tx *sql.Tx, id string) error {
	if _, err := tx.Exec(`INSERT INTO fact_fts(fact_fts, rowid, stems) SELECT 'delete', rid, stems FROM fact_index
		WHERE id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM fact_index WHERE id = ?`, id); err != nil {
		return err
	}
	var content, keywords, queries, status, review string
	err := tx.QueryRow(`SELECT content, keywords, queries, status, review FROM facts WHERE id = ? ORDER BY version DESC
		LIMIT 1`, id).Scan(&content, &keywords, &queries, &status, &review)
	if errors.Is(err, sql.ErrNoRows) || status == Retired || review == Candidate {
		return nil
	}
	if err != nil {
		return err
	}
	stems := strings.Join(text.Stems(content+"\n"+keywords+"\n"+queries), " ")
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

const columns = `id, version, content, keywords, origin, created, status, author, expires, kind, scope, source, evidence, review, prompt_version, queries`

func scan(row interface{ Scan(...any) error }) (Fact, error) {
	var f Fact
	var keywords, created, queries string
	if err := row.Scan(&f.ID, &f.Version, &f.Content, &keywords, &f.Origin, &created, &f.Status, &f.Author,
		&f.Expires, &f.Kind, &f.Scope, &f.Source, &f.Evidence, &f.Review, &f.PromptVersion, &queries); err != nil {
		return Fact{}, err
	}
	if keywords != "" {
		f.Keywords = strings.Split(keywords, ", ")
	}
	if queries != "" {
		f.Queries = strings.Split(queries, ", ")
	}
	f.Created, _ = time.Parse(time.RFC3339Nano, created)
	return f, nil
}

// Get returns the latest version of a fact, active or retired.
func (s *Store) Get(id string) (Fact, error) {
	f, err := scan(s.db.QueryRow(`SELECT `+s.cols("")+` FROM facts WHERE id = ? ORDER BY version DESC LIMIT 1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Fact{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return f, err
}

// History returns every version of a fact, oldest first.
func (s *Store) History(id string) ([]Fact, error) {
	return s.query(`SELECT `+s.cols("")+` FROM facts WHERE id = ? ORDER BY version`, id)
}

// Rows returns every version of every fact, ordered by id and version.
func (s *Store) Rows() ([]Fact, error) {
	return s.query(`SELECT ` + s.cols("") + ` FROM facts ORDER BY id, version`)
}

// Active returns the latest version of every approved fact: not retired, not a candidate and not provisional. It
// reads the facts, not the index: a lost index row must not hide a fact from facts, an audit or a card. Open repairs
// the index.
func (s *Store) Active() ([]Fact, error) {
	return s.query(`SELECT `+s.cols("f")+` FROM facts f
		WHERE f.version = (SELECT max(version) FROM facts WHERE id = f.id) AND f.status = ? AND f.review = ''
		ORDER BY f.id`, Active)
}

// cols is the column list of a SELECT from facts, under alias when it is not empty; a column the base lacks reads as
// its default.
func (s *Store) cols(alias string) string {
	out := strings.Split(columns, ", ")
	for i, c := range out {
		switch def, missing := s.absent[c]; {
		case missing:
			out[i] = def + " AS " + c
		case alias != "":
			out[i] = alias + "." + c
		}
	}
	return strings.Join(out, ", ")
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
	rows, err := s.db.Query(`SELECT i.stems, `+s.cols("f")+`
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
		var stems, keywords, created, queries string
		if err := rows.Scan(&stems, &h.ID, &h.Version, &h.Content, &keywords, &h.Origin, &created, &h.Status,
			&h.Author, &h.Expires, &h.Kind, &h.Scope, &h.Source, &h.Evidence, &h.Review, &h.PromptVersion, &queries); err != nil {
			return nil, err
		}
		if keywords != "" {
			h.Keywords = strings.Split(keywords, ", ")
		}
		if queries != "" {
			h.Queries = strings.Split(queries, ", ")
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
