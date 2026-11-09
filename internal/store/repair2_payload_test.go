package store

import (
	"context"
	"github.com/jackc/pgx/v5"
	"os"
	"testing"
)

func TestRepair2NativeGetValidatesStoredVersion(t *testing.T) {
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
	if _, err = c.Exec(context.Background(), `TRUNCATE memcue.facts; INSERT INTO memcue.facts(id,version,payload,terms) VALUES ('malformed',1,'{"ID":"malformed","Version":2,"Content":"synthetic"}','{}')`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Get("malformed"); err == nil {
		t.Fatal("native lookup accepted payload with mismatched immutable version")
	}
}
