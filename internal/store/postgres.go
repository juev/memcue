package store

import (
	"context"
	"errors"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/juev/memcue/internal/tailnet"
)

var errRemote = errors.New("PostgreSQL operation failed; check server availability, permissions and schema")

// ErrAmbiguous means a commit reply was lost. Never automatically replay this mutation.
var ErrAmbiguous = errors.New("PostgreSQL commit outcome is unknown; inspect the fact before retrying")

// ErrPostgresSchema1 is a base written by a memcue before #700: it kept every version and had no search index.
var ErrPostgresSchema1 = errors.New("PostgreSQL base uses schema 1, which this memcue does not read; recreate it with memcue migrate -postgres")

// postgresSchemaVersion is the schema of memcue.facts. Version 2 (#700) keeps one row per fact, its current version,
// with the search terms of its content, and searches on the server; version 1 kept every version and searched on the
// client.
const postgresSchemaVersion = 2

// postgresSchema holds one row per fact. The client writes the payload and the terms; PostgreSQL derives the
// columns the filters and the full-text index read, so they cannot disagree with the payload. Text that a filter
// compares or sorts is in the C collation: byte order, as SQLite compares it.
const postgresSchema = `CREATE SCHEMA IF NOT EXISTS memcue;
CREATE TABLE IF NOT EXISTS memcue.version (singleton boolean PRIMARY KEY CHECK(singleton), version integer NOT NULL);
INSERT INTO memcue.version VALUES(true,2) ON CONFLICT DO NOTHING;
CREATE TABLE IF NOT EXISTS memcue.facts (
 id text COLLATE "C" PRIMARY KEY,
 version integer NOT NULL CHECK(version>0),
 payload jsonb NOT NULL,
 terms text[] NOT NULL,
 active boolean NOT NULL GENERATED ALWAYS AS (coalesce(payload->>'Status' = 'active', false)) STORED,
 kind text COLLATE "C" NOT NULL GENERATED ALWAYS AS (coalesce(payload->>'Kind', '')) STORED,
 scope text COLLATE "C" NOT NULL GENERATED ALWAYS AS (coalesce(payload->>'Scope', '')) STORED,
 scope_fold text COLLATE "C" NOT NULL GENERATED ALWAYS AS (translate(coalesce(payload->>'Scope', ''), 'ABCDEFGHIJKLMNOPQRSTUVWXYZ', 'abcdefghijklmnopqrstuvwxyz')) STORED,
 origin text COLLATE "C" NOT NULL GENERATED ALWAYS AS (coalesce(payload->>'Origin', '')) STORED,
 created text COLLATE "C" NOT NULL GENERATED ALWAYS AS (coalesce(payload->>'Created', '')) STORED,
 expires text COLLATE "C" NOT NULL GENERATED ALWAYS AS (coalesce(payload->>'Expires', '')) STORED,
 length integer NOT NULL GENERATED ALWAYS AS (cardinality(terms)) STORED,
 lexemes tsvector NOT NULL GENERATED ALWAYS AS (array_to_tsvector(terms)) STORED);
CREATE INDEX IF NOT EXISTS facts_lexemes ON memcue.facts USING gin (lexemes) WHERE active;
CREATE INDEX IF NOT EXISTS facts_scope ON memcue.facts (scope) WHERE active;`

// postgresStore is one PostgreSQL handle: its configuration and the one connection its operations share. The
// connection opens with the handle and stays open for the next operation, so a command pays for TCP, TLS and
// authentication once; an operation whose budget runs out closes it, and the next operation opens a new one.
type postgresStore struct {
	config  *pgx.ConnConfig
	request context.Context
	cancel  context.CancelFunc
	mu      sync.Mutex
	// life bounds every connection of the handle; end cancels it on Close.
	life context.Context
	end  context.CancelFunc
	conn *pgx.Conn
	// release closes every stream the dialer of conn opened, pgx's cancellation streams included.
	release func()
	closed  bool
	// active is the context of the running operation. The dialer of conn opens streams only while it is live, so
	// pgx's asynchronous cleanup cannot reach the network after the operation's budget.
	active atomic.Pointer[context.Context]
}

