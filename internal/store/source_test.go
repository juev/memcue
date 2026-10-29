package store

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// unlabelled writes, as git would bring them, the histories of facts written before sources were labelled, next to
// facts with a source: the relabelling must reach the first and leave the rest as they are (#582).
func unlabelled(t *testing.T, dir string) (active, provisional, retired, user, tool Fact) {
	t.Helper()
	at := func(m int) time.Time { return t0.Add(time.Duration(m) * time.Minute) }
	active = Fact{ID: "01JSOURCE00000000000000001", Version: 1, Content: "Порт db1 — 5432", Keywords: []string{"db1"},
		Origin: "x 2026-10-19", Created: at(0), Status: Active, Author: "mac", Meta: Meta{Kind: "environment"}}
	corrected := active
	corrected.Version, corrected.Content, corrected.Author, corrected.Created = 2, "Порт db1 — 5433", "server", at(1)
	provisional = Fact{ID: "01JSOURCE00000000000000002", Version: 1, Content: "Caddy перечитывает конфиг по SIGUSR1",
		Created: at(2), Status: Active, Author: "mac", Expires: "2099-01-01", Meta: Meta{Review: Provisional}}
	retired = Fact{ID: "01JSOURCE00000000000000003", Version: 1, Content: "Старый факт про db2", Created: at(3),
		Status: Active, Author: "mac"}
	gone := retired
	gone.Version, gone.Status, gone.Created = 2, Retired, at(4)
	user = Fact{ID: "01JSOURCE00000000000000004", Version: 1, Content: "Коммиты всегда на английском",
		Created: at(5), Status: Active, Author: "mac", Meta: Meta{Kind: "rule", Scope: "global", Source: "user"}}
	tool = Fact{ID: "01JSOURCE00000000000000005", Version: 1, Content: "make test гоняет node-тесты плагинов",
		Created: at(6), Status: Active, Author: "mac", Meta: Meta{Kind: "pitfall", Source: "tool"}}
	for _, h := range [][]Fact{{active, corrected}, {provisional}, {retired, gone}, {user}, {tool}} {
		if err := WriteFactFile(FactPath(dir, h[0].ID), h); err != nil {
			t.Fatal(err)
		}
	}
	return corrected, provisional, gone, user, tool
}

// An open gives every active fact without a source one version with source agent, in the cache and in its file; the
// version is the one before it with that one label changed (#582).
func TestOpenRelabelsFactsWithoutASource(t *testing.T) {
	dir, cache := t.TempDir(), filepath.Join(t.TempDir(), "facts.db")
	active, provisional, retired, user, tool := unlabelled(t, dir)
	s := openTextAt(t, dir, cache, "laptop", t0.Add(time.Hour))
	defer func() { _ = s.Close() }()

	for _, before := range []Fact{active, provisional} {
		got, err := s.Get(before.ID)
		if err != nil {
			t.Fatal(err)
		}
		want := before
		want.Version, want.Source = before.Version+1, "agent"
		if !same(got, want) || got.Version != want.Version {
			t.Errorf("relabelled %+v\nwant %+v", got, want)
		}
	}
	for _, kept := range []Fact{retired, user, tool} {
		if h, _ := s.History(kept.ID); len(h) != kept.Version || !same(h[len(h)-1], kept) {
			t.Errorf("%s changed: %+v", kept.ID, h)
		}
	}
	textMatches(t, s)
	if hs := ids(t, s, "db1 порт"); len(hs) != 1 || hs[active.ID].Version != 3 {
		t.Errorf("search after the relabelling: %+v", hs)
	}
	if hs := ids(t, s, "caddy sigusr1"); hs[provisional.ID].Review != Provisional {
		t.Errorf("a provisional fact lost its state: %+v", hs[provisional.ID])
	}
}

// Once relabelled, a base opens without writing: an open that finds nothing new must not pay a commit (#557).
func TestOpenAfterTheRelabellingWritesNothing(t *testing.T) {
	dir, cache := t.TempDir(), filepath.Join(t.TempDir(), "facts.db")
	unlabelled(t, dir)
	_ = openTextAt(t, dir, cache, "laptop", t0).Close()
	_ = openTextAt(t, dir, cache, "laptop", t0).Close() // reads the directories the appends moved

	before := changeCounter(t, cache)
	for range 3 {
		_ = openTextAt(t, dir, cache, "laptop", t0).Close()
	}
	if after := changeCounter(t, cache); after != before {
		t.Errorf("opens after the relabelling wrote the cache: change counter %d → %d", before, after)
	}
}

