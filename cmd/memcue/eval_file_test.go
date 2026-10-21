package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// The unlabelled eval file holds prompts and private fact text: it must not be readable by other users.
func TestWriteEvalPairsIsPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	path := filepath.Join(t.TempDir(), "unlabeled.json")
	if err := writeEvalPairs(path, []evalPair{{Prompt: "p", Fact: "f", Content: "secret text", Origin: "o"}}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("mode %o, want 600", got)
	}
}
