package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The extraction reads the notes of MEMCUE_MEMORY_DIR with their lines kept, cut to maxNotes; without the directory
// it reads none (#456).
func TestExtractionNotes(t *testing.T) {
	dir := env(t)
	t.Setenv("MEMCUE_MEMORY_DIR", "")
	if got := extractionNotes(); got != "" {
		t.Errorf("no directory: %q", got)
	}
	notes := filepath.Join(dir, "memories")
	if err := os.MkdirAll(notes, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(notes, "MEMORY.md"), []byte("Первая заметка.\n§\nВторая заметка.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MEMCUE_MEMORY_DIR", notes)
	if got := extractionNotes(); got != "Первая заметка.\n§\nВторая заметка." {
		t.Errorf("notes %q", got)
	}
	if err := os.WriteFile(filepath.Join(notes, "USER.md"), []byte(strings.Repeat("я", 2*maxNotes)), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := []rune(extractionNotes()); len(got) != maxNotes {
		t.Errorf("notes of %d characters, want the first %d", len(got), maxNotes)
	}
}
