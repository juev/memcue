package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/juev/memcue/internal/hlc"
)

// The roles of a synced base (#714, docs/specs/sync-server.md). The server keeps the shared base and numbers its
// changes; a replica keeps a local copy and an outbox of the changes the server has not accepted yet. Both keep one
// row per fact: facts holds its one current version, sync_rows the stamps of its registers and its change number.
const (
	RoleServer  = "server"
	RoleReplica = "replica"
)

// Names in the stamps table of a synced base.
const (
	stampRole     = "role"
	stampDevice   = "replica_id" // the device id of this copy, server or replica, that its HLC stamps carry
	stampBaseID   = "base_id"    // the server base this copy belongs to
	stampLastSeq  = "last_seq"   // replica: the greatest seq applied from the server
	stampHLC      = "hlc"        // the last stamp the copy's clock issued or observed
	stampLastSync = "last_sync"  // replica: when the helper last heard from the server
)

const syncDDL = `
CREATE TABLE IF NOT EXISTS sync_rows (
	id              TEXT PRIMARY KEY,
	content_stamp   TEXT NOT NULL DEFAULT '',
	kind_stamp      TEXT NOT NULL DEFAULT '',
	scope_stamp     TEXT NOT NULL DEFAULT '',
	expires_stamp   TEXT NOT NULL DEFAULT '',
	status_stamp    TEXT NOT NULL DEFAULT '',
	origin_stamp    TEXT NOT NULL DEFAULT '',
	seq             INTEGER NOT NULL DEFAULT 0,
	updated         TEXT NOT NULL DEFAULT '',
	loser_of        TEXT NOT NULL DEFAULT ''
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS sync_rows_seq ON sync_rows(seq);
`

// serverDDL: the answer to every operation the server applied, by op_id, so a retry gets it again (R4).
const serverDDL = `
CREATE TABLE IF NOT EXISTS ops (op_id TEXT PRIMARY KEY, result TEXT NOT NULL) WITHOUT ROWID;
`

// replicaDDL: the outbox, the replica's changes in the order they were made, until the server accepts them (R3).
const replicaDDL = `
CREATE TABLE IF NOT EXISTS outbox (
	n      INTEGER PRIMARY KEY AUTOINCREMENT,
	op_id  TEXT NOT NULL UNIQUE,
	id     TEXT NOT NULL,
	change TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS outbox_id ON outbox(id);
`

// ErrSyncedBase: a base of a server or a replica opened in another role, or as an ordinary base.
var ErrSyncedBase = errors.New("fact base is a synced base")

// ErrBaseChanged: rows of a server base other than the one the replica holds; the replica must be rebuilt (R9).
var ErrBaseChanged = errors.New("server base differs from the replica's")

// Refusals of a change the server could not apply (Applied.Refused).
const (
	RefusedMalformed = "malformed"
	RefusedIDTaken   = "id_taken"
	RefusedNotFound  = "not_found"
)

// syncRole is what a synced Store knows of its role.
type syncRole struct {
	role   string
	device string
}

// OpenServer opens or creates the base of a sync server at path.
func OpenServer(path string) (*Store, error) { return openSynced(path, RoleServer) }

// OpenReplica opens or creates a replica at path.
func OpenReplica(path string) (*Store, error) { return openSynced(path, RoleReplica) }

func openSynced(path, role string) (*Store, error) {
	s, err := openFile(path)
	if err != nil {
		return nil, err
	}
	if err := s.claimRole(role); err != nil {
		_ = s.Close()
		return nil, err
	}
	return s, nil
}

// claimRole creates the tables of role and the device id, or checks that the base already has that role. A base with
// facts and no role keeps a history of versions, which a synced base cannot hold; migrate -server loads it instead.
func (s *Store) claimRole(role string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	have, err := txStamp(tx, stampRole)
	if err != nil {
		return err
	}
	switch {
	case have != "" && have != role:
		return fmt.Errorf("%w: it is the base of a %s, not of a %s", ErrSyncedBase, have, role)
	case have == "":
		var n int
		if err := tx.QueryRow(`SELECT count(*) FROM facts`).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return fmt.Errorf("%w: the base holds %d versions of facts that are not synced; load them with migrate -server", ErrSyncedBase, n)
		}
	}
	ddl := syncDDL + serverDDL
	if role == RoleReplica {
		ddl = syncDDL + replicaDDL
	}
	if _, err := tx.Exec(ddl); err != nil {
		return err
	}
	device, err := txStamp(tx, stampDevice)
	if err != nil {
		return err
	}
	if device == "" {
		if device, err = hlc.NewDevice(); err != nil {
			return err
		}
		if err := saveStamp(tx, stampDevice, device); err != nil {
			return err
		}
	}
	if role == RoleServer {
		if id, err := txStamp(tx, stampBaseID); err != nil {
			return err
		} else if id == "" {
			if err := saveStamp(tx, stampBaseID, newOpID(s.now())); err != nil {
				return err
			}
		}
	}
	if err := saveStamp(tx, stampRole, role); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.sync = &syncRole{role: role, device: device}
	return nil
}

