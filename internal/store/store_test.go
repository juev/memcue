package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/juev/memcue/internal/text"
)

func open(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "facts.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, path
}

func ids(t *testing.T, s *Store, q string) map[string]Hit {
	t.Helper()
	hs, err := s.Search(text.Terms(q), 50)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]Hit{}
	for _, h := range hs {
		m[h.ID] = h
	}
	return m
}

func TestAddStoresVersionOneAndIsFound(t *testing.T) {
	s, _ := open(t)
	f, err := s.Add("Gradle вне make требует JAVA_HOME=/opt/homebrew/opt/openjdk@21", []string{"gradle", "jdk"}, "lists 2026-10-09")
	if err != nil {
		t.Fatal(err)
	}
	if f.Version != 1 || f.Status != Active || len(f.ID) != 26 {
		t.Errorf("got %+v", f)
	}
	got := ids(t, s, "какой jdk нужен gradle")
	if h, ok := got[f.ID]; !ok || h.Origin != "lists 2026-10-09" || len(h.Keywords) != 2 {
		t.Errorf("search: %+v", got)
	}
}

func TestKeywordsAreSearchable(t *testing.T) {
	s, _ := open(t)
	f, err := s.Add("Путь к JDK задаётся переменной окружения.", []string{"openjdk", "android"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ids(t, s, "openjdk android")[f.ID]; !ok {
		t.Error("not found by keywords")
	}
}

func TestCorrectAddsVersionAndSearchSeesOnlyLatest(t *testing.T) {
	s, _ := open(t)
	f, _ := s.Add("PDS работает на server1 по адресу pds.example.org", nil, "server-migration 2026-08-01")
	g, err := s.Correct(f.ID, "PDS остановлен 2026-10-02, данные сохранены", nil)
	if err != nil {
		t.Fatal(err)
	}
	if g.Version != 2 || g.Origin != f.Origin {
		t.Errorf("got %+v", g)
	}
	if h, ok := ids(t, s, "pds работает адрес")[f.ID]; ok && h.Version != 2 {
		t.Errorf("old version found: %+v", h)
	}
	if h, ok := ids(t, s, "pds остановлен")[f.ID]; !ok || h.Content != g.Content {
		t.Errorf("latest version not found: %+v", h)
	}
	hist, err := s.History(f.ID)
	if err != nil || len(hist) != 2 || hist[0].Content != f.Content {
		t.Errorf("history %+v %v", hist, err)
	}
}

func TestRetireTakesFactOutOfSearchButKeepsHistory(t *testing.T) {
	s, _ := open(t)
	f, _ := s.Add("Vaultwarden обслуживает vault.example.org", nil, "")
	if _, err := s.Retire(f.ID); err != nil {
		t.Fatal(err)
	}
	if got := ids(t, s, "vaultwarden vault"); len(got) != 0 {
		t.Errorf("retired fact found: %v", got)
	}
	act, _ := s.Active()
	if len(act) != 0 {
		t.Errorf("active: %+v", act)
	}
	cur, err := s.Get(f.ID)
	if err != nil || cur.Status != Retired || cur.Content != f.Content {
		t.Errorf("get %+v %v", cur, err)
	}
	if _, err := s.Correct(f.ID, "Vaultwarden снова работает", nil); err != nil {
		t.Fatal(err)
	}
	if len(ids(t, s, "vaultwarden работает")) != 1 {
		t.Error("revived fact not found")
	}
}

func TestUnknownIDIsNotFound(t *testing.T) {
	s, _ := open(t)
	if _, err := s.Correct("01ARZ3NDEKTSV4RRFFQ69G5FAV", "x", nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("got %v", err)
	}
}

func TestEmptyFactRejected(t *testing.T) {
	s, _ := open(t)
	if _, err := s.Add("  ", nil, ""); err == nil {
		t.Error("empty fact accepted")
	}
}

func TestRebuildRestoresIndexFromRows(t *testing.T) {
	s, path := open(t)
	a, _ := s.Add("Caddy-private монтирует файл Caddyfile-private", nil, "")
	b, _ := s.Add("Публичный Caddy монтирует каталог configs/caddy", nil, "")
	_, _ = s.Retire(b.ID)
	if _, err := s.db.Exec(`DELETE FROM fact_index`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO fact_fts(fact_fts) VALUES ('delete-all')`); err != nil {
		t.Fatal(err)
	}
	if err := s.Rebuild(); err != nil {
		t.Fatal(err)
	}
	got := ids(t, s, "caddy монтирует")
	if _, ok := got[a.ID]; !ok || len(got) != 1 {
		t.Errorf("after rebuild: %v", got)
	}
	_ = s.Close()
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s2.Close() }()
	if len(ids(t, s2, "caddy монтирует")) != 1 {
		t.Error("reopen lost the index")
	}
}

func TestIDsSortByCreationTime(t *testing.T) {
	s, _ := open(t)
	base := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return base }
	a, _ := s.Add("первый факт про что-то важное", nil, "")
	s.now = func() time.Time { return base.Add(time.Second) }
	b, _ := s.Add("второй факт про что-то другое", nil, "")
	if a.ID >= b.ID {
		t.Errorf("%s !< %s", a.ID, b.ID)
	}
}

func TestDocFreqCountsActiveFacts(t *testing.T) {
	s, _ := open(t)
	_, _ = s.Add("прокси mihomo на порту 1080", nil, "")
	b, _ := s.Add("прокси для контейнеров задаётся env", nil, "")
	_, _ = s.Add("база данных freshgo в sqlite", nil, "")
	_, _ = s.Retire(b.ID)
	df, total, err := s.DocFreq(text.Terms("прокси"))
	if err != nil || total != 2 || df[text.Terms("прокси")[0]] != 1 {
		t.Errorf("df %v total %d err %v", df, total, err)
	}
}

func TestExpiryIsStoredAndKeptByCorrection(t *testing.T) {
	s, _ := open(t)
	f, err := s.AddExpiring("Крон memcue-dream-check придёт 21 октября.", []string{"dream"}, "hermes 2026-10-19",
		"2026-10-21")
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Expires != "2026-10-21" {
		t.Errorf("expiry %q, want 2026-10-21", got.Expires)
	}
	kept, err := s.Correct(f.ID, "Крон memcue-dream-check перенесён на 22 октября.", []string{"dream"})
	if err != nil {
		t.Fatal(err)
	}
	if kept.Expires != "2026-10-21" {
		t.Errorf("correction dropped the expiry: %q", kept.Expires)
	}
	empty := ""
	cleared, err := s.CorrectExpiring(f.ID, "Крон memcue-dream-check отменён.", []string{"dream"}, &empty)
	if err != nil {
		t.Fatal(err)
	}
	if cleared.Expires != "" {
		t.Errorf("expiry not cleared: %q", cleared.Expires)
	}
}

func TestBadExpiryIsRejected(t *testing.T) {
	s, _ := open(t)
	if _, err := s.AddExpiring("Срок задан неверно.", nil, "", "21.10.2026"); err == nil {
		t.Error("21.10.2026 accepted, want an error: the date must be YYYY-MM-DD")
	}
	if _, err := s.AddExpiring("Срок задан неверно.", nil, "", "2026-13-45"); err == nil {
		t.Error("impossible date accepted")
	}
}

func TestBaseWithoutExpiresColumnStillOpens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	// The base as a memcue before expiry wrote it: no expires column, schema version 1.
	if _, err := db.Exec(`CREATE TABLE facts (
		id TEXT NOT NULL, version INTEGER NOT NULL, content TEXT NOT NULL, keywords TEXT NOT NULL DEFAULT '',
		origin TEXT NOT NULL DEFAULT '', created TEXT NOT NULL,
		status TEXT NOT NULL CHECK (status IN ('active', 'retired')), author TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (id, version)) WITHOUT ROWID;
		CREATE TABLE fact_index (rid INTEGER PRIMARY KEY, id TEXT NOT NULL UNIQUE, stems TEXT NOT NULL);
		CREATE VIRTUAL TABLE fact_fts USING fts5(stems, content='fact_index', content_rowid='rid');
		PRAGMA user_version = 1;`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	s, err := Open(path)
	if err != nil {
		t.Fatalf("opening an old base: %v", err)
	}
	defer func() { _ = s.Close() }()
	f, err := s.AddExpiring("Старая база приняла факт со сроком.", nil, "", "2026-11-01")
	if err != nil {
		t.Fatalf("adding to a migrated base: %v", err)
	}
	got, err := s.Get(f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Expires != "2026-11-01" {
		t.Errorf("expiry %q after migration", got.Expires)
	}
	m := Meta{Kind: "pitfall", Scope: "host:db1", Source: "tool", Evidence: "error: x"}
	f, err = s.AddWith("Старая база приняла факт с типом.", nil, "", "", m)
	if err != nil {
		t.Fatalf("adding meta to a migrated base: %v", err)
	}
	if got, _ := s.Get(f.ID); got.Meta != m {
		t.Errorf("meta %+v after migration", got.Meta)
	}
}

func TestMetaIsStoredAndKeptByCorrection(t *testing.T) {
	s, _ := open(t)
	m := Meta{Kind: "environment", Scope: "host:db1", Source: "tool", Evidence: "port 5432"}
	f, err := s.AddWith("Postgres на db1 слушает порт 5432.", []string{"postgres"}, "x 2026-10-20", "", m)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Correct(f.ID, "Postgres на db1 слушает порт 5433.", nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.Meta != m {
		t.Errorf("correction lost meta: %+v", c.Meta)
	}
	if got, _ := s.Get(f.ID); got.Meta != m || got.Version != 2 {
		t.Errorf("stored %+v", got)
	}
}

func TestCandidateIsNotRecalled(t *testing.T) {
	s, _ := open(t)
	f, err := s.AddWith("Кандидат про порт gitea 3000 ждёт одобрения.", []string{"gitea"}, "", "",
		Meta{Kind: "environment", Review: Candidate})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ids(t, s, "порт gitea")[f.ID]; ok {
		t.Error("a candidate was found by search")
	}
	act, err := s.Active()
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range act {
		if a.ID == f.ID {
			t.Error("a candidate is listed as active")
		}
	}
	if err := s.Rebuild(); err != nil {
		t.Fatal(err)
	}
	if _, ok := ids(t, s, "порт gitea")[f.ID]; ok {
		t.Error("a rebuild indexed a candidate")
	}
	if got, err := s.Get(f.ID); err != nil || got.Review != Candidate {
		t.Errorf("candidate not kept: %+v %v", got, err)
	}
}

func TestApproveTakesACandidateIntoRecall(t *testing.T) {
	s, _ := open(t)
	f, err := s.AddWith("Кандидат про порт gitea 3000.", []string{"gitea"}, "x", "", Meta{Kind: "environment",
		Source: "tool", Review: Candidate})
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Candidates()
	if err != nil || len(c) != 1 || c[0].ID != f.ID {
		t.Fatalf("candidates %+v %v", c, err)
	}
	a, err := s.Approve(f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if a.Version != 2 || a.Review != "" || a.Kind != "environment" || a.Source != "tool" {
		t.Errorf("approved %+v", a)
	}
	if _, ok := ids(t, s, "порт gitea")[f.ID]; !ok {
		t.Error("an approved fact is not found")
	}
	if c, _ := s.Candidates(); len(c) != 0 {
		t.Errorf("still a candidate: %+v", c)
	}
}

func TestApproveRefusesWhatIsNotACandidate(t *testing.T) {
	s, _ := open(t)
	act, _ := s.Add("Активный факт.", nil, "")
	ret, _ := s.AddWith("Снятый кандидат.", nil, "", "", Meta{Review: Candidate})
	if _, err := s.Retire(ret.ID); err != nil {
		t.Fatal(err)
	}
	for name, id := range map[string]string{"active": act.ID, "retired": ret.ID} {
		if _, err := s.Approve(id); !errors.Is(err, ErrNotCandidate) {
			t.Errorf("%s: err %v", name, err)
		}
	}
	if _, err := s.Approve("01NOPE"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown: err %v", err)
	}
	if got, _ := s.Get(ret.ID); got.Status != Retired || got.Version != 2 {
		t.Errorf("a refused approval wrote a version: %+v", got)
	}
}

func TestCorrectingACandidateKeepsItACandidate(t *testing.T) {
	s, _ := open(t)
	f, _ := s.AddWith("Кандидат про порт gitea 3000.", []string{"gitea"}, "", "", Meta{Review: Candidate})
	c, err := s.Correct(f.ID, "Кандидат про порт gitea 3001.", nil)
	if err != nil || c.Review != Candidate {
		t.Fatalf("corrected %+v %v", c, err)
	}
	if _, ok := ids(t, s, "порт gitea")[f.ID]; ok {
		t.Error("a correction took a candidate into recall")
	}
}

// The review page shows the newest candidates first, so a day's batch does not start with yesterday's leftovers.
func TestCandidatesAreNewestFirst(t *testing.T) {
	s, _ := open(t)
	now := time.Unix(1_700_000_000, 0)
	s.now = func() time.Time { return now }
	var first, second Fact
	for i, content := range []string{"Первый кандидат про gitea.", "Второй кандидат про gitea."} {
		now = now.Add(time.Second)
		f, err := s.AddWith(content, []string{"gitea"}, "", "", Meta{Review: Candidate})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = f
		} else {
			second = f
		}
	}
	cs, err := s.Candidates()
	if err != nil || len(cs) != 2 || cs[0].ID != second.ID || cs[1].ID != first.ID {
		t.Fatalf("candidates %+v %v", cs, err)
	}
}

// Undo of an approval or a rejection is a new version in review, never a deletion or a rewrite.
func TestReopenPutsAFactBackIntoReview(t *testing.T) {
	s, _ := open(t)
	f, _ := s.AddWith("Кандидат про порт gitea 3000.", []string{"gitea"}, "", "", Meta{Review: Candidate})
	if _, err := s.Approve(f.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reopen(f.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(f.ID)
	if got.Version != 3 || got.Review != Candidate || got.Status != Active {
		t.Errorf("after undo of approve %+v", got)
	}
	if _, ok := ids(t, s, "порт gitea")[f.ID]; ok {
		t.Error("a reopened fact is still found by search")
	}
	if _, err := s.Reopen(f.ID); !errors.Is(err, ErrAlreadyCandidate) {
		t.Errorf("reopening a candidate: %v", err)
	}
	if _, err := s.Retire(f.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reopen(f.ID); err != nil {
		t.Fatalf("undo of reject: %v", err)
	}
	got, _ = s.Get(f.ID)
	if got.Version != 5 || got.Review != Candidate || got.Status != Active {
		t.Errorf("after undo of reject %+v", got)
	}
}