func PostgresConfig(dsn string) (*pgx.ConnConfig, error) {
	invalid := errors.New("invalid MEMCUE_POSTGRES_DSN: use an explicit single-host PostgreSQL URL with user, database, port and sslmode=verify-full or disable")
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "PG") {
			return nil, errors.New("ambient PG variables are not supported with MEMCUE_POSTGRES_DSN; unset them")
		}
	}
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.User == nil || u.User.Username() == "" || u.Hostname() == "" || u.Port() == "" || u.Path == "" || u.Path == "/" || strings.Contains(u.Host, ",") || u.Fragment != "" {
		return nil, invalid
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return nil, invalid
	}
	if q.Get("sslmode") != "verify-full" && q.Get("sslmode") != "disable" {
		return nil, invalid
	}
	for k, v := range q {
		switch k {
		case "sslmode", "sslrootcert", "sslcert", "sslkey", "connect_timeout", "application_name":
		default:
			return nil, invalid
		}
		if len(v) != 1 {
			return nil, invalid
		}
	}
	// pgx otherwise discovers ~/.pgpass, even with a fully explicit endpoint.
	q.Set("passfile", "/dev/null")
	q.Set("connect_timeout", "10")
	u.RawQuery = q.Encode()
	cfg, err := pgx.ParseConfig(u.String())
	if err != nil || len(cfg.Fallbacks) != 0 || strings.HasPrefix(cfg.Host, "/") {
		return nil, invalid
	}
	cfg.RuntimeParams["application_name"] = "memcue"
	cfg.RuntimeParams["statement_timeout"] = "10000"
	cfg.RuntimeParams["lock_timeout"] = "10000"
	// One round trip per statement: the default mode first prepares and describes every new statement, and a
	// command's connection runs each of its statements once.
	cfg.DefaultQueryExecMode = pgx.QueryExecModeExec
	return cfg, nil
}

// budgetConfig also constrains pgx's asynchronous cancellation connections, which
// otherwise use a fresh background cleanup context after the caller has returned.
type operationConn struct {
	net.Conn
	once   sync.Once
	err    error
	closed func(*operationConn)
}

func (c *operationConn) Close() error {
	c.once.Do(func() {
		c.err = c.Conn.Close()
		if c.closed != nil {
			c.closed(c)
		}
	})
	return c.err
}

func budgetConfig(operation context.Context, config *pgx.ConnConfig) *pgx.ConnConfig {
	cfg, _ := ownedBudgetConfig(operation, config)
	return cfg
}

// The owner context owns every connection allocated by its dialer, including pgx's
// independently-contexted asynchronous CancelRequest streams. Shared transports
// are not closed: only the net.Conn returned for this owner is owned here.
func ownedBudgetConfig(operation context.Context, config *pgx.ConnConfig) (*pgx.ConnConfig, func()) {
	cfg := config.Copy()
	dial := cfg.DialFunc
	var mu sync.Mutex
	closed := false
	connections := map[*operationConn]bool{}
	forget := func(c *operationConn) {
		mu.Lock()
		delete(connections, c)
		mu.Unlock()
	}
	release := func() {
		mu.Lock()
		closed = true
		owned := make([]*operationConn, 0, len(connections))
		for c := range connections {
			owned = append(owned, c)
		}
		mu.Unlock()
		for _, c := range owned {
			_ = c.Close()
		}
	}
	stop := context.AfterFunc(operation, release)
	cfg.DialFunc = func(ctx context.Context, network, address string) (net.Conn, error) {
		if err := operation.Err(); err != nil {
			return nil, err
		}
		bounded, cancel := context.WithCancel(ctx)
		defer cancel()
		stopDial := context.AfterFunc(operation, cancel)
		defer stopDial()
		if deadline, ok := operation.Deadline(); ok {
			var clipped context.CancelFunc
			bounded, clipped = context.WithDeadline(bounded, deadline)
			defer clipped()
		}
		conn, err := dial(bounded, network, address)
		if err != nil {
			return nil, err
		}
		owned := &operationConn{Conn: conn, closed: forget}
		mu.Lock()
		if closed || operation.Err() != nil {
			mu.Unlock()
			_ = owned.Close()
			return nil, context.Canceled
		}
		connections[owned] = true
		mu.Unlock()
		return owned, nil
	}
	return cfg, func() { stop(); release() }
}

