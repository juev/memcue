package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/juev/memcue/internal/store"
)

// dumpBase is memcue dump of the base this process opens.
func dumpBase(t *testing.T) string {
	t.Helper()
	s, err := openBase()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	var b bytes.Buffer
	if err := s.Dump(&b); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// seedHistory writes facts with several versions and both statuses into the old format.
func seedHistory(t *testing.T) {
	t.Helper()
	seed(t)
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	f, err := s.AddWith("Hermes держит базу в /opt/data/memcue.", []string{"hermes"}, "memcue 2026-10-28", "",
		store.Meta{Kind: "environment", Scope: "host:hermes"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Correct(f.ID, "Hermes держит базу в /opt/data/memcue/facts.", []string{"hermes", "facts"}); err != nil {
		t.Fatal(err)
	}
	g, err := s.AddWith("Старое правило.", nil, "", "", store.Meta{Kind: "rule"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Retire(g.ID); err != nil {
		t.Fatal(err)
	}
}

// A base the path names is read in the format the file system shows (#530).
func TestFactsBaseByPath(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file.db")
	write(t, file, "")
	moved := filepath.Join(dir, "moved.db")
	if err := os.Mkdir(filepath.Join(dir, "moved"), 0o700); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(dir, "base.sqlite")
	write(t, other, "")
	for _, tc := range []struct {
		facts string
		want  factBase
	}{
		{file, factBase{path: file}},
		{moved, factBase{path: filepath.Join(dir, "moved"), text: true}},
		{filepath.Join(dir, "moved"), factBase{path: filepath.Join(dir, "moved"), text: true}},
		{filepath.Join(dir, "new.db"), factBase{path: filepath.Join(dir, "new.db")}},
		{filepath.Join(dir, "new"), factBase{path: filepath.Join(dir, "new"), text: true}},
		{other, factBase{path: other}},
	} {
		t.Setenv("MEMCUE_FACTS", tc.facts)
		if got := factsBase(); got != tc.want {
			t.Errorf("MEMCUE_FACTS=%s: %+v, want %+v", tc.facts, got, tc.want)
		}
	}
}

// migrate -text keeps every version, removes the file and leaves a base that searches as before (#530, R1, R7).
func TestMigrateTextKeepsTheBase(t *testing.T) {
	dir := env(t)
	seedHistory(t)
	before := dumpBase(t)
	var out bytes.Buffer
	if err := runMigrateText(&out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "3 facts (5 versions)") {
		t.Errorf("report: %s", out.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "facts.db")); !os.IsNotExist(err) {
		t.Errorf("the old file stays: %v", err)
	}
	if b := factsBase(); !b.text || b.path != filepath.Join(dir, "facts") {
		t.Fatalf("base after the move: %+v", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "state", "memcue", "facts.db")); err != nil {
		t.Errorf("no cache in the state directory: %v", err)
	}
	if after := dumpBase(t); after != before {
		t.Errorf("dump changed:\n%s\nwant\n%s", after, before)
	}
	out.Reset()
	if err := runSearch([]string{"gradle jdk"}, &out); err != nil || !strings.Contains(out.String(), "JAVA_HOME") {
		t.Errorf("search after the move: %v %s", err, out.String())
	}

	// Another machine: git brought the text and removed the file, there is no cache yet.
	t.Setenv("MEMCUE_STATE_DIR", filepath.Join(dir, "other-state"))
	if after := dumpBase(t); after != before {
		t.Errorf("cache built from the text differs:\n%s\nwant\n%s", after, before)
	}
}

// A second migrate -text changes nothing; one that finds a file of the old format beside the text takes it in.
func TestMigrateTextAgain(t *testing.T) {
	dir := env(t)
	seed(t)
	if err := runMigrateText(&bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runMigrateText(&out); err != nil || !strings.Contains(out.String(), "already a text history") {
		t.Fatalf("second run: %v %s", err, out.String())
	}
	left, err := store.Open(filepath.Join(dir, "facts.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := left.AddWith("Факт из забытой копии.", []string{"копия"}, "", "", store.Meta{}); err != nil {
		t.Fatal(err)
	}
	_ = left.Close()
	if b := factsBase(); !b.text {
		t.Fatalf("a file beside the text turned the base back: %+v", b)
	}
	out.Reset()
	if err := runMigrateText(&out); err != nil || !strings.Contains(out.String(), "took 1 versions") {
		t.Fatalf("leftover: %v %s", err, out.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "facts.db")); !os.IsNotExist(err) {
		t.Errorf("the leftover stays: %v", err)
	}
	if d := dumpBase(t); !strings.Contains(d, "Факт из забытой копии.") || !strings.Contains(d, "JAVA_HOME") {
		t.Errorf("dump after the leftover:\n%s", d)
	}
}

// A base outside the default keeps its state directory across the move: the key follows the path, and the state of
// the file goes to the text (#530).
func TestStateFollowsTheBaseToText(t *testing.T) {
	dir := env(t)
	t.Setenv("MEMCUE_STATE_DIR", "")
	seed(t)
	old, err := stateDir()
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(old, "dream.at"), "2026-10-28T00:00:00Z\n")
	if err := runMigrateText(&bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	now, err := stateDir()
	if err != nil {
		t.Fatal(err)
	}
	if now == old || now != filepath.Join(dir, "state", "memcue", baseKey(filepath.Join(dir, "facts"))) {
		t.Fatalf("state dir %s (was %s)", now, old)
	}
	if _, err := os.Stat(filepath.Join(now, "dream.at")); err != nil {
		t.Errorf("the dream schedule stayed behind: %v", err)
	}
	if _, err := os.Stat(filepath.Join(now, "facts.db")); err != nil {
		t.Errorf("no cache in the state directory: %v", err)
	}
}

// The default text history keeps the historical state directory, as the default file did.
func TestStateDirKeepsTheDefaultText(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(dir, "claude"))
	t.Setenv("MEMCUE_STATE_DIR", "")
	t.Setenv("MEMCUE_FACTS", "")
	if err := os.MkdirAll(filepath.Join(dir, "claude", "memory", "facts"), 0o700); err != nil {
		t.Fatal(err)
	}
	if b := factsBase(); !b.text || b.path != filepath.Join(dir, "claude", "memory", "facts") {
		t.Fatalf("default base: %+v", b)
	}
	got, err := stateDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "state", "memcue"); got != want {
		t.Errorf("state dir = %s, want %s", got, want)
	}
}
