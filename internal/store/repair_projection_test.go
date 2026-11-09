package store

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"os"
	"testing"
	"time"
)

func TestRepairOperationCannotPublishAfterDeadline(t *testing.T) {
	dsn := os.Getenv("MEMCUE_REPAIR_DSN")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	s, err := OpenPostgres(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	c, err := pgx.Connect(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close(context.Background()) }()
	if _, err = c.Exec(context.Background(), "TRUNCATE memcue.facts"); err != nil {
		t.Fatal(err)
	}
	f, err := s.AddWith("synthetic operation budget", "fixture", "", Meta{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err = postgresOp(s, ctx, false, func(ctx context.Context, c *pgx.Conn) (Fact, error) {
		var id string
		if e := c.QueryRow(ctx, `SELECT id FROM memcue.facts WHERE id=$1`, f.ID).Scan(&id); e != nil {
			return Fact{}, e
		}
		time.Sleep(200 * time.Millisecond)
		return f, nil
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired operation published success instead of operation deadline: %v", err)
	}
	if _, err = s.Get(f.ID); err != nil {
		t.Fatalf("next operation did not reconnect after the expired one: %v", err)
	}
}
