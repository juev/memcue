package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/juev/memcue/internal/extract"
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

// An extracted fact the model left without a scope gets the one its origin names (#442).
func TestExtractedFactWithoutScopeTakesTheOrigin(t *testing.T) {
	env(t)
	old := extractFn
	extractFn = func(string, string) (extracted, error) {
		return extracted{Facts: []extract.Fact{{Content: "Решили держать очередь acme-jobs в Redis.", Kind: "decision",
			Source: "user", Evidence: "держим очередь в redis"}}}, nil
	}
	t.Cleanup(func() { extractFn = old })
	msgs := `[{"role":"user","content":"держим очередь в redis"}]`
	if err := runExtractMessages([]string{"-origin", "example.com/team/widget 2026-10-26", "-min-chars", "0"},
		strings.NewReader(msgs)); err != nil {
		t.Fatal(err)
	}
	got := scopesByContent(t)
	if sc := got["Решили держать очередь acme-jobs в Redis."]; sc != "repo:example.com/team/widget" {
		t.Errorf("stored %v, want the repository of the origin", got)
	}
}

// The vet pass gives a live fact stored without a scope the one its origin names; a dry run only lists it (#442).
func TestVetScopesTheUnscopedFacts(t *testing.T) {
	env(t)
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	repoFact, err := s.AddWith("Решение репозитория без scope.", nil, "example.com/team/widget 2026-10-22", "",
		store.Meta{Kind: "decision"})
	if err != nil {
		t.Fatal(err)
	}
	// A migrated note keeps its file after the date; the origin still names its project.
	noteFact, err := s.AddWith("Заметка репозитория без scope.", nil, "example.com/team/widget 2026-10-24 (widget/note.md)",
		"", store.Meta{})
	if err != nil {
		t.Fatal(err)
	}
	globalFact, err := s.AddWith("Глобальное правило без scope.", nil, "_global 2026-10-22", "", store.Meta{})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	var out bytes.Buffer
	if err := runVet([]string{"-no-model", "-dry-run"}, &out); err != nil {
		t.Fatal(err)
	}
	if f := factByID(t, repoFact.ID); f.Scope != "" || !strings.Contains(out.String(), "Фактов без scope 3") {
		t.Errorf("dry run: scope %q, report:\n%s", f.Scope, out.String())
	}
	out.Reset()
	if err := runVet([]string{"-no-model"}, &out); err != nil {
		t.Fatal(err)
	}
	if f := factByID(t, repoFact.ID); f.Scope != "repo:example.com/team/widget" || f.Kind != "decision" {
		t.Errorf("repository fact: %+v", f)
	}
	if f := factByID(t, noteFact.ID); f.Scope != "repo:example.com/team/widget" {
		t.Errorf("migrated note: %+v", f)
	}
	if f := factByID(t, globalFact.ID); f.Scope != "global" {
		t.Errorf("global fact: %+v", f)
	}
	out.Reset()
	if err := runVet([]string{"-no-model"}, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "Фактов без scope") {
		t.Errorf("a second run scoped again:\n%s", out.String())
	}
}