// OpenPostgres uses PostgreSQL as the sole authority. Every read and search runs on the server: nothing is copied
// into a local base, and there is no local fallback.
func OpenPostgres(dsn string) (*Store, error) { return openPostgres(context.Background(), dsn) }
func openPostgres(parent context.Context, dsn string) (*Store, error) {
	cfg, err := PostgresConfig(dsn)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	if err := tailnet.Configure(ctx, cfg); err != nil {
		return nil, err
	}
	// The handle, not one operation, owns its connection: it closes with Close or with the caller's context.
	life, end := context.WithCancel(parent)
	r := &postgresStore{config: cfg, life: life, end: end}
	host, _ := os.Hostname()
	s := &Store{remote: r, author: host, now: time.Now}
	if _, err := postgresOp(s, ctx, false, func(ctx context.Context, c *pgx.Conn) (struct{}, error) {
		return struct{}{}, ensureSchema(ctx, c)
	}); err != nil {
		r.close()
		return nil, err
	}
	return s, nil
}

// ensureSchema checks the schema with one statement. Only a database without it takes the lock and creates it, so
// an ordinary open needs no DDL rights and no write.
func ensureSchema(ctx context.Context, c *pgx.Conn) error {
	var version int
	err := c.QueryRow(ctx, `SELECT version FROM memcue.version WHERE singleton`).Scan(&version)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && (pgErr.Code == "42P01" || pgErr.Code == "3F000") {
		version, err = createSchema(ctx, c)
	}
	if err != nil {
		if errors.Is(err, ErrAmbiguous) || errors.Is(err, errIncompatible) {
			return err
		}
		return errRemote
	}
	switch version {
	case postgresSchemaVersion:
		return nil
	case 1:
		return ErrPostgresSchema1
	}
	return errors.New("incompatible PostgreSQL schema; update Memcue")
}

var errIncompatible = errors.New("incompatible PostgreSQL schema; operator repair required")

