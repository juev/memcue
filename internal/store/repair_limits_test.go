package store

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRepairRowCapRejectsAcknowledgement(t *testing.T) {
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
	if _, err = c.Exec(context.Background(), `TRUNCATE memcue.facts; INSERT INTO memcue.facts(id,version,payload,terms) SELECT 'synthetic-retired-'||i,1,jsonb_build_object('ID','synthetic-retired-'||i,'Version',1,'Content','synthetic','Created','2026-01-01T00:00:00Z','Status','retired'),'{}' FROM generate_series(1,100000) AS i`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddWith("synthetic boundary write", "fixture", "", Meta{}); err == nil {
		t.Fatal("mutation acknowledged a base exceeding the row cap")
	}
	var n int
	if err = c.QueryRow(context.Background(), `SELECT count(*) FROM memcue.facts`).Scan(&n); err != nil || n != 100000 {
		t.Fatalf("refusal changed committed history: %d %v", n, err)
	}
	if _, err = s.Get("synthetic-retired-1"); err != nil {
		t.Fatalf("exact boundary history became unreadable: %v", err)
	}
}

// Supplemental exact-byte and multi-input controls on the frozen transactional cap fix.
func TestRepairByteCapBatchRefusal(t *testing.T) {
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
	_, err = c.Exec(context.Background(), `TRUNCATE memcue.facts; INSERT INTO memcue.facts(id,version,payload,terms) SELECT 'byte-fixture-'||i,1,jsonb_build_object('ID','byte-fixture-'||i,'Version',1,'Content','synthetic','Created','2026-01-01T00:00:00Z','Status','retired','Evidence',repeat('x',65000)),'{}' FROM generate_series(1,1000) i;
 UPDATE memcue.facts SET payload=jsonb_set(payload,'{Evidence}',to_jsonb(repeat('x',65000+(67108864-(SELECT sum(octet_length(payload::text)) FROM memcue.facts))::int))) WHERE id='byte-fixture-1000'`)
	if err != nil {
		t.Fatal(err)
	}
	var before int64
	if err = c.QueryRow(context.Background(), `SELECT sum(octet_length(payload::text)) FROM memcue.facts`).Scan(&before); err != nil || before != 64<<20 {
		t.Fatalf("byte fixture precondition: %d %v", before, err)
	}
	if _, err = s.Get("byte-fixture-1"); err != nil {
		t.Fatalf("exact byte cap unreadable: %v", err)
	}
	if _, err = s.AddBatch([]FactInput{{Content: "synthetic one"}, {Content: "synthetic two"}}); err == nil {
		t.Fatal("over-byte-cap batch acknowledged")
	}
	var after int64
	var count int
	if err = c.QueryRow(context.Background(), `SELECT count(*),sum(octet_length(payload::text)) FROM memcue.facts`).Scan(&count, &after); err != nil || count != 1000 || after != before {
		t.Fatalf("batch refusal changed history: %d %d %v", count, after, err)
	}
}

// A correction replaces its row (#700), so at the row cap it is accepted, while an add that would grow the base is
// refused whole.
func TestRepairRowCapCorrectionRefusal(t *testing.T) {
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
	_, err = c.Exec(context.Background(), `TRUNCATE memcue.facts; INSERT INTO memcue.facts(id,version,payload,terms) SELECT CASE WHEN i=100000 THEN 'row-fixture' ELSE 'row-fixture-'||i END,1,jsonb_build_object('ID',CASE WHEN i=100000 THEN 'row-fixture' ELSE 'row-fixture-'||i END,'Version',1,'Content','synthetic','Created','2026-01-01T00:00:00Z','Status',CASE WHEN i=100000 THEN 'active' ELSE 'retired' END),'{}' FROM generate_series(1,100000) i`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Correct("row-fixture", "synthetic correction", 1); err != nil {
		t.Fatalf("a correction does not grow the base and must pass at the row cap: %v", err)
	}
	if _, err = s.AddBatch([]FactInput{{Content: "synthetic one"}, {Content: "synthetic two"}}); err == nil || !strings.Contains(err.Error(), "client's limit") {
		t.Fatalf("batch must refuse specifically at cap: %v", err)
	}
	got, err := s.Get("row-fixture")
	if err != nil || got.Version != 2 || got.Content != "synthetic correction" {
		t.Fatalf("refused batch changed the corrected fact: %#v %v", got, err)
	}
	var n int
	if err = c.QueryRow(context.Background(), `select count(*) from memcue.facts`).Scan(&n); err != nil || n != 100000 {
		t.Fatalf("partial batch/correction: %d %v", n, err)
	}
}

func TestRepairExactByteBoundaryWriteThenCorrectionRefusal(t *testing.T) {
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
	fixed := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return fixed }
	probe := Fact{ID: strings.Repeat("0", 26), Version: 1, Content: "synthetic byte boundary", Origin: "fixture", Created: fixed, Status: Active, Author: s.author, Meta: approved(Meta{})}
	payload, _ := json.Marshal(probe)
	var delta int64
	if err = c.QueryRow(context.Background(), "select octet_length($1::jsonb::text)", payload).Scan(&delta); err != nil {
		t.Fatal(err)
	}
	_, err = c.Exec(context.Background(), `TRUNCATE memcue.facts; INSERT INTO memcue.facts(id,version,payload,terms) SELECT 'byte-fixture-'||i,1,jsonb_build_object('ID','byte-fixture-'||i,'Version',1,'Content','synthetic','Created','2026-01-01T00:00:00Z','Status','retired','Evidence',repeat('x',65000)),'{}' FROM generate_series(1,1000) i`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Exec(context.Background(), `UPDATE memcue.facts SET payload=jsonb_set(payload,'{Evidence}',to_jsonb(repeat('x',65000+($1-(SELECT sum(octet_length(payload::text)) FROM memcue.facts))::int))) WHERE id='byte-fixture-1000'`, int64(64<<20)-delta)
	if err != nil {
		t.Fatal(err)
	}
	f, err := s.AddWith(probe.Content, probe.Origin, "", Meta{})
	if err != nil {
		t.Fatalf("write exactly at byte cap rejected: %v", err)
	}
	var size int64
	if err = c.QueryRow(context.Background(), `select sum(octet_length(payload::text)) from memcue.facts`).Scan(&size); err != nil || size != 64<<20 {
		t.Fatalf("exact-byte write size=%d err=%v", size, err)
	}
	if _, err = s.Correct(f.ID, "synthetic rejected correction", f.Version); err == nil || !strings.Contains(err.Error(), "client's limit") {
		t.Fatalf("byte-cap correction did not refuse specifically: %v", err)
	}
	got, err := s.Get(f.ID)
	if err != nil || got != f {
		t.Fatalf("refused correction damaged accepted history: %#v %v", got, err)
	}
}