// A version without a source that git brings after the relabelling, a correction made by an older memcue, is
// relabelled at the next open.
func TestOpenRelabelsAVersionGitBroughtLater(t *testing.T) {
	dir, cache := t.TempDir(), filepath.Join(t.TempDir(), "facts.db")
	active, _, _, _, _ := unlabelled(t, dir)
	_ = openTextAt(t, dir, cache, "laptop", t0).Close()

	h, err := ReadFactFile(FactPath(dir, active.ID))
	if err != nil {
		t.Fatal(err)
	}
	older := h[len(h)-1]
	older.Version, older.Content, older.Source, older.Author = older.Version+1, "Порт db1 — 6432", "", "server"
	older.Created = t0.Add(3 * time.Hour)
	if err := WriteFactFile(FactPath(dir, active.ID), append(h, older)); err != nil {
		t.Fatal(err)
	}
	s := openTextAt(t, dir, cache, "laptop", t0)
	defer func() { _ = s.Close() }()
	if got, err := s.Get(active.ID); err != nil || got.Content != older.Content || got.Source != "agent" ||
		got.Version != older.Version+1 {
		t.Errorf("after git brought an unlabelled version: %+v %v", got, err)
	}
	textMatches(t, s)
}

// Two machines that relabel the same facts apart write the same versions, so their histories merge without a doubled
// version (#540).
func TestFactsRelabelledApartMergeAsOne(t *testing.T) {
	mac, server := t.TempDir(), t.TempDir()
	active, _, _, _, _ := unlabelled(t, mac)
	unlabelled(t, server)
	_ = openTextAt(t, mac, filepath.Join(t.TempDir(), "facts.db"), "mac", t0.Add(time.Hour)).Close()
	_ = openTextAt(t, server, filepath.Join(t.TempDir(), "facts.db"), "server", t0.Add(2*time.Hour)).Close()

	ours, err := ReadFactFile(FactPath(mac, active.ID))
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := ReadFactFile(FactPath(server, active.ID))
	if err != nil {
		t.Fatal(err)
	}
	if h := MergeHistories(ours, theirs); h.Added != 0 || len(h.Versions) != 3 {
		t.Errorf("merged %d versions, %d added: %+v", len(h.Versions), h.Added, h.Versions)
	}
}

// The parts of the Claude Code session start open the base at once: each fact is relabelled once.
func TestParallelOpensRelabelOnce(t *testing.T) {
	dir, cache := t.TempDir(), filepath.Join(t.TempDir(), "facts.db")
	// The cache exists, as on a machine that upgrades; the facts come with the next pull.
	_ = openTextAt(t, dir, cache, "laptop", t0).Close()
	active, provisional, _, _, _ := unlabelled(t, dir)
	var wg sync.WaitGroup
	errs := make(chan error, 3)
	for range 3 {
		wg.Go(func() {
			s, err := OpenText(dir, cache)
			if err != nil {
				errs <- err
				return
			}
			_ = s.Close()
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	s := openTextAt(t, dir, cache, "laptop", t0)
	defer func() { _ = s.Close() }()
	for _, f := range []Fact{active, provisional} {
		if h, _ := s.History(f.ID); len(h) != f.Version+1 {
			t.Errorf("%s has %d versions, want %d", f.ID, len(h), f.Version+1)
		}
	}
	textMatches(t, s)
}

// The first opens of a new cache, in parallel, all succeed: each read-then-write transaction waits for the others
// instead of being refused with SQLITE_BUSY.
func TestParallelFirstOpensOfACache(t *testing.T) {
	dir, cache := t.TempDir(), filepath.Join(t.TempDir(), "facts.db")
	unlabelled(t, dir)
	var wg sync.WaitGroup
	errs := make(chan error, 3)
	for range 3 {
		wg.Go(func() {
			s, err := OpenText(dir, cache)
			if err != nil {
				errs <- err
				return
			}
			_ = s.Close()
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	s := openTextAt(t, dir, cache, "laptop", t0)
	defer func() { _ = s.Close() }()
	textMatches(t, s)
}

// A base of the old format, one SQLite file, is relabelled at its open too.
func TestOpenOfASQLiteBaseRelabels(t *testing.T) {
	path := filepath.Join(t.TempDir(), "facts.db")
	s := openAt(t, path, "mac", t0)
	legacyRows(t, s, Fact{ID: "01JSOURCE00000000000000006", Version: 1, Content: "Порт db3 — 5432"})
	_ = s.Close()
	s = openAt(t, path, "mac", t0)
	defer func() { _ = s.Close() }()
	if got, err := s.Get("01JSOURCE00000000000000006"); err != nil || got.Version != 2 || got.Source != "agent" {
		t.Errorf("relabelled %+v %v", got, err)
	}
}

// A fact written now without a source is the agent's, and so is a new version of a fact that had none.
func TestAWriteWithoutASourceIsTheAgents(t *testing.T) {
	s, _ := open(t)
	f, err := s.AddWith("Порт db4 — 5432", nil, "x", "", Meta{Kind: "environment"})
	if err != nil || f.Source != "agent" {
		t.Fatalf("added %+v %v", f, err)
	}
	if got, _ := s.Get(f.ID); got.Source != "agent" {
		t.Errorf("stored %+v", got)
	}
	u, err := s.AddWith("Коммиты на английском", nil, "x", "", Meta{Kind: "rule", Source: "user"})
	if err != nil || u.Source != "user" {
		t.Errorf("a user fact %+v %v", u, err)
	}
}
