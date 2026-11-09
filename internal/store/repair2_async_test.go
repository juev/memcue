package store

import (
	"context"
	"io"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

type repair2FaultConn struct {
	net.Conn
	fail atomic.Bool
}

func (c *repair2FaultConn) Write(b []byte) (int, error) {
	if strings.Contains(string(b), "repair2_async") {
		c.fail.Store(true)
	}
	return c.Conn.Write(b)
}
func (c *repair2FaultConn) Read(b []byte) (int, error) {
	if c.fail.Load() {
		return 0, io.ErrUnexpectedEOF
	}
	return c.Conn.Read(b)
}

// Supplemental: actual pgx asyncClose opens its cancellation stream before the
// native operation callback returns. Neither its 15s cleanup context nor pending
// write/response can extend the owning operation's lifetime.
func TestRepair2ActualAsyncCancellationOwnedUntilReturn(t *testing.T) {
	dsn := os.Getenv("MEMCUE_REPAIR_DSN")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	for _, response := range []bool{false, true} {
		t.Run(map[bool]string{false: "packet-write", true: "response-read"}[response], func(t *testing.T) {
			s, err := OpenPostgres(dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = s.Close() }()
			cfg := s.remote.config.Copy()
			dial := cfg.DialFunc
			var calls atomic.Int32
			peers := make(chan net.Conn, 1)
			cfg.DialFunc = func(ctx context.Context, n, a string) (net.Conn, error) {
				if calls.Add(1) == 1 {
					c, e := dial(ctx, n, a)
					if e != nil {
						return nil, e
					}
					return &repair2FaultConn{Conn: c}, nil
				}
				x, y := net.Pipe()
				peers <- y
				return x, nil
			}
			useConfig(s, cfg)
			var peer net.Conn
			var cleanup <-chan struct{}
			_, err = remoteRead(s, func(ctx context.Context, c *pgx.Conn) (int, error) {
				cleanup = c.PgConn().CleanupDone()
				_, e := c.Exec(ctx, `SELECT 'repair2_async'`)
				if e == nil {
					t.Fatal("real pgx I/O fault missing")
				}
				select {
				case peer = <-peers:
				case <-time.After(time.Second):
					t.Fatal("pgx asyncClose did not allocate cancellation stream")
				}
				if response {
					buf := make([]byte, 16)
					if _, e = io.ReadFull(peer, buf); e != nil {
						t.Fatal(e)
					}
				}
				return 0, errRemote
			})
			if err == nil {
				t.Fatal("native transaction hid I/O failure")
			}
			if peer == nil {
				t.Fatal("missing cancellation peer")
			}
			defer func() { _ = peer.Close() }()
			_ = peer.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))
			_, err = peer.Write([]byte{1})
			if ne, ok := err.(net.Error); err == nil || ok && ne.Timeout() {
				t.Errorf("native operation returned with live async stream: %v", err)
			}
			select {
			case <-cleanup:
			case <-time.After(time.Second):
				t.Fatal("actual pgx async cleanup did not terminate")
			}
			foreign, err := OpenPostgres(dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = foreign.Close() }()
			if _, err = foreign.Current(); err != nil {
				t.Fatalf("separate owner was affected: %v", err)
			}
		})
	}
}
