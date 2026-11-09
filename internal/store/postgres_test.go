package store

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestPostgresFactSurvivesNewClient(t *testing.T) {
	dsn := os.Getenv("MEMCUE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires an isolated PostgreSQL fixture")
	}
	s, err := OpenPostgres(dsn)
	if err != nil {
		t.Fatalf("open isolated PostgreSQL: %v", err)
	}
	f, err := s.AddWith("Synthetic cedar PROJ-680 хранит историю", "fixture", "", Meta{Scope: "repo:fixture", Evidence: "synthetic evidence", Kind: "rule", Source: "user"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenPostgres(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	got, err := s.Get(f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got != f {
		t.Fatalf("remote history did not round-trip: got %#v want %#v", got, f)
	}
}

// PostgreSQL keeps the current version only (#700): a correction replaces it and keeps the metadata.
func TestPostgresCorrectionReplacesTheVersion(t *testing.T) {
	dsn := os.Getenv("MEMCUE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires an isolated PostgreSQL fixture")
	}
	s, err := OpenPostgres(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	f, err := s.AddWith("Synthetic original cedar", "fixture", "", Meta{Evidence: "synthetic evidence", Scope: "shelf:research"})
	if err != nil {
		t.Fatal(err)
	}
	revised, err := s.Correct(f.ID, "Synthetic revised cedar", f.Version)
	if err != nil {
		t.Fatalf("remote correction: %v", err)
	}
	h, err := s.History(f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(h) != 1 || h[0] != revised || revised.Version != 2 || revised.Evidence != f.Evidence || revised.Scope != f.Scope {
		t.Fatalf("history or metadata changed: %#v", h)
	}
}

func TestPostgresRecallTracksRetirement(t *testing.T) {
	dsn := os.Getenv("MEMCUE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires an isolated PostgreSQL fixture")
	}
	s, err := OpenPostgres(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	f, err := s.AddWith("Synthetic cedar PROJ-681", "fixture", "", Meta{Scope: "repo:recall681"})
	if err != nil {
		t.Fatal(err)
	}
	lookup := func(want int) {
		t.Helper()
		h, err := s.SearchFiltered([]string{"cedar"}, 100, Filter{Scopes: []string{"repo:recall681"}})
		if err != nil {
			t.Fatalf("remote recall must reflect history: %v", err)
		}
		found := 0
		for _, hit := range h {
			if hit.ID == f.ID {
				found++
			}
		}
		if found != want {
			t.Fatalf("recalled %d copies, want %d", found, want)
		}
	}
	lookup(1)
	if _, err = s.Retire(f.ID, f.Version); err != nil {
		t.Fatal(err)
	}
	lookup(0)
	if _, err = s.Restore(f.ID); err != nil {
		t.Fatal(err)
	}
	lookup(1)
}

func TestPostgresLabelsRejectStaleVersion(t *testing.T) {
	dsn := os.Getenv("MEMCUE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires isolated PostgreSQL")
	}
	s, err := OpenPostgres(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	f, err := s.AddWith("Synthetic label fixture", "fixture", "", Meta{})
	if err != nil {
		t.Fatal(err)
	}
	next, err := s.Relabel(f.ID, f.Version, "rule", "shelf:labels", "2099-01-01")
	if err != nil {
		t.Fatalf("remote label must persist: %v", err)
	}
	if _, err = s.Relabel(f.ID, f.Version, "pitfall", "repo:wrong", ""); !errors.Is(err, ErrChanged) {
		t.Fatalf("stale update: %v", err)
	}
	got, err := s.Get(f.ID)
	if err != nil || got != next {
		t.Fatalf("stale update changed fact: %#v %v", got, err)
	}
}

func TestPostgresReadersAgreeOnStoredHistory(t *testing.T) {
	dsn := os.Getenv("MEMCUE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires isolated PostgreSQL")
	}
	s, err := OpenPostgres(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	f, err := s.AddWith("Synthetic reader sequoia", "readerfixture 2026-11-07", "", Meta{Scope: "repo:readerfixture"})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.Rows()
	if err != nil {
		t.Fatalf("history readers must work remotely: %v", err)
	}
	found := false
	for _, r := range rows {
		if r == f {
			found = true
		}
	}
	if !found {
		t.Fatal("history omitted fact")
	}
	live, err := s.Active()
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, r := range live {
		if r == f {
			found = true
		}
	}
	if !found {
		t.Fatal("current omitted fact")
	}
	scopes, err := s.RepoScopes()
	if err != nil || !slices.Contains(scopes, "repo:readerfixture") {
		t.Fatalf("scope reader: %v %v", scopes, err)
	}
	origins, err := s.OriginTiers()
	if err != nil || !slices.Contains(origins, "readerfixture") {
		t.Fatalf("origin reader: %v %v", origins, err)
	}
	df, n, err := s.DocFreq([]string{"sequoia"})
	if err != nil || df["sequoia"] < 1 || n < 1 {
		t.Fatalf("document reader: %v %d %v", df, n, err)
	}
	count, err := s.CountNamed("readerfixture")
	if err != nil || count < 1 {
		t.Fatalf("count reader: %d %v", count, err)
	}
	if _, err = s.IndexStats(); !errors.Is(err, ErrRemoteUnsupported) {
		t.Fatalf("index statistics belong to local reindex: %v", err)
	}
}

func TestPostgresRejectsLocalFileMaintenanceBeforeOpeningFiles(t *testing.T) {
	dsn := os.Getenv("MEMCUE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires isolated PostgreSQL")
	}
	s, err := OpenPostgres(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	path := filepath.Join(t.TempDir(), "must-not-exist.db")
	func() { defer func() { _ = recover() }(); _, err = s.Merge(path) }()
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatal("remote merge opened and created a local base before refusal")
	}
	if !errors.Is(err, ErrRemoteUnsupported) {
		t.Fatalf("file merge should explain remote mode: %v", err)
	}
	if err = s.Backup("this-file-must-not-be-created.db"); !errors.Is(err, ErrRemoteUnsupported) {
		t.Fatalf("backup should explain remote mode: %v", err)
	}
}

func TestPostgresRejectsMalformedURLQuery(t *testing.T) {
	_, err := PostgresConfig("postgres://fixture:synthetic-secret@database.invalid:5432/fixture?sslmode=disable&application_name=%ZZ")
	if err == nil {
		t.Fatal("malformed explicit PostgreSQL query was silently ignored")
	}
	if strings.Contains(err.Error(), "synthetic-secret") {
		t.Fatal("DSN secret leaked")
	}
}
