package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/juev/memcue/internal/extract"
)

func TestStripFrontmatter(t *testing.T) {
	body := "The rule.\n\n**Why:** because.\n"
	with := "---\nname: x\ndescription: y\ntype: feedback\n---\n" + body
	if got := string(stripFrontmatter([]byte(with))); got != body {
		t.Errorf("stripped %q, want %q", got, body)
	}
	if got := string(stripFrontmatter([]byte(body))); got != body {
		t.Errorf("plain text changed: %q", got)
	}
	unclosed := "---\nname: x\nno closing line\n"
	if got := string(stripFrontmatter([]byte(unclosed))); got != unclosed {
		t.Errorf("unclosed frontmatter changed: %q", got)
	}
}

// A Qwen memory file carries its metadata in the frontmatter and holds one note in the body, blank lines and all;
// a file without frontmatter keeps the entry-per-blank-line reading.
func TestReadNoteEntriesQwenFileIsOneEntry(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "feedback.md"), "---\nname: rule\ntype: feedback\n---\nThe rule.\n\n**Why:** because.\n\n**How to apply:** always.\n")
	write(t, filepath.Join(dir, "plain.md"), "first entry\n\nsecond entry\n")
	entries, err := readNoteEntries(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("entries %+v", entries)
	}
	if entries[0].File != "feedback.md" || entries[0].Index != 1 ||
		entries[0].Text != "The rule.\n\n**Why:** because.\n\n**How to apply:** always." {
		t.Errorf("qwen entry %+v", entries[0])
	}
	if strings.Contains(entries[0].Text, "name:") {
		t.Errorf("frontmatter kept: %q", entries[0].Text)
	}
}

// The Qwen memory layout: user-level notes in memories/*.md, project notes in projects/<key>/memory/*.md whose
// transcript lies in chats/; MEMORY.md is an index, not a note.
func TestFindQwenNotes(t *testing.T) {
	dir := t.TempDir()
	qwen := filepath.Join(dir, "qwen")
	repo := filepath.Join(dir, "src", "lists")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(qwen, "memories", "MEMORY.md"), "- [u](u.md)\n")
	write(t, filepath.Join(qwen, "memories", "u.md"), "---\ntype: user\n---\nuser note\n")
	proj := filepath.Join(qwen, "projects", "-x-src-lists")
	write(t, filepath.Join(proj, "chats", "s.jsonl"), `{"type":"user","cwd":"`+repo+`","message":{"parts":[{"text":"hi"}]}}`+"\n")
	write(t, filepath.Join(proj, "memory", "MEMORY.md"), "- [p](p.md)\n")
	write(t, filepath.Join(proj, "memory", "p.md"), "---\ntype: project\n---\nproject note\n")
	got, err := findQwenNotes(qwen)
	if err != nil {
		t.Fatal(err)
	}
	if r := refs(got); !slices.Equal(r, []string{"lists/p.md", "user/u.md"}) {
		t.Errorf("got %v", r)
	}
}

func TestNoteTierQwenProject(t *testing.T) {
	dir := env(t)
	repo := filepath.Join(dir, "src", "lists")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	proj := filepath.Join(qwenDir(), "projects", "-x-src-lists")
	write(t, filepath.Join(proj, "chats", "s.jsonl"), `{"cwd":"`+repo+`"}`+"\n")
	notePath := filepath.Join(proj, "memory", "a.md")
	write(t, notePath, "note\n")
	if got := noteTier(notePath, claudeDir()); got != "lists" {
		t.Errorf("tier %q, want lists", got)
	}
}

// migrate -all lists the notes of both hosts, and the model receives a Qwen note without its frontmatter.
func TestMigrateAllListsClaudeAndQwenNotes(t *testing.T) {
	env(t)
	write(t, filepath.Join(claudeDir(), "memory", "_global", "g.md"), "claude note\n")
	qwenNote := filepath.Join(qwenDir(), "memories", "u.md")
	write(t, qwenNote, "---\ntype: user\n---\nqwen note\n")
	var out bytes.Buffer
	if err := runMigrate([]string{"-list", "-all"}, &out); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "_global/g.md") || !strings.Contains(got, "user/u.md") {
		t.Errorf("list:\n%s", got)
	}

	old := noteFn
	var seen string
	noteFn = func(_ context.Context, _, text string) (extract.Result, error) {
		seen = text
		return extract.Result{}, nil
	}
	t.Cleanup(func() { noteFn = old })
	out.Reset()
	if err := runMigrate([]string{"-dry-run", qwenNote}, &out); err != nil {
		t.Fatal(err)
	}
	if seen != "qwen note\n" {
		t.Errorf("the model received %q, want the body without frontmatter", seen)
	}
}
