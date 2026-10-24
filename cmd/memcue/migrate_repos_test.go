package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/juev/memcue/internal/store"
)

// migrate -repos maps the names facts were written under onto this machine's project keys: a directory's name from a
// clone under a root or in a transcript's cwd, an owner/name scope by its suffix, an explicit old=new pair for a key
// that changed. A name it cannot map stays and is reported; a fact already under a key is left alone. Without -apply
// nothing is written; with it the base is backed up first and each change is a new version that keeps the creation
// time.
func TestMigrateReposMovesFactsOntoKeys(t *testing.T) {
	dir := env(t)
	root := filepath.Join(dir, "src")
	clone := filepath.Join(root, "local-name")
	key := gitRepo(t, clone) // example.com/team/local-name
	other := filepath.Join(dir, "elsewhere", "seen-in-a-session")
	otherKey := gitRepo(t, other)
	write(t, filepath.Join(dir, "claude", "projects", "-x", "s.jsonl"),
		`{"type":"user","cwd":"`+other+`","message":{"content":"hi"}}`+"\n")

	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	add := func(content, origin, scope string) store.Fact {
		f, err := s.AddWith(content, nil, origin, "", store.Meta{Kind: "decision", Scope: scope})
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	byName := add("Решение под именем каталога.", "local-name 2026-10-01", "repo:local-name")
	byOwner := add("Решение с owner/name.", "", "repo:team/local-name")
	bySession := add("Решение из сессии.", "seen-in-a-session 2026-10-02", "")
	renamed := add("Решение под старым ключом.", "example.com/team/old 2026-10-03", "repo:example.com/team/old")
	gone := add("Решение репозитория, которого здесь нет.", "gone 2026-10-04", "repo:gone")
	done := add("Решение уже под ключом.", key+" 2026-10-05", "repo:"+key)
	_ = s.Close()

	var out bytes.Buffer
	pairs := []string{"example.com/team/old=example.com/team/new"}
	if err := runMigrate(append([]string{"-repos", "-root", root}, pairs...), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "to change 4") || !strings.Contains(out.String(), "gone 2") {
		t.Errorf("dry run report:\n%s", out.String())
	}
	if s, err = store.Open(factsPath()); err != nil {
		t.Fatal(err)
	}
	if f, _ := s.Get(byName.ID); f.Version != 1 {
		t.Errorf("dry run wrote a version: %+v", f)
	}
	_ = s.Close()

	out.Reset()
	if err := runMigrate(append([]string{"-repos", "-apply", "-root", root}, pairs...), &out); err != nil {
		t.Fatal(err)
	}
	backups, _ := filepath.Glob(filepath.Join(dir, "state", "memcue", "backup", "facts.db.pre-repos-*"))
	if len(backups) != 1 {
		t.Fatalf("backups %v\n%s", backups, out.String())
	}
	if s, err = store.Open(factsPath()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	for _, tc := range []struct {
		before        store.Fact
		origin, scope string
		version       int
	}{
		{byName, key + " 2026-10-01", "repo:" + key, 2},
		{byOwner, "", "repo:" + key, 2},
		{bySession, otherKey + " 2026-10-02", "", 2},
		{renamed, "example.com/team/new 2026-10-03", "repo:example.com/team/new", 2},
		{gone, "gone 2026-10-04", "repo:gone", 1},
		{done, key + " 2026-10-05", "repo:" + key, 1},
	} {
		f, err := s.Get(tc.before.ID)
		if err != nil {
			t.Fatal(err)
		}
		if f.Origin != tc.origin || f.Scope != tc.scope || f.Version != tc.version || !f.Created.Equal(tc.before.Created) {
			t.Errorf("%s: origin %q scope %q v%d created %v; want %q %q v%d created %v", tc.before.Content, f.Origin,
				f.Scope, f.Version, f.Created, tc.origin, tc.scope, tc.version, tc.before.Created)
		}
	}
	if old, err := store.Open(backups[0]); err != nil {
		t.Fatal(err)
	} else {
		f, _ := old.Get(byName.ID)
		_ = old.Close()
		if f.Origin != "local-name 2026-10-01" {
			t.Errorf("backup holds %q, want the base before the migration", f.Origin)
		}
	}
	if _, err := os.Stat(backups[0]); err != nil {
		t.Fatal(err)
	}
}

// Two clones that had the same directory name and now have different keys cannot be told apart: the name is reported
// as ambiguous and left alone, unless an old=new pair settles it.
func TestMigrateReposLeavesAnAmbiguousName(t *testing.T) {
	dir := env(t)
	gitRepo(t, filepath.Join(dir, "a", "same"))
	b := filepath.Join(dir, "b", "same")
	if err := os.MkdirAll(filepath.Join(b, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b, ".git", "config"),
		[]byte("[remote \"origin\"]\n\turl = git@example.org:other/same.git\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	f, _ := s.AddWith("Решение одного из двух.", nil, "same 2026-10-01", "", store.Meta{Kind: "decision"})
	_ = s.Close()
	var out bytes.Buffer
	if err := runMigrate([]string{"-repos", "-apply", "-root", filepath.Join(dir, "a"), "-root", filepath.Join(dir, "b")},
		&out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "ambiguous") || !strings.Contains(out.String(), "to change 0") {
		t.Errorf("report:\n%s", out.String())
	}
	s, _ = store.Open(factsPath())
	defer func() { _ = s.Close() }()
	if got, _ := s.Get(f.ID); got.Origin != "same 2026-10-01" {
		t.Errorf("ambiguous name rewritten: %q", got.Origin)
	}
}