// refuseSynced keeps an ordinary Open off a synced base: its writes add versions, which a synced base cannot hold.
func (s *Store) refuseSynced() error {
	role, err := s.stamp(stampRole)
	if err != nil {
		return err
	}
	if role != "" {
		return fmt.Errorf("%w: it is the base of a %s; set MEMCUE_SERVER to use it", ErrSyncedBase, role)
	}
	return nil
}

func txStamp(tx *sql.Tx, name string) (string, error) {
	var v string
	err := tx.QueryRow(`SELECT value FROM stamps WHERE name = ?`, name).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

func newOpID(now time.Time) string {
	return ulid.MustNew(ulid.Timestamp(now), entropy).String()
}

// clock reads the copy's HLC inside a write transaction; every process of the machine shares it through the base.
func (s *Store) clock(tx *sql.Tx) (*hlc.Clock, error) {
	v, err := txStamp(tx, stampHLC)
	if err != nil {
		return nil, err
	}
	last, err := hlc.Parse(v)
	if err != nil {
		return nil, err
	}
	return hlc.New(s.sync.device, last, s.now)
}

func saveClock(tx *sql.Tx, c *hlc.Clock) error { return saveStamp(tx, stampHLC, c.Last().String()) }

func stampColumn(reg string) string { return reg + "_stamp" }

var syncColumns = func() string {
	cols := make([]string, 0, len(Registers)+3)
	for _, reg := range Registers {
		cols = append(cols, stampColumn(reg))
	}
	return strings.Join(append(cols, "seq", "updated", "loser_of"), ", ")
}()

// loadRow reads the row of id; exists is false when the base has no such fact.
func loadRow(tx *sql.Tx, id string) (row Row, exists bool, err error) {
	f, err := scan(tx.QueryRow(`SELECT `+columns+` FROM facts WHERE id = ? ORDER BY version DESC LIMIT 1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Row{}, false, nil
	}
	if err != nil {
		return Row{}, false, err
	}
	row, err = scanSync(tx.QueryRow(`SELECT `+syncColumns+` FROM sync_rows WHERE id = ?`, id), f)
	if errors.Is(err, sql.ErrNoRows) {
		return Row{Fact: f, Stamps: Stamps{}}, true, nil
	}
	return row, err == nil, err
}

func scanSync(r interface{ Scan(...any) error }, f Fact) (Row, error) {
	stamps := make([]string, len(Registers))
	var (
		seq              int64
		updated, loserOf string
	)
	args := make([]any, 0, len(Registers)+3)
	for i := range stamps {
		args = append(args, &stamps[i])
	}
	if err := r.Scan(append(args, &seq, &updated, &loserOf)...); err != nil {
		return Row{}, err
	}
	row := Row{Fact: f, Stamps: Stamps{}, Seq: seq, LoserOf: loserOf}
	for i, reg := range Registers {
		if stamps[i] != "" {
			row.Stamps[reg] = stamps[i]
		}
	}
	row.Updated, _ = time.Parse(time.RFC3339Nano, updated)
	return row, nil
}

// saveRow makes row the one stored version of its fact and refreshes its index entry.
func saveRow(tx *sql.Tx, row Row) error {
	if _, err := tx.Exec(`DELETE FROM facts WHERE id = ?`, row.ID); err != nil {
		return err
	}
	if err := insert(tx, row.Fact); err != nil {
		return err
	}
	args := []any{row.ID}
	for _, reg := range Registers {
		args = append(args, row.Stamps[reg])
	}
	updated := ""
	if !row.Updated.IsZero() {
		updated = row.Updated.UTC().Format(time.RFC3339Nano)
	}
	args = append(args, row.Seq, updated, row.LoserOf)
	if _, err := tx.Exec(`INSERT OR REPLACE INTO sync_rows(id, `+syncColumns+`) VALUES (?, `+marks(len(args)-1)+`)`,
		args...); err != nil {
		return err
	}
	return reindex(tx, row.ID)
}

func nextSeq(tx *sql.Tx) (int64, error) {
	var seq int64
	err := tx.QueryRow(`SELECT coalesce(max(seq), 0) + 1 FROM sync_rows`).Scan(&seq)
	return seq, err
}

// syncWrite stores facts as this copy's own writes: each register that differs from the stored fact becomes a change
// stamped by the shared clock. The server numbers the change; a replica puts it into its outbox. A fact the base
// does not hold is created with every register. It returns the facts as stored.
func (s *Store) syncWrite(facts []Fact) ([]Fact, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	clock, err := s.clock(tx)
	if err != nil {
		return nil, err
	}
	out := make([]Fact, 0, len(facts))
	for _, f := range facts {
		row, exists, err := loadRow(tx, f.ID)
		if err != nil {
			return nil, err
		}
		ch := Change{OpID: newOpID(s.now()), ID: f.ID}
		if exists {
			ch.Values, ch.Base = Diff(row.Fact, f), row.Stamps[RegContent]
			if len(ch.Values) == 0 {
				out = append(out, row.Fact)
				continue
			}
		} else {
			ch.Create, ch.Created, ch.Source, ch.Values = true, f.Created, f.Source, map[string]string{}
			for _, reg := range Registers {
				ch.Values[reg] = Register(f, reg)
			}
		}
		ch.Stamp = clock.Next().String()
		if _, err := ApplyChange(&row, exists, ch); err != nil {
			return nil, err
		}
		row.Updated = s.now().UTC()
		if s.sync.role == RoleServer {
			if row.Seq, err = nextSeq(tx); err != nil {
				return nil, err
			}
		} else if err := enqueue(tx, ch); err != nil {
			return nil, err
		}
		if err := saveRow(tx, row); err != nil {
			return nil, err
		}
		out = append(out, row.Fact)
	}
	if err := saveClock(tx, clock); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

func enqueue(tx *sql.Tx, ch Change) error {
	b, err := json.Marshal(ch)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO outbox(op_id, id, change) VALUES (?, ?, ?)`, ch.OpID, ch.ID, string(b))
	return err
}

// Applied is the server's answer to one change.
type Applied struct {
	OpID string `json:"op_id"`
	ID   string `json:"id"`
	// Seq is the change number of the fact after the change, 0 when it was refused.
	Seq int64 `json:"seq"`
	// Loser is the id of the new fact that keeps the content a concurrent correction pushed out (R6).
	Loser string `json:"loser,omitempty"`
	// Refused names why the change was not applied: RefusedMalformed, RefusedIDTaken or RefusedNotFound.
	Refused string `json:"refused,omitempty"`
}

// ApplyChanges merges changes sent by replicas into the server base in one transaction. A change whose op_id the
// server has answered gets that answer again and is not applied twice (R4). A change that cannot be applied is
// refused on its own and does not stop the others.
func (s *Store) ApplyChanges(changes []Change) ([]Applied, error) {
	if s.sync == nil || s.sync.role != RoleServer {
		return nil, fmt.Errorf("%w: only a server applies changes of replicas", ErrSyncedBase)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	clock, err := s.clock(tx)
	if err != nil {
		return nil, err
	}
	out := make([]Applied, 0, len(changes))
	for _, ch := range changes {
		if ch.OpID == "" {
			out = append(out, Applied{ID: ch.ID, Refused: RefusedMalformed})
			continue
		}
		var prev string
		err := tx.QueryRow(`SELECT result FROM ops WHERE op_id = ?`, ch.OpID).Scan(&prev)
		if err == nil {
			var a Applied
			if err := json.Unmarshal([]byte(prev), &a); err != nil {
				return nil, err
			}
			out = append(out, a)
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		a, err := s.applyOne(tx, clock, ch)
		if err != nil {
			return nil, err
		}
		b, err := json.Marshal(a)
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(`INSERT INTO ops(op_id, result) VALUES (?, ?)`, ch.OpID, string(b)); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	if err := saveClock(tx, clock); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) applyOne(tx *sql.Tx, clock *hlc.Clock, ch Change) (Applied, error) {
	a := Applied{OpID: ch.OpID, ID: ch.ID}
	if ch.Create && ValidID(ch.ID) != nil {
		a.Refused = RefusedMalformed
		return a, nil
	}
	row, exists, err := loadRow(tx, ch.ID)
	if err != nil {
		return a, err
	}
	m, err := ApplyChange(&row, exists, ch)
	switch {
	case errors.Is(err, ErrIDTaken):
		a.Refused = RefusedIDTaken
		return a, nil
	case errors.Is(err, ErrNotFound):
		a.Refused = RefusedNotFound
		return a, nil
	case errors.Is(err, ErrChange):
		a.Refused = RefusedMalformed
		return a, nil
	case err != nil:
		return a, err
	}
	if st, err := hlc.Parse(ch.Stamp); err == nil {
		clock.Observe(st)
	}
	if m.Changed {
		row.Updated = s.now().UTC()
		if row.Seq, err = nextSeq(tx); err != nil {
			return a, err
		}
		if err := saveRow(tx, row); err != nil {
			return a, err
		}
	}
	a.Seq = row.Seq
	if m.HasLoser {
		if a.Loser, err = s.keepLoser(tx, clock, row, m.Loser); err != nil {
			return a, err
		}
	}
	return a, nil
}

// keepLoser stores content that lost to a concurrent correction of row as a new fact with the other registers of row
// and a reference to it (R6).
func (s *Store) keepLoser(tx *sql.Tx, clock *hlc.Clock, row Row, content string) (string, error) {
	ch := Change{OpID: newOpID(s.now()), ID: newOpID(s.now()), Create: true, Created: s.now().UTC(), Source: row.Source,
		Values: map[string]string{}, Stamp: clock.Next().String()}
	for _, reg := range Registers {
		ch.Values[reg] = Register(row.Fact, reg)
	}
	ch.Values[RegContent] = content
	var loser Row
	if _, err := ApplyChange(&loser, false, ch); err != nil {
		return "", err
	}
	loser.Updated = s.now().UTC()
	var err error
	if loser.Seq, err = nextSeq(tx); err != nil {
		return "", err
	}
	loser.LoserOf = row.ID
	return loser.ID, saveRow(tx, loser)
}

// LoserOf is the id of the fact whose concurrent correction pushed out the content of id, or "".
func (s *Store) LoserOf(id string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT loser_of FROM sync_rows WHERE id = ?`, id).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// BaseID is the id of the server base: the server's own, or the one a replica holds rows of ("" before the first).
func (s *Store) BaseID() (string, error) { return s.stamp(stampBaseID) }

// Changes returns the rows of the server with seq above since, in order of seq, at most limit of them, and the head:
// the greatest seq of the base.
func (s *Store) Changes(since int64, limit int) ([]Row, int64, error) {
	if s.sync == nil {
		return nil, 0, fmt.Errorf("%w: not a synced base", ErrSyncedBase)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = tx.Rollback() }()
	var head int64
	if err := tx.QueryRow(`SELECT coalesce(max(seq), 0) FROM sync_rows`).Scan(&head); err != nil {
		return nil, 0, err
	}
	rows, err := tx.Query(`SELECT r.id FROM sync_rows r WHERE r.seq > ? ORDER BY r.seq LIMIT ?`, since, limit)
	if err != nil {
		return nil, 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, 0, err
	}
	out := make([]Row, 0, len(ids))
	for _, id := range ids {
		row, exists, err := loadRow(tx, id)
		if err != nil {
			return nil, 0, err
		}
		if exists {
			out = append(out, row)
		}
	}
	return out, head, tx.Commit()
}

// Outbox is the replica's changes the server has not accepted, in the order they were made.
func (s *Store) Outbox() ([]Change, error) {
	if s.sync == nil || s.sync.role != RoleReplica {
		return nil, fmt.Errorf("%w: only a replica has an outbox", ErrSyncedBase)
	}
	return outbox(s.db, "")
}

// outbox reads the outbox, of one fact when id is set.
func outbox(q interface {
	Query(string, ...any) (*sql.Rows, error)
}, id string) ([]Change, error) {
	query, args := `SELECT change FROM outbox ORDER BY n`, []any{}
	if id != "" {
		query, args = `SELECT change FROM outbox WHERE id = ? ORDER BY n`, []any{id}
	}
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Change
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		var ch Change
		if err := json.Unmarshal([]byte(v), &ch); err != nil {
			return nil, err
		}
		out = append(out, ch)
	}
	return out, rows.Err()
}

// Acknowledge removes from the outbox the change the server accepted as opID, and gives its fact the server's seq.
func (s *Store) Acknowledge(opID string, seq int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var id string
	err = tx.QueryRow(`DELETE FROM outbox WHERE op_id = ? RETURNING id`, opID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE sync_rows SET seq = max(seq, ?) WHERE id = ?`, seq, id); err != nil {
		return err
	}
	return tx.Commit()
}

// ApplyRows makes the rows received from the server of baseID the replica's rows and applies the outbox entries of
// their facts on top again, so a change the server has not seen stays in effect. The replica's clock moves past
// every stamp received, and last_seq to the greatest seq. Rows of another server base are refused with ErrBaseChanged:
// the replica must be rebuilt first (ResetReplica).
func (s *Store) ApplyRows(baseID string, rows []Row) error {
	if s.sync == nil || s.sync.role != RoleReplica {
		return fmt.Errorf("%w: only a replica applies the server's rows", ErrSyncedBase)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	have, err := txStamp(tx, stampBaseID)
	if err != nil {
		return err
	}
	switch {
	case baseID == "":
		return fmt.Errorf("%w: rows without a base id", ErrBaseChanged)
	case have == "":
		if err := saveStamp(tx, stampBaseID, baseID); err != nil {
			return err
		}
	case have != baseID:
		return fmt.Errorf("%w: replica holds %s, the server sent %s", ErrBaseChanged, have, baseID)
	}
	clock, err := s.clock(tx)
	if err != nil {
		return err
	}
	last, err := txLastSeq(tx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		for _, v := range row.Stamps {
			if st, err := hlc.Parse(v); err == nil {
				clock.Observe(st)
			}
		}
		if row.Stamps == nil {
			row.Stamps = Stamps{}
		}
		if err := s.replay(tx, row, true); err != nil {
			return err
		}
		last = max(last, row.Seq)
	}
	if err := saveStamp(tx, stampLastSeq, strconv.FormatInt(last, 10)); err != nil {
		return err
	}
	if err := saveStamp(tx, stampLastSync, s.now().UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	if err := saveClock(tx, clock); err != nil {
		return err
	}
	return tx.Commit()
}

// replay stores row, when exists, with the outbox entries of its fact applied on top; for a fact the replica does not
// hold yet it applies them to nothing, so a fact created here comes back. An entry the row cannot take (a fact not
// found, an id the server gave another fact) waits for the helper to resolve it.
func (s *Store) replay(tx *sql.Tx, row Row, exists bool) error {
	pending, err := outbox(tx, row.ID)
	if err != nil {
		return err
	}
	seq := row.Seq
	for _, ch := range pending {
		_, err := ApplyChange(&row, exists, ch)
		switch {
		case errors.Is(err, ErrNotFound), errors.Is(err, ErrIDTaken):
			continue
		case err != nil:
			return err
		}
		exists = true
	}
	if !exists {
		return nil
	}
	row.Seq = seq
	return saveRow(tx, row)
}

func txLastSeq(tx *sql.Tx) (int64, error) {
	v, err := txStamp(tx, stampLastSeq)
	if err != nil || v == "" {
		return 0, err
	}
	return strconv.ParseInt(v, 10, 64)
}

// ResetReplica drops every row the replica got from a server and binds it to baseID with nothing applied (R9). The
// outbox stays and is applied again, so a fact created here is still found; the helper then fetches the server's
// rows from the start.
func (s *Store) ResetReplica(baseID string) error {
	if s.sync == nil || s.sync.role != RoleReplica {
		return fmt.Errorf("%w: only a replica is rebuilt from a server", ErrSyncedBase)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, q := range []string{`DELETE FROM facts`, `DELETE FROM sync_rows`} {
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}
	if err := rebuild(tx); err != nil {
		return err
	}
	if err := saveStamp(tx, stampBaseID, baseID); err != nil {
		return err
	}
	if err := saveStamp(tx, stampLastSeq, "0"); err != nil {
		return err
	}
	rows, err := tx.Query(`SELECT DISTINCT id FROM outbox ORDER BY id`)
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
		if err := s.replay(tx, Row{Fact: Fact{ID: id}, Stamps: Stamps{}}, false); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SyncState is what a replica knows of its sync.
type SyncState struct {
	BaseID   string
	LastSeq  int64
	LastSync time.Time // zero before the replica first heard from the server
	Outbox   int
}

// State reports the replica's sync state.
func (s *Store) State() (SyncState, error) {
	if s.sync == nil || s.sync.role != RoleReplica {
		return SyncState{}, fmt.Errorf("%w: only a replica has a sync state", ErrSyncedBase)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return SyncState{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var st SyncState
	if st.BaseID, err = txStamp(tx, stampBaseID); err != nil {
		return st, err
	}
	if st.LastSeq, err = txLastSeq(tx); err != nil {
		return st, err
	}
	v, err := txStamp(tx, stampLastSync)
	if err != nil {
		return st, err
	}
	st.LastSync, _ = time.Parse(time.RFC3339Nano, v)
	if err := tx.QueryRow(`SELECT count(*) FROM outbox`).Scan(&st.Outbox); err != nil {
		return st, err
	}
	return st, tx.Commit()
}

// ValidID checks that id is a fact id: a ULID in its canonical form.
func ValidID(id string) error {
	u, err := ulid.ParseStrict(id)
	if err != nil || u.String() != id {
		return fmt.Errorf("%w: fact id %q is not a ULID", ErrChange, id)
	}
	return nil
}

// ErrNotEmpty: an import into a server base that already holds facts.
var ErrNotEmpty = errors.New("server base already holds facts")

// Import loads facts into an empty server base in one transaction (R12): every register of a fact is stamped with
// its creation time and the server's device id. A base that holds any fact refuses with ErrNotEmpty and stores
// nothing; so does a set with an invalid or repeated fact.
func (s *Store) Import(facts []Fact) error {
	if s.sync == nil || s.sync.role != RoleServer {
		return fmt.Errorf("%w: only a server imports facts", ErrSyncedBase)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var n int
	if err := tx.QueryRow(`SELECT count(*) FROM facts`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("%w: %d facts", ErrNotEmpty, n)
	}
	clock, err := s.clock(tx)
	if err != nil {
		return err
	}
	seen := make(map[string]bool, len(facts))
	for _, f := range facts {
		if err := checkImported(f); err != nil {
			return err
		}
		if seen[f.ID] {
			return fmt.Errorf("%w: fact %s given twice", ErrChange, f.ID)
		}
		seen[f.ID] = true
		st := hlc.Stamp{Millis: f.Created.UnixMilli(), Device: s.sync.device}
		clock.Observe(st)
		row := Row{Fact: Fact{ID: f.ID, Version: max(f.Version, 1), Content: strings.TrimSpace(f.Content), Origin: f.Origin,
			Created: f.Created.UTC(), Status: f.Status, Expires: f.Expires,
			Meta: approved(Meta{Kind: f.Kind, Scope: f.Scope, Source: f.Source})}, Stamps: Stamps{}, Updated: s.now().UTC()}
		for _, reg := range Registers {
			row.Stamps[reg] = st.String()
		}
		if row.Seq, err = nextSeq(tx); err != nil {
			return err
		}
		if err := saveRow(tx, row); err != nil {
			return err
		}
	}
	if err := saveClock(tx, clock); err != nil {
		return err
	}
	return tx.Commit()
}

func checkImported(f Fact) error {
	if err := ValidID(f.ID); err != nil {
		return err
	}
	switch {
	case strings.TrimSpace(f.Content) == "":
		return fmt.Errorf("%w: fact %s has no content", ErrChange, f.ID)
	case f.Status != Active && f.Status != Retired:
		return fmt.Errorf("%w: fact %s has status %q", ErrChange, f.ID, f.Status)
	case f.Created.IsZero() || f.Created.UnixMilli() <= 0:
		return fmt.Errorf("%w: fact %s has no creation time", ErrChange, f.ID)
	}
	if err := ValidExpiry(f.Expires); err != nil {
		return fmt.Errorf("%w: fact %s: %v", ErrChange, f.ID, err)
	}
	return nil
}

// DataVersion changes when another connection, such as a dream in another process, commits to the base; this
// Store's own commits leave it as it is. A long poll compares it to notice changes it did not make.
func (s *Store) DataVersion() (int64, error) {
	var v int64
	err := s.db.QueryRow(`PRAGMA data_version`).Scan(&v)
	return v, err
}
