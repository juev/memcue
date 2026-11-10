package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExplicitInvalidPostgresNeverOpensLocalBase(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "must-not-exist")
	t.Setenv("MEMCUE_FACTS", dir)
	t.Setenv("MEMCUE_POSTGRES_DSN", "postgres://secret:secret@%broken")
	s, err := openBase()
	if s != nil {
		_ = s.Close()
	}
	if err == nil {
		t.Fatal("invalid explicit PostgreSQL configuration silently selected local store")
	}
	if strings.Contains(err.Error(), "secret") {
		t.Fatal("DSN leaked")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("local base changed: %v", err)
	}
}

func TestEmbeddedPostgresRequiresExistingHelper(t *testing.T) {
	dsn := os.Getenv("MEMCUE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires isolated PostgreSQL")
	}
	dir := filepath.Join(t.TempDir(), "identity")
	t.Setenv("MEMCUE_POSTGRES_DSN", dsn)
	t.Setenv("MEMCUE_TAILSCALE_MODE", "embedded")
	t.Setenv("MEMCUE_TAILSCALE_STATE_DIR", dir)
	s, err := openBase()
	if s != nil {
		_ = s.Close()
	}
	if err == nil {
		t.Fatal("embedded request silently used ordinary host networking without helper")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("ordinary tool call created or enrolled a node")
	}
}

func TestRemoteCommandMatrixProtectsLocalMaintenance(t *testing.T) {
	t.Setenv("MEMCUE_POSTGRES_DSN", "invalid-explicit-remote")
	for _, name := range []string{"migrate", "import", "install", "notes", "dream", "reindex"} {
		if err := remoteCommandAllowed(name); err == nil {
			t.Fatalf("%s could touch local state under a remote selection", name)
		}
	}
	for _, name := range []string{"search", "add", "correct", "retire", "reopen", "facts", "session-start", "session-card", "sessions", "dump", "merge", "connect", "version"} {
		if err := remoteCommandAllowed(name); err != nil {
			t.Fatalf("supported or explicit-file command %s rejected: %v", name, err)
		}
	}
}

func TestRemoteRepositorySnapshotDoesNotPretendBaseIsAbsent(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("MEMCUE_FACTS", filepath.Join(t.TempDir(), "missing"))
	t.Setenv("MEMCUE_POSTGRES_DSN", "invalid-explicit-remote")
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	header, _ := repositoryPitfallParts(cwd)
	if header != repositoryPitfallsUnavailable {
		t.Fatalf("remote failure became a false empty snapshot: %q", header)
	}
}

func TestRemoteHooksNeverStartLocalDreamMaintenance(t *testing.T) {
	t.Setenv("MEMCUE_POSTGRES_DSN", "explicit-remote")
	t.Setenv("MEMCUE_DREAM_BACKGROUND", "")
	previous := lookClaude
	lookClaude = func() bool { return true }
	defer func() { lookClaude = previous }()
	if dreamBackground() {
		t.Fatal("remote hook could spawn unsupported maintenance against an implicit local base")
	}
}

func TestRemoteSessionStateIsNamespacedWithoutPassword(t *testing.T) {
	t.Setenv("MEMCUE_STATE_DIR", "")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("MEMCUE_POSTGRES_DSN", "postgres://fixture:first@db.invalid:5432/one?sslmode=disable")
	first, err := stateDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("MEMCUE_POSTGRES_DSN", "postgres://fixture:rotated@db.invalid:5432/one?sslmode=disable")
	rotated, err := stateDir()
	if err != nil || first != rotated {
		t.Fatal("credential rotation changed backend state namespace")
	}
	t.Setenv("MEMCUE_POSTGRES_DSN", "postgres://fixture:rotated@db.invalid:5432/two?sslmode=disable")
	other, err := stateDir()
	if err != nil || first == other {
		t.Fatal("different PostgreSQL databases share session/receipt state")
	}
}
