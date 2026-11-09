package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/juev/memcue/internal/tailnet"
)

// ErrTargetNotEmpty means the PostgreSQL base already holds facts: a copy goes only into an empty base, so it never
// becomes a second writer beside an authority that is already in use.
var ErrTargetNotEmpty = errors.New("PostgreSQL base already holds facts; a copy goes only into an empty base")

// PostgresTarget is what a copy would find in PostgreSQL: whether the memcue schema exists, its version and how many
// facts it holds.
type PostgresTarget struct {
	Schema  bool
	Version int
	Rows    int64
}

// InspectPostgres reads the state of the PostgreSQL base without creating its schema or writing anything.
func InspectPostgres(ctx context.Context, dsn string) (PostgresTarget, error) {
	var t PostgresTarget
	err := copyTransaction(ctx, dsn, false, func(tx pgx.Tx) error {
		var versionTable, factsTable bool
		if err := tx.QueryRow(ctx, `SELECT to_regclass('memcue.version') IS NOT NULL,to_regclass('memcue.facts') IS NOT NULL`).Scan(&versionTable, &factsTable); err != nil {
			return errRemote
		}
		if versionTable != factsTable {
			return errIncompatible
		}
		t.Schema = factsTable
		if !factsTable {
			return nil
		}
		if err := tx.QueryRow(ctx, `SELECT (SELECT version FROM memcue.version WHERE singleton), (SELECT count(*) FROM memcue.facts)`).Scan(&t.Version, &t.Rows); err != nil {
			return errRemote
		}
		return nil
	})
	return t, err
}

// Latest returns the latest version of every fact in rows, ordered by id: what a PostgreSQL base keeps (#700).
func Latest(rows []Fact) []Fact {
	latest := map[string]Fact{}
	for _, f := range rows {
		if old, ok := latest[f.ID]; !ok || f.Version > old.Version {
			latest[f.ID] = f
		}
	}
	out := make([]Fact, 0, len(latest))
	for _, f := range latest {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// CopyToPostgres writes the latest version of every fact of a local base, as Rows returns them, into an empty
// PostgreSQL base in one transaction, with the search terms of each. A PostgreSQL base keeps no history (#700). Before
// the commit it reads the facts back and compares them with the source byte for byte, so a copy that does not match
// leaves the base empty. It creates the schema of an empty database, as the first open does. A lost commit reply is
// ErrAmbiguous: inspect the base before trying again.
func CopyToPostgres(ctx context.Context, dsn string, rows []Fact) error {
	for i, f := range rows {
		if f.ID == "" || f.Version <= 0 {
			return fmt.Errorf("source version %d has no id or version", i)
		}
	}
	facts := Latest(rows)
	if len(facts) > postgresMaxFacts {
		return fmt.Errorf("%d facts exceed the PostgreSQL limit of %d", len(facts), postgresMaxFacts)
	}
	payloads := make([][]byte, len(facts))
	terms := make([][]string, len(facts))
	size := 0
	for i, f := range facts {
		b, err := marshalFact(ctx, f)
		if err != nil {
			return err
		}
		if terms[i], err = searchTerms(ctx, f); err != nil {
			return err
		}
		payloads[i] = b
		size += len(b)
	}
	if size > postgresMaxBytes {
		return fmt.Errorf("%d bytes of facts exceed the PostgreSQL limit of 64 MiB", size)
	}
	s, err := openPostgres(ctx, dsn)
	if err != nil {
		return err
	}
	_ = s.Close()
	return copyTransaction(ctx, dsn, true, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(680681)`); err != nil {
			return errRemote
		}
		// One COPY and one read-back of the whole base outlast the ten seconds an ordinary statement gets.
		if _, err := tx.Exec(ctx, `SET LOCAL statement_timeout = '300s'`); err != nil {
			return errRemote
		}
		var n int64
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM memcue.facts`).Scan(&n); err != nil {
			return errRemote
		}
		if n != 0 {
			return ErrTargetNotEmpty
		}
		copied, err := tx.CopyFrom(ctx, pgx.Identifier{"memcue", "facts"}, []string{"id", "version", "payload", "terms"},
			pgx.CopyFromSlice(len(facts), func(i int) ([]any, error) {
				return []any{facts[i].ID, facts[i].Version, string(payloads[i]), terms[i]}, nil
			}))
		if err != nil {
			return fmt.Errorf("copy to PostgreSQL: %w", err)
		}
		if copied != int64(len(facts)) {
			return fmt.Errorf("copied %d of %d facts", copied, len(facts))
		}
		return verifyCopy(ctx, tx, facts, payloads)
	})
}

// verifyCopy reads the copied facts back in the order of the source and compares each with its source.
func verifyCopy(ctx context.Context, tx pgx.Tx, facts []Fact, payloads [][]byte) error {
	got, err := tx.Query(ctx, `SELECT id, version, payload FROM memcue.facts ORDER BY id`)
	if err != nil {
		return errRemote
	}
	defer got.Close()
	i := 0
	for got.Next() {
		var id string
		var version int
		var b []byte
		if err := got.Scan(&id, &version, &b); err != nil {
			return errRemote
		}
		if i >= len(facts) {
			return errors.New("PostgreSQL holds more facts than were copied")
		}
		var f Fact
		if err := unmarshalFact(ctx, b, &f); err != nil {
			return fmt.Errorf("copied fact %s/%d does not read back: %w", id, version, err)
		}
		again, err := marshalFact(ctx, f)
		if err != nil {
			return err
		}
		if id != facts[i].ID || version != facts[i].Version || !bytes.Equal(again, payloads[i]) {
			return fmt.Errorf("copied fact %s/%d differs from the source %s/%d", id, version, facts[i].ID, facts[i].Version)
		}
		i++
	}
	if got.Err() != nil {
		return errRemote
	}
	if i != len(facts) {
		return fmt.Errorf("read back %d of %d facts", i, len(facts))
	}
	var size int64
	if err := tx.QueryRow(ctx, `SELECT coalesce(sum(octet_length(payload::text)),0) FROM memcue.facts`).Scan(&size); err != nil {
		return errRemote
	}
	if size > postgresMaxBytes {
		return errPostgresLimit
	}
	return nil
}

// copyTransaction runs fn in one transaction of its own connection under the caller's context, which bounds the whole
// copy instead of the budget of an ordinary operation. A write commits; a read stays read-only.
func copyTransaction(ctx context.Context, dsn string, write bool, fn func(pgx.Tx) error) error {
	cfg, err := PostgresConfig(dsn)
	if err != nil {
		return err
	}
	if err := tailnet.Configure(ctx, cfg); err != nil {
		return err
	}
	owned, release := ownedBudgetConfig(ctx, cfg)
	defer release()
	c, err := pgx.ConnectConfig(ctx, owned)
	if err != nil {
		return errRemote
	}
	stop := context.AfterFunc(ctx, func() { _ = c.PgConn().Conn().Close() })
	defer stop()
	defer func() { _ = c.Close(ctx) }()
	opts := pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}
	if write {
		opts = pgx.TxOptions{}
	}
	tx, err := c.BeginTx(ctx, opts)
	if err != nil {
		return errRemote
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	if !write {
		return nil
	}
	if err := tx.Commit(ctx); err != nil {
		return ErrAmbiguous
	}
	return nil
}
