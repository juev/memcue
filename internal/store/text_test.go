package store

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Every version of every kind of fact reads back from its line as it reads from the base (#530): the text history is
// the source the base is rebuilt from, so a field it drops is lost.
func TestTextRoundTripKeepsEveryField(t *testing.T) {
	s := openAt(t, filepath.Join(t.TempDir(), "facts.db"), "mac", t0)
	defer func() { _ = s.Close() }()
	plain, err := s.AddWith("Порт <db1> & \"кавычки\",\nвторая строка\tи таб", []string{"db1", "порт, postgres"},
		"github.com/me/app 2026-10-19", "2026-12-01", Meta{Kind: "environment", Scope: "host:mac", Source: "tool",
			Evidence: "`psql -p 5433` → ok", PromptVersion: 3})
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return t0.Add(1234567 * time.Nanosecond) }
	if _, err := s.Correct(plain.ID, "Порт db1 — 5433 🚀", []string{"db1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetQueries(plain.ID, 2, []string{"какой порт у db1", "db1 port"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Relabel(plain.ID, 3, "pitfall", "global", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Retire(plain.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Restore(plain.ID); err != nil {
		t.Fatal(err)
	}
	bare, err := s.AddWith("x", nil, "", "", Meta{})
	if err != nil {
		t.Fatal(err)
	}
	legacy := Fact{ID: "01JLEGACYCANDIDATE00000099", Version: 1, Content: "Кандидат.", Origin: "x", Author: "old",
		Created: t0, Status: Active, Meta: Meta{Kind: "environment", Review: Candidate}}
	legacyRows(t, s, legacy)
	for _, id := range []string{plain.ID, bare.ID, legacy.ID} {
		want, err := s.History(id)
		if err != nil || len(want) == 0 {
			t.Fatalf("history of %s: %v %v", id, want, err)
		}
		data, err := EncodeFacts(want)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Count(data, []byte("\n")) != len(want) {
			t.Fatalf("%s: %d lines for %d versions:\n%s", id, bytes.Count(data, []byte("\n")), len(want), data)
		}
		got, err := ParseFacts(data)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(want) {
			t.Fatalf("%s: %d versions back, want %d", id, len(got), len(want))
		}
		for i := range want {
			if !same(got[i], want[i]) || got[i].ID != want[i].ID || got[i].Version != want[i].Version {
				t.Errorf("%s v%d:\n got %+v\nwant %+v", id, want[i].Version, got[i], want[i])
			}
		}
		again, _ := EncodeFacts(got)
		if !bytes.Equal(again, data) {
			t.Errorf("%s: a second encoding differs:\n%s\nvs\n%s", id, again, data)
		}
	}
	data, _ := EncodeFacts([]Fact{plain})
	if !bytes.Contains(data, []byte("<db1> & ")) {
		t.Errorf("HTML characters escaped: %s", data)
	}
}

func TestParseFactsOrdersAndRejects(t *testing.T) {
	f := Fact{ID: "01JTEXT000000000000000000A", Version: 1, Content: "a", Created: t0, Status: Active, Author: "mac"}
	g := f
	g.Version, g.Content, g.Created = 2, "b", t0.Add(time.Minute)
	l1, _ := encodeLine(f)
	l2, _ := encodeLine(g)

	got, err := ParseFacts(bytes.Join([][]byte{l2, l1, l2, []byte("\n")}, nil))
	if err != nil || len(got) != 2 || got[0].Content != "a" || got[1].Content != "b" {
		t.Fatalf("got %+v %v, want a then b once each", got, err)
	}
	other := g
	other.Content = "c"
	l3, _ := encodeLine(other)
	if _, err := ParseFacts(append(append([]byte{}, l2...), l3...)); err == nil ||
		!strings.Contains(err.Error(), "two different versions 2") {
		t.Errorf("two lines of v2: %v", err)
	}
	stranger := f
	stranger.ID = "01JTEXT000000000000000000B"
	l4, _ := encodeLine(stranger)
	if _, err := ParseFacts(append(append([]byte{}, l1...), l4...)); err == nil {
		t.Error("a line of another fact was accepted")
	}
	for _, bad := range []string{`{"id":"x"}`, `{"id":"x","version":1,"status":"gone","created":"2026-10-19T12:00:00Z"}`,
		`{"id":"x","version":1,"status":"active","created":"yesterday"}`, `not json`} {
		if _, err := ParseFacts([]byte(bad + "\n")); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

func TestFactFileAppendAndReplace(t *testing.T) {
	dir := t.TempDir()
	f := Fact{ID: "01JTEXT000000000000000000Z", Version: 1, Content: "a", Created: t0, Status: Active, Author: "mac"}
	path := FactPath(dir, f.ID)
	if want := filepath.Join(dir, "Z", f.ID+".jsonl"); path != want {
		t.Fatalf("path %s, want %s", path, want)
	}
	if got, err := ReadFactFile(path); err != nil || got != nil {
		t.Fatalf("missing file: %v %v", got, err)
	}
	if err := appendFacts(path, []Fact{f}); err != nil {
		t.Fatal(err)
	}
	g := f
	g.Version, g.Status, g.Created = 2, Retired, t0.Add(time.Hour)
	if err := appendFacts(path, []Fact{g}); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFactFile(path)
	if err != nil || len(got) != 2 || got[1].Status != Retired {
		t.Fatalf("after two appends %+v %v", got, err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", info.Mode().Perm())
	}
	h := f
	h.Content = "replaced"
	if err := WriteFactFile(path, []Fact{h}); err != nil {
		t.Fatal(err)
	}
	if got, _ := ReadFactFile(path); len(got) != 1 || got[0].Content != "replaced" {
		t.Errorf("after replace %+v", got)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "Z", ".tmp-*")); len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}
}
