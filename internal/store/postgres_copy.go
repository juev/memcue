package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/juev/memcue/internal/tailnet"
)

// ErrTargetNotEmpty means the PostgreSQL base already holds facts: a copy goes only into an empty base, so it never
// becomes a second writer beside an authority that is already in use.
var ErrTargetNotEmpty = errors.New("PostgreSQL base already holds facts; a copy goes only into an empty base")

// PostgresTarget is what a copy would find in PostgreSQL: whether the memcue schema exists and how many versions it
// holds.
type PostgresTarget struct {
	Schema bool
	Rows   int64
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
			return errors.New("incompatible PostgreSQL schema; operator repair required")
		}
		t.Schema = factsTable
		if !factsTable {
			return nil
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM memcue.facts`).Scan(&t.Rows); err != nil {
			return errRemote
		}
		return nil
	})
	return t, err
}

// CopyToPostgres writes every version of a local base, as Rows returns them, into an empty PostgreSQL base in one
// transaction. Before the commit it reads the versions back and compares them with the source byte for byte, so a
// copy that does not match leaves the base empty. It creates the schema of an empty database, as the first open does.
// A lost commit reply is ErrAmbiguous: inspect the base before trying again.
func CopyToPostgres(ctx context.Context, dsn string, rows []Fact) error {
	if len(rows) > 100000 {
		return fmt.Errorf("%d versions exceed the PostgreSQL history limit of 100000", len(rows))
	}
	payloads := make([][]byte, len(rows))
	size := 0
	for i, f := range rows {
		if f.ID == "" || f.Version <= 0 {
			return fmt.Errorf("source version %d has no id or version", i)
		}
		b, err := marshalFact(ctx, f)
		if err != nil {
			return err
		}
		payloads[i] = b
		size += len(b)
	}
	if size > 64<<20 {
		return fmt.Errorf("%d bytes of history exceed the PostgreSQL limit of 64 MiB", size)
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
		// One COPY and one read-back of the whole history outlast the ten seconds an ordinary statement gets.
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
		copied, err := tx.CopyFrom(ctx, pgx.Identifier{"memcue", "facts"}, []string{"id", "version", "payload"},
			pgx.CopyFromSlice(len(rows), func(i int) ([]any, error) {
				return []any{rows[i].ID, rows[i].Version, string(payloads[i])}, nil
			}))
		if err != nil {
			return fmt.Errorf("copy to PostgreSQL: %w", err)
		}
		if copied != int64(len(rows)) {
			return fmt.Errorf("copied %d of %d versions", copied, len(rows))
		}
		return verifyCopy(ctx, tx, rows, payloads)
	})
}

// verifyCopy reads the copied history back in the order of the source and compares each version with its source.
func verifyCopy(ctx context.Context, tx pgx.Tx, rows []Fact, payloads [][]byte) error {
	got, err := tx.Query(ctx, `SELECT id, version, payload FROM memcue.facts ORDER BY id, version`)
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
		if i >= len(rows) {
			return errors.New("PostgreSQL holds more versions than were copied")
		}
		var f Fact
		if err := unmarshalFact(ctx, b, &f); err != nil {
			return fmt.Errorf("copied version %s/%d does not read back: %w", id, version, err)
		}
		again, err := marshalFact(ctx, f)
		if err != nil {
			return err
		}
		if id != rows[i].ID || version != rows[i].Version || !bytes.Equal(again, payloads[i]) {
			return fmt.Errorf("copied version %s/%d differs from the source %s/%d", id, version, rows[i].ID, rows[i].Version)
		}
		i++
	}
	if got.Err() != nil {
		return errRemote
	}
	if i != len(rows) {
		return fmt.Errorf("read back %d of %d versions", i, len(rows))
	}
	var size int64
	if err := tx.QueryRow(ctx, `SELECT coalesce(sum(octet_length(payload::text)),0) FROM memcue.facts`).Scan(&size); err != nil {
		return errRemote
	}
	if size > 64<<20 {
		return errors.New("PostgreSQL history exceeds this client's projection limit")
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
