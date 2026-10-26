package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/juev/memcue/internal/store"
)

// notesDir writes the note files into a new directory and returns it.
func notesDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// addFacts stores the facts in the base of env and returns their ids in order.
func addFacts(t *testing.T, facts ...store.FactInput) []string {
	t.Helper()
	s, err := store.Open(os.Getenv("MEMCUE_FACTS"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	ids := make([]string, len(facts))
	for i, in := range facts {
		f, err := s.AddWith(in.Content, in.Keywords, in.Origin, in.Expires, in.Meta)
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = f.ID
	}
	return ids
}

// Two verbatim copies and a paraphrase are reported as the facts that hold them, the unrelated entry as new, and the
// summary counts the entries and their characters; the base is left as it was (#489).
func TestNotesReportsEntriesTheBaseHolds(t *testing.T) {
	env(t)
	postgres := "The demo database is PostgreSQL 18 on port 55432 and serves the staging cluster for tests."
	gateway := "Hermes gateway restarts nightly at three in the morning through the systemd timer unit."
	signing := "Release builds are signed with the hardware token kept in the office safe drawer."
	ids := addFacts(t,
		store.FactInput{Content: postgres, Meta: store.Meta{Kind: "environment", Scope: "host:builder"}},
		store.FactInput{Content: gateway, Meta: store.Meta{Kind: "rule", Scope: "global"}},
		store.FactInput{Content: signing},
	)
	paraphrase := "Release builds always get signed with the hardware token from the office safe."
	unrelated := "The cat likes sleeping on the windowsill during sunny afternoons."
	dir := notesDir(t, map[string]string{
		"MEMORY.md": postgres + "\n\n" + gateway + "\n\n" + paraphrase + "\n\n" + unrelated + "\n",
	})

	base := os.Getenv("MEMCUE_FACTS")
	before, err := os.Stat(base)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runNotes([]string{"-dir", dir}, &out); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(base)
	if err != nil {
		t.Fatal(err)
	}
	if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		t.Errorf("notes changed the base: size %d -> %d, mtime %v -> %v", before.Size(), after.Size(),
			before.ModTime(), after.ModTime())
	}

	got := out.String()
	for _, id := range ids {
		if n := strings.Count(got, " "+id+" "); n != 1 {
			t.Errorf("fact %s appears %d times, want once:\n%s", id, n, got)
		}
	}
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) != 6 {
		t.Fatalf("got %d lines, want 4 entries, a summary and a hint:\n%s", len(lines), got)
	}
	wantPrefix := []string{
		"ok " + ids[0] + " environment/host:builder The demo database",
		"ok " + ids[1] + " rule/global Hermes gateway",
		"ok " + ids[2] + " -/- Release builds always",
		"new " + strconv.Itoa(len([]rune(unrelated))) + "ch The cat likes",
	}
	for i, p := range wantPrefix {
		if !strings.HasPrefix(lines[i], p) {
			t.Errorf("line %d = %q, want prefix %q", i+1, lines[i], p)
		}
	}
	if !strings.HasSuffix(lines[0], postgres[:60]) {
		t.Errorf("line 1 = %q, want the first 60 characters of the entry", lines[0])
	}
	known := len([]rune(postgres)) + len([]rune(gateway)) + len([]rune(paraphrase))
	total := known + len([]rune(unrelated))
	wantSummary := "4 entries: 3 already facts (" + strconv.Itoa(known) + " of " + strconv.Itoa(total) + " characters), 1 not in the base"
	if lines[4] != wantSummary {
		t.Errorf("summary = %q, want %q", lines[4], wantSummary)
	}
	if !strings.HasPrefix(lines[5], "move them with: memcue add") {
		t.Errorf("hint = %q", lines[5])
	}
}

