package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The consistency contract of the text history (#642): memcue and git change it. Git writes a file anew when it
// checks one out or merges it, which moves the time of its directory, and the next open takes the file in. A file
// edited in place, as an editor or rsync --inplace may do, keeps its directory's time: the open does not read it,
// and the cache keeps the history it had, without writing it back over the edit. A fact is changed with memcue correct.
func TestAnOpenSeesAReplacedFileAndNotAnInPlaceEdit(t *testing.T) {
	dir, cache := t.TempDir(), filepath.Join(t.TempDir(), "facts.db")
	s := openTextAt(t, dir, cache, "mac", t0)
	f, err := s.AddWith("Порт db1 — 5432", []string{"db1"}, "x 2026-10-19", "", Meta{})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	// the new file moved its directory: the next open reads that directory once and finds nothing new
	s = openTextAt(t, dir, cache, "mac", t0)
	_ = s.Close()
	path := FactPath(dir, f.ID)
	edited := f
	edited.Content = "Порт db1 — 5433"

	// in place: the content changes, the directory keeps its time
	info, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	data, err := EncodeFacts([]Fact{edited})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Dir(path), info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	s = openTextAt(t, dir, cache, "mac", t0)
	if got, err := s.Get(f.ID); err != nil || got.Content != f.Content {
		t.Errorf("an in-place edit reached the cache: %+v %v", got, err)
	}
	_ = s.Close()
	if file, err := ReadFactFile(path); err != nil || len(file) != 1 || file[0].Content != edited.Content {
		t.Errorf("the open wrote over the edit: %+v %v", file, err)
	}

	// as git does it: a new file renamed into place, which moves the directory's time
	time.Sleep(10 * time.Millisecond)
	if err := WriteFactFile(path, []Fact{edited}); err != nil {
		t.Fatal(err)
	}
	s = openTextAt(t, dir, cache, "mac", t0)
	defer func() { _ = s.Close() }()
	// the edited version and the cached one share a number, so the merge keeps both, the edit as the latest
	if got, err := s.Get(f.ID); err != nil || got.Content != edited.Content {
		t.Errorf("a replaced file did not reach the cache: %+v %v", got, err)
	}
	textMatches(t, s)
}
