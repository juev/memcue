package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/juev/memcue/internal/store"
)

// The Claude config directory is a repository like any other (#617): a fact stored there without a scope holds in
// the config repository, and a search of a session there finds that repository's rules, which a search in another
// repository does not. A fact stored under the old global tier (origin _global) stays global and reaches both.
func TestClaudeConfigDirectoryIsARepository(t *testing.T) {
	dir := env(t)
	config := filepath.Join(dir, "claude")
	if err := os.MkdirAll(filepath.Join(config, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, ".git", "config"),
		[]byte("[remote \"origin\"]\n\turl = git@example.com:me/claude.git\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	shop := filepath.Join(dir, "shop")
	gitRepo(t, shop)

	t.Chdir(config)
	var out bytes.Buffer
	if err := runAdd([]string{"-kind", "rule", "-source", "user", "--",
		"Хук ревью конфига срабатывает на два триггера."}, &out); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	active, _ := s.Active()
	if len(active) != 1 || active[0].Scope != "repo:example.com/me/claude" {
		t.Fatalf("a fact stored in the config directory: %+v", active)
	}
	configRule := active[0].ID
	old, err := s.AddWith("Коммиты пишутся по-английски.", "_global 2026-10-01", "",
		store.Meta{Kind: "rule", Scope: "global", Source: "user"})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	for _, c := range []struct {
		cwd        string
		wantConfig bool
	}{{config, true}, {shop, false}} {
		t.Chdir(c.cwd)
		var found bytes.Buffer
		if err := runSearch([]string{"-kind", "rule", "хук ревью конфига"}, &found); err != nil {
			t.Fatal(err)
		}
		// The config rule is the config repository's: elsewhere it comes only through the widened search, marked as
		// a fact of another repository (#712).
		widened := strings.HasPrefix(found.String(), widenedHint+"\n")
		if !strings.Contains(found.String(), configRule) || widened == c.wantConfig {
			t.Errorf("search in %s printed %q: want the config rule, widened %v", filepath.Base(c.cwd), found.String(),
				!c.wantConfig)
		}
		found.Reset()
		if err := runSearch([]string{"-kind", "rule", "коммиты по-английски"}, &found); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(found.String(), old.ID) {
			t.Errorf("search in %s lost the global rule of the old tier", filepath.Base(c.cwd))
		}
	}
}
