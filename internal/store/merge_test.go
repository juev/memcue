package store

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// clone copies the base at path to a new file, as git does for the two sides of a merge.
func clone(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "facts.db")
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func openAt(t *testing.T, path, author string, at time.Time) *Store {
	t.Helper()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.author = author
	s.now = func() time.Time { return at }
	return s
}

var t0 = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

func base(t *testing.T) (string, Fact) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "facts.db")
	s := openAt(t, p, "mac", t0)
	f, err := s.Add("Hermes работает на модели deepseek-flash", []string{"hermes"}, "server-migration 2026-10-17")
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	return p, f
}

func dump(t *testing.T, s *Store) string {
	t.Helper()
	var b bytes.Buffer
	if err := s.Dump(&b); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestMergeUnitesFactsAddedOnBothSides(t *testing.T) {
	b, _ := base(t)
	mac, server := clone(t, b), clone(t, b)
	m := openAt(t, mac, "mac", t0.Add(time.Hour))
	_, _ = m.Add("Caddy-private монтирует файл, после замены пересоздать контейнер", nil, "server-migration")
	l := openAt(t, server, "server", t0.Add(2*time.Hour))
	_, _ = l.Add("Updater держит по два rollback-образа на сервис", nil, "server-migration")
	_ = l.Close()

	st, err := m.Merge(server)
	if err != nil {
		t.Fatal(err)
	}
	if st.Added != 1 || st.Diverged != 0 {
		t.Errorf("stats %+v", st)
	}
	act, _ := m.Active()
	if len(act) != 3 {
		t.Errorf("active %d", len(act))
	}
	if len(ids(t, m, "rollback образа updater")) != 1 {
		t.Error("merged fact not searchable")
	}
	_ = m.Close()
}

func TestMergeIsTheSameFromBothSides(t *testing.T) {
	b, f := base(t)
	mac, server := clone(t, b), clone(t, b)
	m := openAt(t, mac, "mac", t0.Add(time.Hour))
	_, _ = m.Correct(f.ID, "Hermes работает на deepseek-flash, fallback Codex", nil)
	_ = m.Close()
	l := openAt(t, server, "server", t0.Add(2*time.Hour))
	_, _ = l.Retire(f.ID)
	_ = l.Close()

	a, c := clone(t, mac), clone(t, server)
	sa := openAt(t, a, "mac", t0)
	st, err := sa.Merge(server)
	if err != nil {
		t.Fatal(err)
	}
	if st.Diverged != 1 {
		t.Errorf("stats %+v", st)
	}
	sc := openAt(t, c, "server", t0)
	if _, err := sc.Merge(mac); err != nil {
		t.Fatal(err)
	}
	if da, dc := dump(t, sa), dump(t, sc); da != dc {
		t.Errorf("merge depends on side:\n%s\nvs\n%s", da, dc)
	}
	hist, _ := sa.History(f.ID)
	if len(hist) != 3 || !strings.Contains(hist[1].Content, "fallback Codex") || hist[2].Status != Retired {
		t.Errorf("history %+v", hist)
	}
	if len(ids(t, sa, "hermes deepseek")) != 0 {
		t.Error("the later retirement did not win")
	}
	_ = sa.Close()
	_ = sc.Close()
}

func TestMergeOfIdenticalCopiesChangesNothing(t *testing.T) {
	b, _ := base(t)
	c := clone(t, b)
	s := openAt(t, b, "mac", t0)
	before := dump(t, s)
	st, err := s.Merge(c)
	if err != nil || st != (MergeStats{}) || dump(t, s) != before {
		t.Errorf("stats %+v err %v", st, err)
	}
	_ = s.Close()
}

// olderCopy turns the base at path into one an older memcue wrote: schema 5, with rows such a memcue stored.
func olderCopy(t *testing.T, path string, rows ...Fact) {
	t.Helper()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	legacyRows(t, s, rows...)
	if _, err := s.db.Exec(`PRAGMA user_version = 5`); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
}

// A candidate synced from a machine that still runs an older memcue keeps its row and its meta, and is found here
// (#514).
func TestMergeCarriesMetaAndCandidates(t *testing.T) {
	b, _ := base(t)
	c := clone(t, b)
	m := Meta{Kind: "pitfall", Scope: "host:db1", Source: "tool", Evidence: "exit 1", Review: Candidate}
	f := Fact{ID: "01JLEGACYCANDIDATE00000010", Version: 1, Content: "Кандидат с builder про db1.",
		Origin: "x 2026-10-20", Author: "builder", Created: t0.Add(time.Minute), Meta: m}
	olderCopy(t, c, f)
	s := openAt(t, b, "mac", t0.Add(2*time.Minute))
	if _, err := s.Merge(c); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(f.ID)
	if err != nil || got.Meta != m || got.Version != 1 {
		t.Errorf("merged %+v %v", got, err)
	}
	if !strings.Contains(dump(t, s), "kind=pitfall scope=host:db1 source=tool review=candidate") {
		t.Errorf("dump lacks meta: %s", dump(t, s))
	}
	if _, ok := ids(t, s, "кандидат builder db1")[f.ID]; !ok {
		t.Error("a merged candidate is not recalled")
	}
	_ = s.Close()
}

func TestDumpOneLinePerVersion(t *testing.T) {
	b, f := base(t)
	s := openAt(t, b, "mac", t0.Add(time.Minute))
	_, _ = s.Correct(f.ID, "Hermes:\nмодель deepseek-flash", nil)
	var w bytes.Buffer
	if err := s.Dump(io.Writer(&w)); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(w.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[1], " v2 active ") || !strings.HasSuffix(lines[1], "Hermes: модель deepseek-flash") {
		t.Errorf("dump %q", lines)
	}
	_ = s.Close()
}

// A machine on an older memcue corrects a candidate while this one retires it later: the merge keeps both versions,
// the retire is the latest, and the candidate state of the older machine does not bring the fact back.
func TestMergeOfAnOlderCorrectionAndARetire(t *testing.T) {
	b := filepath.Join(t.TempDir(), "facts.db")
	m := Meta{Kind: "environment", Review: Candidate}
	v1 := Fact{ID: "01JLEGACYCANDIDATE00000011", Version: 1, Content: "Кандидат про db1.", Keywords: []string{"db1"},
		Origin: "x", Author: "mac", Created: t0, Meta: m}
	olderCopy(t, b, v1)
	c := clone(t, b)
	v2 := v1
	v2.Version, v2.Content, v2.Author, v2.Created = 2, "Кандидат про db1, уточнён.", "builder", t0.Add(time.Minute)
	olderCopy(t, c, v2)
	s := openAt(t, b, "mac", t0.Add(2*time.Minute))
	if _, err := s.Retire(v1.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Merge(c); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(v1.ID)
	if err != nil || got.Status != Retired || got.Version != 3 {
		t.Errorf("merged %+v %v", got, err)
	}
	if h, _ := s.History(v1.ID); len(h) != 3 || h[1].Content != "Кандидат про db1, уточнён." || h[1].Review != Candidate {
		t.Errorf("history %+v", h)
	}
	if _, ok := ids(t, s, "кандидат db1")[v1.ID]; ok {
		t.Error("a retired candidate is recalled after the merge")
	}
	_ = s.Close()
}

// A side that has not pulled a merge yet merges it later: the version both tails hold is one write and stays one
// version (#540). Before, B's correction sat at v2 on B and at v3 on A, both tails carried it, and B got a v4 copy.
func TestMergeOfAStaleSideKeepsOneCopyOfAVersion(t *testing.T) {
	b, f := base(t)
	a, bb := clone(t, b), clone(t, b)
	sa := openAt(t, a, "mac", t0.Add(time.Hour))
	if _, err := sa.Correct(f.ID, "Hermes работает на deepseek-pro", nil); err != nil {
		t.Fatal(err)
	}
	sb := openAt(t, bb, "server", t0.Add(2*time.Hour))
	if _, err := sb.Correct(f.ID, "Hermes работает на deepseek-pro, fallback Codex", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := sa.Merge(bb); err != nil { // A pulls B: B's correction becomes v3 on A
		t.Fatal(err)
	}
	if _, err := sb.Add("Updater держит по два rollback-образа на сервис", nil, "server-migration"); err != nil {
		t.Fatal(err)
	}
	st, err := sb.Merge(a) // B, before pulling, writes again and then merges A
	if err != nil {
		t.Fatal(err)
	}
	if st.Added != 1 {
		t.Errorf("stats %+v, want one version taken from A", st)
	}
	h, err := sb.History(f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(h) != 3 || !strings.Contains(h[2].Content, "fallback Codex") || h[2].Version != 3 {
		t.Fatalf("history of %d versions, want 3 with B's correction last: %+v", len(h), h)
	}
	// A pulls the result: nothing new for A, and both sides hold the same base.
	if st, err := sa.Merge(bb); err != nil || st.Added != 1 || st.Diverged != 0 {
		t.Errorf("A pulls B: stats %+v %v, want only B's new fact", st, err)
	}
	if da, db := dump(t, sa), dump(t, sb); da != db {
		t.Errorf("the sides differ:\n%s\nvs\n%s", da, db)
	}
	if st, err := sb.Merge(a); err != nil || st.Added != 0 {
		t.Errorf("merging again: stats %+v %v, want nothing", st, err)
	}
	_ = sa.Close()
	_ = sb.Close()
}

// A merge carries no vectors: a copy synced from an older binary may still hold them, and nothing reads them (#260).
func TestMergeCarriesNoVectors(t *testing.T) {
	macPath := filepath.Join(t.TempDir(), "mac.db")
	m := openAt(t, macPath, "mac", t0)
	serverPath := filepath.Join(t.TempDir(), "server.db")
	server := openAt(t, serverPath, "server", t0.Add(time.Hour))
	sf, err := server.Add("Факт server про порт 3001.", nil, "y")
	if err != nil {
		t.Fatal(err)
	}
	saveVector(t, server, sf.ID)
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Merge(serverPath); err != nil {
		t.Fatal(err)
	}
	if n := countVectors(t, m); n != 0 {
		t.Fatalf("vectors after merge = %d, want none", n)
	}
	if _, err := m.Get(sf.ID); err != nil {
		t.Errorf("the fact of the other copy was not merged: %v", err)
	}
}

// The queries one machine wrote reach the other with the merge, and the merged fact is found by them.
func TestMergeCarriesQueries(t *testing.T) {
	b, f := base(t)
	c := clone(t, b)
	sc := openAt(t, c, "loffe", t0.Add(time.Minute))
	if _, err := sc.SetQueries(f.ID, f.Version, []string{"языковая модель гермеса"}); err != nil {
		t.Fatal(err)
	}
	_ = sc.Close()
	s := openAt(t, b, "mac", t0.Add(2*time.Minute))
	if _, err := s.Merge(c); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(f.ID)
	if err != nil || got.Version != 2 || len(got.Queries) != 1 {
		t.Errorf("merged %+v %v", got, err)
	}
	if !strings.Contains(dump(t, s), "queries=[языковая модель гермеса]") {
		t.Errorf("dump lacks the queries: %s", dump(t, s))
	}
	if len(ids(t, s, "языковая модель")) != 1 {
		t.Error("the merged queries are not searched")
	}
	_ = s.Close()
}
