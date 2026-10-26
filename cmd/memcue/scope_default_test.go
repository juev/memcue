package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/juev/memcue/internal/store"
)

// factByID reads the latest version of a fact.
func factByID(t *testing.T, id string) store.Fact {
	t.Helper()
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	f, err := s.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// A fact added without -scope holds in the project its origin names, and in no project it holds everywhere: an empty
// scope is in no place a search asks for (#442). A scope that is given stays.
func TestAddWithoutScopeTakesTheOrigin(t *testing.T) {
	dir := env(t)
	repo := filepath.Join(dir, "shop")
	key := gitRepo(t, repo)
	t.Chdir(repo)
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"факт репозитория"}, "repo:" + key},
		{[]string{"-origin", "_global 2026-10-01", "факт глобального tier"}, "global"},
		{[]string{"-cwd", "", "факт вне проекта"}, "global"},
		{[]string{"-scope", "host:db1", "факт хоста"}, "host:db1"},
	} {
		if got := factByID(t, addFactForTest(t, c.args...)).Scope; got != c.want {
			t.Errorf("add %q: scope %q, want %q", c.args, got, c.want)
		}
	}
	var out bytes.Buffer
	if err := runSearch([]string{"-in", "global", "-min-score", "0", "факт"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "вне проекта") {
		t.Errorf("-in global did not find the fact added outside a project: %q", out.String())
	}
}