// Fewer than five shared words never make a copy, however close the two texts are: the conservative side of the rule.
func TestNotesShortEntryStaysNew(t *testing.T) {
	env(t)
	addFacts(t, store.FactInput{Content: "Deploy keys rotate monthly on the build server."})
	dir := notesDir(t, map[string]string{"MEMORY.md": "Deploy keys rotate monthly."})
	var out bytes.Buffer
	if err := runNotes([]string{"-dir", dir}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "new 27ch Deploy keys rotate monthly.\n") {
		t.Errorf("a four-word overlap was taken for a copy:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "1 entries: 0 already facts (0 of 27 characters), 1 not in the base") {
		t.Errorf("summary:\n%s", out.String())
	}
}

// A § separates entries as a blank line does, in every *.md of the directory, MEMORY.md included; a directory whose
// files hold no entry, and a missing directory, are errors.
func TestNotesSplitsOnSectionSign(t *testing.T) {
	env(t)
	dir := notesDir(t, map[string]string{
		"MEMORY.md": "first entry about alpha\n§\nsecond entry about beta",
		"USER.md":   "third entry about gamma",
		"notes.txt": "not a note file",
	})
	var out bytes.Buffer
	if err := runNotes([]string{"-dir", dir, "-json"}, &out); err != nil {
		t.Fatal(err)
	}
	var r notesReport
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	var got []string
	for _, e := range r.Entries {
		got = append(got, e.File+"#"+strconv.Itoa(e.Index)+" "+e.Text)
	}
	want := []string{"MEMORY.md#1 first entry about alpha", "MEMORY.md#2 second entry about beta",
		"USER.md#1 third entry about gamma"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("entries = %q, want %q", got, want)
	}

	empty := notesDir(t, map[string]string{"MEMORY.md": "\n§\n  \n\n§"})
	if err := runNotes([]string{"-dir", empty}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "no note entries") {
		t.Errorf("a file without entries: err = %v, want no note entries", err)
	}
	if err := runNotes([]string{"-dir", filepath.Join(empty, "missing")}, &bytes.Buffer{}); err == nil {
		t.Error("a missing directory is not an error")
	}
}

// -json prints one object; without a base every entry is new, and the base is not created.
func TestNotesJSONWithoutBase(t *testing.T) {
	env(t)
	dir := notesDir(t, map[string]string{"MEMORY.md": "Ünïcode entry\n\nsecond one"})
	t.Setenv("MEMCUE_MEMORY_DIR", dir)
	var out bytes.Buffer
	if err := runNotes([]string{"-json"}, &out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(os.Getenv("MEMCUE_FACTS")); !os.IsNotExist(err) {
		t.Errorf("notes created the base: %v", err)
	}
	var r map[string]any
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	if r["already"] != 0.0 || r["total"] != 2.0 || r["chars_already"] != 0.0 || r["chars_total"] != 23.0 {
		t.Errorf("counts = %v", r)
	}
	entries, _ := r["entries"].([]any)
	if len(entries) != 2 {
		t.Fatalf("entries = %v", r["entries"])
	}
	first, _ := entries[0].(map[string]any)
	want := map[string]any{"file": "MEMORY.md", "index": 1.0, "chars": 13.0, "fact": "", "kind": "", "scope": "",
		"text": "Ünïcode entry"}
	for k, v := range want {
		if first[k] != v {
			t.Errorf("entries[0].%s = %v, want %v", k, first[k], v)
		}
	}
	if len(first) != len(want) {
		t.Errorf("entries[0] = %v, want exactly the keys of %v", first, want)
	}
}

// -min-share takes the place of both shares: a paraphrase the default rule takes is new under a strict share.
func TestNotesMinShare(t *testing.T) {
	env(t)
	addFacts(t, store.FactInput{Content: "Release builds are signed with the hardware token kept in the office safe drawer."})
	dir := notesDir(t, map[string]string{
		"MEMORY.md": "Release builds always get signed with the hardware token from the office safe.",
	})
	for _, c := range []struct {
		share string
		want  float64
	}{{"0", 1}, {"0.95", 0}} {
		var out bytes.Buffer
		if err := runNotes([]string{"-dir", dir, "-json", "-min-share", c.share}, &out); err != nil {
			t.Fatal(err)
		}
		var r map[string]any
		if err := json.Unmarshal(out.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		if r["already"] != c.want {
			t.Errorf("-min-share %s: already = %v, want %v", c.share, r["already"], c.want)
		}
	}
}