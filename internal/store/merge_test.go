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

func TestMergeCarriesMetaAndCandidates(t *testing.T) {
	b, _ := base(t)
	c := clone(t, b)
	sc := openAt(t, c, "loffe", t0.Add(time.Minute))
	m := Meta{Kind: "pitfall", Scope: "host:db1", Source: "tool", Evidence: "exit 1", Review: Candidate}
	f, err := sc.AddWith("Кандидат с loffe про db1.", nil, "x 2026-10-20", "", m)
	if err != nil {
		t.Fatal(err)
	}
	_ = sc.Close()
	s := openAt(t, b, "mac", t0.Add(2*time.Minute))
	if _, err := s.Merge(c); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(f.ID)
	if err != nil || got.Meta != m {
		t.Errorf("merged %+v %v", got, err)
	}
	if !strings.Contains(dump(t, s), "kind=pitfall scope=host:db1 source=tool review=candidate") {
		t.Errorf("dump lacks meta: %s", dump(t, s))
	}
	if _, ok := ids(t, s, "кандидат loffe db1")[f.ID]; ok {
		t.Error("a merged candidate is recalled")
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