func createSchema(ctx context.Context, c *pgx.Conn) (int, error) {
	tx, err := c.Begin(ctx)
	if err != nil {
		return 0, errRemote
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(680681)`); err != nil {
		return 0, errRemote
	}
	var versionTable, factsTable bool
	if err = tx.QueryRow(ctx, `SELECT to_regclass('memcue.version') IS NOT NULL,to_regclass('memcue.facts') IS NOT NULL`).Scan(&versionTable, &factsTable); err != nil {
		return 0, errRemote
	}
	if versionTable != factsTable {
		return 0, errIncompatible
	}
	if !versionTable {
		if _, err = tx.Exec(ctx, postgresSchema); err != nil {
			return 0, errRemote
		}
	}
	var version int
	if err := tx.QueryRow(ctx, `SELECT version FROM memcue.version WHERE singleton`).Scan(&version); err != nil {
		return 0, errRemote
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, ErrAmbiguous
	}
	return version, nil
}

// postgresOp runs fn on the handle's connection under ctx, one operation at a time. It opens the connection when
// there is none. A budget that runs out closes the connection, so a statement cannot outlive it; the next operation
// opens a new one. A read that outlives its budget fails with the context's error, a write with ErrAmbiguous.
func postgresOp[T any](s *Store, ctx context.Context, write bool, fn func(context.Context, *pgx.Conn) (T, error)) (result T, err error) {
	r := s.remote
	r.mu.Lock()
	defer r.mu.Unlock()
	var zero T
	if r.closed {
		return zero, errors.New("PostgreSQL handle is closed")
	}
	if r.request != nil && r.request.Err() != nil {
		return zero, r.request.Err()
	}
	defer func() {
		if err == nil && ctx.Err() != nil {
			result = zero
			if write {
				err = ErrAmbiguous
			} else {
				err = ctx.Err()
			}
		}
		// A read that outlived its budget failed because of it, whatever the statement reported.
		if err != nil && !write && ctx.Err() != nil {
			result, err = zero, ctx.Err()
		}
	}()
	r.active.Store(&ctx)
	defer r.active.Store(nil)
	if r.conn != nil && r.conn.IsClosed() {
		r.drop(ctx)
	}
	if r.conn == nil {
		owner, cancel := context.WithCancel(r.life)
		dial, release := ownedBudgetConfig(owner, r.gated())
		c, e := pgx.ConnectConfig(ctx, dial)
		if e != nil {
			release()
			cancel()
			return zero, errRemote
		}
		r.conn, r.release = c, func() { release(); cancel() }
	}
	c := r.conn
	stop := context.AfterFunc(ctx, func() { _ = c.PgConn().Conn().Close() })
	result, err = fn(ctx, c)
	if !stop() || ctx.Err() != nil || c.IsClosed() {
		r.drop(ctx)
	}
	return result, err
}

// gated is the configuration whose dialer refuses outside a live operation and bounds each dial by it.
func (r *postgresStore) gated() *pgx.ConnConfig {
	cfg := r.config.Copy()
	dial := cfg.DialFunc
	cfg.DialFunc = func(ctx context.Context, network, address string) (net.Conn, error) {
		op := r.active.Load()
		if op == nil || (*op).Err() != nil {
			return nil, context.Canceled
		}
		bounded, cancel := context.WithCancel(ctx)
		defer cancel()
		stop := context.AfterFunc(*op, cancel)
		defer stop()
		return dial(bounded, network, address)
	}
	return cfg
}

// drop closes the connection and every stream its dialer opened. The caller holds mu.
func (r *postgresStore) drop(ctx context.Context) {
	if r.conn == nil {
		return
	}
	c := r.conn
	stop := context.AfterFunc(ctx, func() { _ = c.PgConn().Conn().Close() })
	_ = c.Close(ctx)
	stop()
	r.release()
	r.conn, r.release = nil, nil
}

func (r *postgresStore) close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.closed = true
	parent := context.Background()
	if r.request != nil {
		parent = r.request
	}
	ctx, cancel := context.WithTimeout(parent, time.Second)
	r.drop(ctx)
	cancel()
	r.end()
	if r.cancel != nil {
		r.cancel()
	}
}

// ErrRemoteUnsupported identifies operations that require a local file or a local maintenance workflow.
var ErrRemoteUnsupported = errors.New("operation unavailable for PostgreSQL; use PostgreSQL backup tools or explicitly unset MEMCUE_POSTGRES_DSN for local maintenance")

// OpenPostgresContext binds schema startup and every operation to the caller-owned request context; the handle's
// connection closes with Close or with that context. OpenPostgres gives each operation its own budget.
func OpenPostgresContext(ctx context.Context, dsn string) (*Store, error) {
	s, err := openPostgres(ctx, dsn)
	if err == nil {
		s.remote.request = ctx
	}
	return s, err
}

func (r *postgresStore) operation() (context.Context, context.CancelFunc) {
	parent := context.Background()
	if r.request != nil {
		parent = r.request
	}
	return context.WithTimeout(parent, 10*time.Second)
}

// OpenPostgresRequest owns a finite invocation context, including schema startup.
// Close cancels it; callers needing their own context use OpenPostgresContext.
func OpenPostgresRequest(dsn string, budget time.Duration) (*Store, error) {
	if budget <= 0 || budget > 10*time.Second {
		return nil, errors.New("invalid PostgreSQL request budget")
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	s, err := OpenPostgresContext(ctx, dsn)
	if err != nil {
		cancel()
		return nil, err
	}
	s.remote.cancel = cancel
	return s, nil
}

// Borrow returns a handle that shares this PostgreSQL handle's connection and whose Close leaves it open: the owner
// closes it. A command that opens the base several times thereby connects once. A local store is returned as is.
func (s *Store) Borrow() *Store {
	if s.remote == nil {
		return s
	}
	return &Store{remote: s.remote, author: s.author, now: s.now, borrowed: true}
}
