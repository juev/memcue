package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/juev/memcue/internal/text"
)

// Limits of a PostgreSQL base: the facts a client still reads whole (facts, Rows) must fit its memory.
const (
	postgresMaxFacts = 100000
	postgresMaxBytes = 64 << 20
)

var errPostgresLimit = errors.New("PostgreSQL base exceeds this client's limit of 100000 facts or 64 MiB")

// postgresTransaction is one write transaction under the PostgreSQL write lock.
type postgresTransaction struct {
	ctx context.Context
	tx  pgx.Tx
}

func (p *postgresTransaction) get(id string) (Fact, error) {
	f, err := scanRemote(p.ctx, p.tx.QueryRow(p.ctx, `SELECT version, payload FROM memcue.facts WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Fact{}, ErrNotFound
	}
	if err != nil {
		return Fact{}, errRemote
	}
	if f.ID != id {
		return Fact{}, errRemote
	}
	return f, nil
}

// append stores the new version of each fact: a first version is a new row, a later one replaces the row of the
// version before it. The store logic has already read that version under the lock, so a row that does not match is
// a base written past this protocol.
func (p *postgresTransaction) append(facts []Fact) error {
	if len(facts) == 0 {
		return nil
	}
	for _, f := range facts {
		b, err := marshalFact(p.ctx, f)
		if err != nil {
			return errRemote
		}
		terms, err := searchTerms(p.ctx, f)
		if err != nil {
			return err
		}
		q := `INSERT INTO memcue.facts(id,version,payload,terms) VALUES($1,$2,$3,$4) ON CONFLICT (id) DO NOTHING`
		if f.Version > 1 {
			q = `UPDATE memcue.facts SET version=$2, payload=$3, terms=$4 WHERE id=$1 AND version=$2-1`
		}
		tag, err := p.tx.Exec(p.ctx, q, f.ID, f.Version, string(b), terms)
		if err != nil {
			return errRemote
		}
		if tag.RowsAffected() != 1 {
			return fmt.Errorf("%w: %s", ErrChanged, f.ID)
		}
	}
	var n, size int64
	if err := p.tx.QueryRow(p.ctx, `SELECT count(*), coalesce(sum(octet_length(payload::text)),0) FROM memcue.facts`).Scan(&n, &size); err != nil {
		return errRemote
	}
	if n > postgresMaxFacts || size > postgresMaxBytes {
		return errPostgresLimit
	}
	return nil
}

// maxLexeme is one byte under the longest lexeme a tsvector holds (2047 bytes); a longer term stays out of the index.
const maxLexeme = 2046

// searchTerms are the stems the server searches a fact by, the same the local index holds. A retired fact is out of
// every search, so it keeps none. The slice is never nil: pgx sends a nil slice as NULL.
func searchTerms(ctx context.Context, f Fact) ([]string, error) {
	terms := []string{}
	if f.Status == Retired {
		return terms, nil
	}
	stems, err := text.StemsContext(ctx, f.Content)
	if err != nil {
		return nil, err
	}
	for _, t := range stems {
		if t != "" && len(t) <= maxLexeme {
			terms = append(terms, t)
		}
	}
	return terms, nil
}

// scanRemote reads the version and payload of one row and checks that they agree.
func scanRemote(ctx context.Context, row pgx.Row, extra ...any) (Fact, error) {
	var version int
	var payload []byte
	if err := row.Scan(append([]any{&version, &payload}, extra...)...); err != nil {
		return Fact{}, err
	}
	var f Fact
	if unmarshalFact(ctx, payload, &f) != nil || f.Version != version || f.ID == "" {
		return Fact{}, errRemote
	}
	return f, nil
}

func remoteWrite[T any](s *Store, fn func(*Store) (T, error)) (T, error) {
	ctx, cancel := s.remote.operation()
	defer cancel()
	return postgresOp(s, ctx, true, func(ctx context.Context, c *pgx.Conn) (result T, err error) {
		tx, err := c.Begin(ctx)
		if err != nil {
			return result, errRemote
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(680681)`); err != nil {
			return result, errRemote
		}
		result, err = fn(&Store{native: &postgresTransaction{ctx, tx}, now: s.now, author: s.author})
		if err != nil {
			return result, err
		}
		if err = tx.Commit(ctx); err != nil {
			return result, ErrAmbiguous
		}
		return result, nil
	})
}

// remoteRead runs one read statement through fn under an operation budget.
func remoteRead[T any](s *Store, fn func(context.Context, *pgx.Conn) (T, error)) (T, error) {
	ctx, cancel := s.remote.operation()
	defer cancel()
	return postgresOp(s, ctx, false, fn)
}

func remoteGet(s *Store, id string) (Fact, error) {
	return remoteRead(s, func(ctx context.Context, c *pgx.Conn) (Fact, error) {
		f, err := scanRemote(ctx, c.QueryRow(ctx, `SELECT version, payload FROM memcue.facts WHERE id=$1`, id))
		if errors.Is(err, pgx.ErrNoRows) {
			return Fact{}, fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		if err != nil || f.ID != id {
			return Fact{}, errRemote
		}
		return f, nil
	})
}

// remoteHistory is the one version a PostgreSQL base keeps: it holds no history (#700).
func remoteHistory(s *Store, id string) ([]Fact, error) {
	f, err := remoteGet(s, id)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return []Fact{f}, nil
}

// remoteFacts reads the facts a statement selects, ordered as it orders them.
func remoteFacts(s *Store, q string, args ...any) ([]Fact, error) {
	return remoteRead(s, func(ctx context.Context, c *pgx.Conn) ([]Fact, error) {
		rows, err := c.Query(ctx, q, args...)
		if err != nil {
			return nil, errRemote
		}
		defer rows.Close()
		var out []Fact
		for rows.Next() {
			f, err := scanRemote(ctx, rows)
			if err != nil {
				return nil, errRemote
			}
			out = append(out, f)
		}
		if rows.Err() != nil {
			return nil, errRemote
		}
		return out, nil
	})
}

// remoteStrings reads one text column.
func remoteStrings(s *Store, q string, args ...any) ([]string, error) {
	return remoteRead(s, func(ctx context.Context, c *pgx.Conn) ([]string, error) {
		rows, err := c.Query(ctx, q, args...)
		if err != nil {
			return nil, errRemote
		}
		out, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return nil, errRemote
		}
		return out, nil
	})
}
