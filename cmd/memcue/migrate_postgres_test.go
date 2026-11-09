package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/juev/memcue/internal/store"
)

// emptyPostgresBase creates a database of its own on the isolated fixture and drops it after the test.
func emptyPostgresBase(t *testing.T) string {
	t.Helper()
	admin := os.Getenv("MEMCUE_TEST_POSTGRES_DSN")
	if admin == "" {
		t.Skip("requires an isolated PostgreSQL fixture whose user may create databases")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close(ctx) }()
	name := fmt.Sprintf("memcue_cli_copy_%d", time.Now().UnixNano())
	if _, err := c.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Skipf("fixture user cannot create databases: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		c, err := pgx.Connect(ctx, admin)
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = c.Close(ctx) }()
		if _, err := c.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
	})
	u, err := url.Parse(admin)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	return u.String()
}

func TestMigratesToPostgresIsOnlyMigrateWithTheFlag(t *testing.T) {
	for _, c := range []struct {
		args []string
		want bool
	}{
		{[]string{"migrate", "-postgres"}, true},
		{[]string{"migrate", "-postgres", "-apply"}, true},
		{[]string{"migrate", "-apply", "--postgres"}, true},
		{[]string{"migrate", "-postgres=true"}, true},
		{[]string{"migrate"}, false},
		{[]string{"migrate", "-all"}, false},
		{[]string{"migrate", "-postgres=false"}, false},
		{[]string{"migrate", "--", "-postgres"}, false},
		{[]string{"import", "-postgres"}, false},
		{[]string{"search", "-postgres"}, false},
	} {
		if got := migratesToPostgres(c.args); got != c.want {
			t.Errorf("%q: %v, want %v", c.args, got, c.want)
		}
	}
}

// Under a DSN every other form of migrate still refuses, even if it reaches the command past the gate.
func TestMigrateWithoutPostgresStillRefusesUnderADSN(t *testing.T) {
	env(t)
	t.Setenv("MEMCUE_POSTGRES_DSN", "postgres://u@127.0.0.1:1/db?sslmode=disable")
	inv := &invocation{ctx: context.Background(), stop: func() {}}
	for _, args := range [][]string{{"-all"}, {"-text"}, {"-repos"}, {"note.md"}} {
		err := inv.runMigrate(args, &strings.Builder{})
		if err == nil || !strings.Contains(err.Error(), "unavailable for PostgreSQL") {
			t.Errorf("migrate %q under a DSN: %v", args, err)
		}
	}
}

func TestMigratePostgresNeedsADSNAndTakesOnlyApply(t *testing.T) {
	env(t)
	t.Setenv("MEMCUE_POSTGRES_DSN", "")
	inv := &invocation{ctx: context.Background(), stop: func() {}}
	if err := inv.runMigrate([]string{"-postgres"}, &strings.Builder{}); err == nil || !strings.Contains(err.Error(), "MEMCUE_POSTGRES_DSN") {
		t.Errorf("without a DSN: %v", err)
	}
	for _, args := range [][]string{{"-postgres", "-text"}, {"-postgres", "-all"}, {"-postgres", "-dry-run"}, {"-postgres", "note.md"}} {
		if err := inv.runMigrate(args, &strings.Builder{}); err == nil || !strings.Contains(err.Error(), "takes only -apply") {
			t.Errorf("migrate %q: %v", args, err)
		}
	}
}

// A missing or empty local base is not a source: the copy would otherwise succeed with no facts.
func TestMigratePostgresRefusesAMissingOrEmptySource(t *testing.T) {
	dir := env(t)
	t.Setenv("MEMCUE_POSTGRES_DSN", "postgres://u@127.0.0.1:1/db?sslmode=disable")
	missing := filepath.Join(dir, "absent-history")
	t.Setenv("MEMCUE_FACTS", missing)
	inv := &invocation{ctx: context.Background(), stop: func() {}}
	if err := inv.runMigrate([]string{"-postgres"}, &strings.Builder{}); err == nil || !strings.Contains(err.Error(), "no local fact base") {
		t.Errorf("missing base: %v", err)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Errorf("the refusal wrote a base: %v", err)
	}
	empty := filepath.Join(dir, "empty.db")
	s, err := store.Open(empty)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	t.Setenv("MEMCUE_FACTS", empty)
	if err := inv.runMigrate([]string{"-postgres"}, &strings.Builder{}); err == nil || !strings.Contains(err.Error(), "holds no facts") {
		t.Errorf("empty base: %v", err)
	}
}

// The report writes nothing, -apply copies the whole history, and a second -apply refuses the base now in use.
func TestMigratePostgresReportsThenCopies(t *testing.T) {
	env(t)
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.AddWith("Synthetic fact copied to PostgreSQL", "fixture", "", store.Meta{Kind: "event"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Correct(a.ID, "Synthetic fact copied to PostgreSQL, corrected", a.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddWith("Second synthetic fact", "fixture", "", store.Meta{Kind: "rule"}); err != nil {
		t.Fatal(err)
	}
	local, err := s.Rows()
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	dsn := emptyPostgresBase(t)
	t.Setenv("MEMCUE_POSTGRES_DSN", dsn)
	inv := &invocation{ctx: context.Background(), stop: func() {}}

	var report strings.Builder
	if err := inv.runMigrate([]string{"-postgres"}, &report); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"2 facts, 3 versions", "no memcue schema yet", "nothing written"} {
		if !strings.Contains(report.String(), want) {
			t.Errorf("report lacks %q:\n%s", want, report.String())
		}
	}
	u, _ := url.Parse(dsn)
	if pw, ok := u.User.Password(); ok && strings.Contains(report.String(), pw) {
		t.Fatal("the report shows the password")
	}
	if state, err := store.InspectPostgres(context.Background(), dsn); err != nil || state.Schema {
		t.Fatalf("the report wrote to PostgreSQL: %+v %v", state, err)
	}

	var copied strings.Builder
	if err := inv.runMigrate([]string{"-postgres", "-apply"}, &copied); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(copied.String(), "copied 3 versions of 2 facts") {
		t.Errorf("apply output:\n%s", copied.String())
	}
	remote, err := inv.openBase()
	if err != nil {
		t.Fatal(err)
	}
	got, err := remote.Rows()
	_ = remote.Close()
	if err != nil || len(got) != len(local) {
		t.Fatalf("PostgreSQL holds %d versions (%v), want %d", len(got), err, len(local))
	}
	for i := range local {
		if got[i].ID != local[i].ID || got[i].Version != local[i].Version || got[i].Content != local[i].Content || got[i].WriteID != local[i].WriteID {
			t.Errorf("version %d: got %+v want %+v", i, got[i], local[i])
		}
	}
	if err := inv.runMigrate([]string{"-postgres", "-apply"}, &strings.Builder{}); !errors.Is(err, store.ErrTargetNotEmpty) {
		t.Fatalf("second copy: %v", err)
	}
}
