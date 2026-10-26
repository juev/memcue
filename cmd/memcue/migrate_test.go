package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/juev/memcue/internal/extract"
	"github.com/juev/memcue/internal/store"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func refs(ns []note) []string {
	var out []string
	for _, n := range ns {
		out = append(out, n.ref())
	}
	sort.Strings(out)
	return out
}

func TestFindNotesInBothLayouts(t *testing.T) {
	dir := t.TempDir()
	claude := filepath.Join(dir, "claude")
	repo := filepath.Join(dir, "src", "lists")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	// Built-in layout: the project directory name does not say which repository it is; its transcript does.
	proj := filepath.Join(claude, "projects", "-x-src-lists")
	write(t, filepath.Join(proj, "s.jsonl"), `{"type":"user","cwd":"`+repo+`","message":{"content":"hi"}}`+"\n")
	write(t, filepath.Join(proj, "memory", "MEMORY.md"), "- [a](a.md)\n")
	write(t, filepath.Join(proj, "memory", "a.md"), "note a\n")
	// Shared layout: notes kept in memory/<tier> and linked into a project.
	write(t, filepath.Join(claude, "memory", "_global", "g.md"), "note g\n")
	write(t, filepath.Join(claude, "memory", "perigee", "p.md"), "note p\n")
	if err := os.MkdirAll(filepath.Join(claude, "projects", "-x-perigee"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(claude, "memory", "perigee"), filepath.Join(claude, "projects", "-x-perigee", "memory")); err != nil {
		t.Fatal(err)
	}

	got, err := findNotes(claude)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"_global/g.md", "lists/a.md", "perigee/p.md"}
	if r := refs(got); len(r) != len(want) || r[0] != want[0] || r[1] != want[1] || r[2] != want[2] {
		t.Errorf("got %v, want %v", r, want)
	}
}

func TestNotMigratedSkipsNotesInTheBase(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "facts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if _, err := s.AddWith("факт из заметки a", nil, "lists 2026-10-01 (lists/a.md)", "", store.Meta{}); err != nil {
		t.Fatal(err)
	}
	got, err := notMigrated(s, []note{{path: "/m/lists/a.md", tier: "lists"}, {path: "/m/lists/b.md", tier: "lists"}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if r := refs(got); len(r) != 1 || r[0] != "lists/b.md" {
		t.Errorf("got %v", r)
	}
}

// A note migrated with an older prompt is offered again when the prompt version moves forward.
func TestNotMigratedRerunsOlderPrompt(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "facts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	notes := []note{{path: "/m/lists/a.md", tier: "lists"}}
	if _, err := s.AddWith("факт из заметки a", nil, "lists 2026-10-01 (lists/a.md)", "",
		store.Meta{PromptVersion: 0}); err != nil {
		t.Fatal(err)
	}
	if got, err := notMigrated(s, notes, 0); err != nil || len(got) != 0 {
		t.Errorf("current prompt: %v %v", refs(got), err)
	}
	if got, err := notMigrated(s, notes, 1); err != nil || len(got) != 1 {
		t.Errorf("newer prompt: %v %v", refs(got), err)
	}
}

func TestMigrateRecordsPromptVersion(t *testing.T) {
	dir := env(t)
	notePath := filepath.Join(dir, "claude", "memory", "lists", "a.md")
	write(t, notePath, "note a\n")
	noteFn = func(context.Context, string, string) (extract.Result, error) {
		return extract.Result{Facts: []extract.Fact{{Content: "Факт из заметки a.", Keywords: []string{"a"}}}}, nil
	}
	t.Cleanup(func() { noteFn = extract.Note })
	var out strings.Builder
	if err := runMigrate([]string{notePath}, &out); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	act, err := s.Active()
	if err != nil || len(act) != 1 || act[0].PromptVersion != extract.NotePromptVersion {
		t.Errorf("migrated facts %+v %v", act, err)
	}
}

func TestDecodeProjectFindsTheEncodedPath(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(dir, "git.example", "admin-panel")
	if err := os.MkdirAll(filepath.Join(dir, "git-example"), 0o700); err != nil { // a decoy with the same encoding
		t.Fatal(err)
	}
	if err := os.MkdirAll(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	name := reNotAlnum.ReplaceAllString(repo, "-")
	if got := decodeProject(name); got != repo {
		t.Errorf("decodeProject(%q) = %q, want %q", name, got, repo)
	}
	if got := decodeProject(name + "-gone"); got != "" {
		t.Errorf("missing path decoded to %q", got)
	}
}

func TestNamedNoteIsMigratedAgainButFoundOneIsSkipped(t *testing.T) {
	dir := env(t)
	note := filepath.Join(dir, "claude", "memory", "lists", "a.md")
	write(t, note, "note a\n")
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddWith("факт из заметки a", nil, "lists 2026-10-01 (lists/a.md)", "", store.Meta{}); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	var all, named strings.Builder
	if err := runMigrate([]string{"-list", "-all"}, &all); err != nil {
		t.Fatal(err)
	}
	if err := runMigrate([]string{"-list", note}, &named); err != nil {
		t.Fatal(err)
	}
	if all.String() != "" || !strings.HasPrefix(named.String(), "lists/a.md ") {
		t.Errorf("-all listed %q, named listed %q", all.String(), named.String())
	}
}

// A note named on the command line and found by -all is sent to the model once, not twice.
func TestMigrateAllDeduplicatesNamedNote(t *testing.T) {
	dir := env(t)
	notePath := filepath.Join(dir, "claude", "memory", "lists", "a.md")
	write(t, notePath, "note a\n")
	var calls int
	noteFn = func(context.Context, string, string) (extract.Result, error) {
		calls++
		return extract.Result{Facts: []extract.Fact{{Content: "Факт из заметки a.", Keywords: []string{"a"}}}}, nil
	}
	t.Cleanup(func() { noteFn = extract.Note })
	var out strings.Builder
	if err := runMigrate([]string{"-all", notePath}, &out); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	act, err := s.Active()
	if calls != 1 || err != nil || len(act) != 1 {
		t.Errorf("%d model calls, facts %+v, %v", calls, act, err)
	}
}

// The same file under two paths is one note: a named symlink and the real note that -all finds resolve to one path.
func TestMigrateAllDeduplicatesNamedSymlink(t *testing.T) {
	dir := env(t)
	real := filepath.Join(dir, "notes", "a.md")
	write(t, real, "note a\n")
	found := filepath.Join(dir, "claude", "memory", "lists", "a.md")
	if err := os.MkdirAll(filepath.Dir(found), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, found); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(dir, "alias", "a.md")
	if err := os.MkdirAll(filepath.Dir(alias), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	var calls int
	noteFn = func(context.Context, string, string) (extract.Result, error) {
		calls++
		return extract.Result{Facts: []extract.Fact{{Content: "Факт из заметки a.", Keywords: []string{"a"}}}}, nil
	}
	t.Cleanup(func() { noteFn = extract.Note })
	var out strings.Builder
	if err := runMigrate([]string{"-all", alias}, &out); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	act, err := s.Active()
	if calls != 1 || err != nil || len(act) != 1 {
		t.Errorf("%d model calls, facts %+v, %v", calls, act, err)
	}
}

func TestNamedNoteOfBuiltInLayoutGetsItsRepository(t *testing.T) {
	dir := t.TempDir()
	claude := filepath.Join(dir, "claude")
	repo := filepath.Join(dir, "src", "admin-panel")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	proj := filepath.Join(claude, "projects", "-x-admin-panel")
	write(t, filepath.Join(proj, "s.jsonl"), `{"type":"user","cwd":"`+repo+`","message":{"content":"hi"}}`+"\n")
	if got := noteTier(filepath.Join(proj, "memory", "a.md"), claude); got != "admin-panel" {
		t.Errorf("built-in layout: %q", got)
	}
	if got := noteTier(filepath.Join(claude, "memory", "lists", "b.md"), claude); got != "lists" {
		t.Errorf("shared layout: %q", got)
	}
}

func TestMigrateDryRunSaysFactsDoNotGoThroughReview(t *testing.T) {
	dir := env(t)
	notePath := filepath.Join(dir, "claude", "memory", "lists", "a.md")
	write(t, notePath, "note a\n")
	noteFn = func(context.Context, string, string) (extract.Result, error) {
		return extract.Result{Facts: []extract.Fact{{Content: "Факт из заметки a.", Keywords: []string{"a"}}}}, nil
	}
	t.Cleanup(func() { noteFn = extract.Note })
	var out strings.Builder
	if err := runMigrate([]string{"-dry-run", notePath}, &out); err != nil {
		t.Fatal(err)
	}
	var rec struct {
		Review bool `json:"review"`
		Prompt int  `json:"prompt_version"`
	}
	if err := json.Unmarshal([]byte(out.String()), &rec); err != nil {
		t.Fatalf("%q: %v", out.String(), err)
	}
	if rec.Review || rec.Prompt != extract.NotePromptVersion {
		t.Errorf("record %+v", rec)
	}
}
