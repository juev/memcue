package store

import (
	"database/sql"
	"errors"
	"os"
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

// A card carries the rules and decisions that still hold on the given day and skips the expired ones; a fact whose
// expiry is that day is still carried, as recall does.
func TestCardFactsSkipExpired(t *testing.T) {
	s, _ := open(t)
	add := func(content, kind, scope, expires string) Fact {
		t.Helper()
		f, err := s.AddWith(content, nil, "test 2026-10-20", expires,
			Meta{Kind: kind, Scope: scope, Source: "user"})
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	expiredRule := add("Expired rule.", "rule", "global", "2026-10-19")
	liveRule := add("Live rule.", "rule", "global", "2026-10-20")
	expiredDecision := add("Expired decision.", "decision", "repo:shop", "2026-10-19")
	liveDecision := add("Live decision.", "decision", "repo:shop", "")

	got, err := s.CardFacts("shop", "2026-10-20")
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, f := range got {
		ids[f.ID] = true
	}
	if len(got) != 2 || !ids[liveRule.ID] || !ids[liveDecision.ID] || ids[expiredRule.ID] || ids[expiredDecision.ID] {
		t.Errorf("card facts %+v, want only %s and %s", got, liveRule.ID, liveDecision.ID)
	}

	old := add("Ancient rule.", "rule", "global", "2000-01-01")
	got, err = s.CardFacts("shop", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range got {
		if f.ID == old.ID {
			t.Errorf("an empty today kept the expired fact %s", old.ID)
		}
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
	m := Meta{Kind: "pitfall", Scope: "host:db1", Source: "tool", Evidence: "error: x", PromptVersion: 3}
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
	m := Meta{Kind: "environment", Scope: "host:db1", Source: "tool", Evidence: "port 5432", PromptVersion: 2}
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

func TestRetireCandidateRefusesAnApprovedFact(t *testing.T) {
	s, _ := open(t)
	f, err := s.AddWith("Кандидат про порт gitea 3000.", []string{"gitea"}, "x", "", Meta{Review: Candidate})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(f.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RetireCandidate(f.ID); !errors.Is(err, ErrNotCandidate) {
		t.Errorf("retiring an approved fact: %v", err)
	}
	got, err := s.Get(f.ID)
	if err != nil || got.Status != Active || got.Review != "" || got.Version != 2 {
		t.Errorf("a refused candidate retirement changed the fact: %+v %v", got, err)
	}
	cand, err := s.AddWith("Другой кандидат.", nil, "", "", Meta{Review: Candidate})
	if err != nil {
		t.Fatal(err)
	}
	ret, err := s.RetireCandidate(cand.ID)
	if err != nil || ret.Status != Retired || ret.Version != 2 {
		t.Errorf("retiring a candidate: %+v %v", ret, err)
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

// The base may hold evidence and secrets; a new file is 0600 and an old 0644 one is tightened on open.
func TestBaseIsPrivate(t *testing.T) {
	for _, existing := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), "facts.db")
		if existing {
			if err := os.WriteFile(path, nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		s, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		fi, err := os.Stat(path)
		if err != nil {
			_ = s.Close()
			t.Fatal(err)
		}
		if got := fi.Mode().Perm(); got != 0o600 {
			t.Errorf("existing=%v: mode %o, want 600", existing, got)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

// A path is a file path, not a URI: characters SQLite would treat as URI syntax stay part of the name, only one file
// is created, and the file is 0600.
func TestSpecialCharactersStayInTheBasePath(t *testing.T) {
	for _, name := range []string{"plain.db", "a?b.db", "a#b.db", "a%20b.db", "a b.db"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, name)
			s, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			f, err := s.Add("A fact in a base with an unusual path.", nil, "")
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}

			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 || entries[0].Name() != name {
				t.Fatalf("files %v, want only %q", entries, name)
			}
			fi, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if got := fi.Mode().Perm(); got != 0o600 {
				t.Errorf("mode %o, want 600", got)
			}

			s, err = Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = s.Close() }()
			if got, err := s.Get(f.ID); err != nil || got.Content != f.Content {
				t.Errorf("reopened base: %+v, %v", got, err)
			}
		})
	}
}

// A URI is not a base path: accepting one half-way used to create a directory named "file:" in the working directory.
func TestFileURIRejected(t *testing.T) {
	dir := t.TempDir()
	if _, err := Open("file:" + filepath.Join(dir, "facts.db")); err == nil {
		t.Fatal("a file: URI opened")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("files created for a rejected URI: %v", entries)
	}
}

func TestOpenMemoryBase(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	f, err := s.Add("A fact in an in-memory base.", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.Get(f.ID); err != nil || got.Content != f.Content {
		t.Errorf("in-memory base: %+v, %v", got, err)
	}
}

// A lost index row must not hide a fact from facts or an audit; reopening the base repairs Search too.
func TestActiveSurvivesLostIndexRow(t *testing.T) {
	s, path := open(t)
	f, err := s.Add("Vaultwarden обслуживает vault.example.org", []string{"vaultwarden"}, "x 2026-10-20")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DELETE FROM fact_index`); err != nil {
		t.Fatal(err)
	}
	act, err := s.Active()
	if err != nil || len(act) != 1 || act[0].ID != f.ID {
		t.Fatalf("active without an index row: %+v %v", act, err)
	}
	if got := ids(t, s, "vaultwarden vault"); len(got) != 0 {
		t.Errorf("search found a fact whose index row is gone: %v", got)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if _, ok := ids(t, s, "vaultwarden vault")[f.ID]; !ok {
		t.Error("reopening did not repair the index")
	}
}

// The stems changed since version 1: reopening a base built with the old tokenizer must rebuild the index so a
// ticket key becomes findable without re-adding the fact.
func TestOpeningAnOldBaseRebuildsTicketStems(t *testing.T) {
	s, path := open(t)
	f, err := s.Add("Тикет PROJ-1234: берём в работу.", nil, "x 2026-10-20")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE fact_index SET stems = 'тикет'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`PRAGMA user_version = 1`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if _, ok := ids(t, s, "PROJ-1234")[f.ID]; !ok {
		t.Error("a reopened old base does not index the ticket key")
	}
	var version int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != schemaVersion {
		t.Errorf("schema version %d, want %d (%v)", version, schemaVersion, err)
	}
}

// A version-3 base has no fact_vectors; opening it must create the table and leave it usable.
func TestOpeningAVersion3BaseCreatesFactVectors(t *testing.T) {
	s, path := open(t)
	f, err := s.Add("PostgreSQL-16 runs in a container on port 55432.", nil, "x 2026-10-20")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DROP TABLE fact_vectors`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`PRAGMA user_version = 3`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if err := s.SaveVector(f.ID, "test-model", TextHash(f.Content), []float32{1, 0}); err != nil {
		t.Fatalf("saving a vector on a migrated base: %v", err)
	}
	if n, err := s.TagVectors("test-model"); err != nil || n != 1 {
		t.Errorf("vectors after migration = %d, %v; want 1", n, err)
	}
}

// A versioned name keeps its word in the index: the number is dropped, but "PostgreSQL" is still searchable.
func TestVersionedNameKeepsItsWordInTheIndex(t *testing.T) {
	s, _ := open(t)
	f, err := s.Add("PostgreSQL-16 runs in a container on port 55432.", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ids(t, s, "postgresql")[f.ID]; !ok {
		t.Error("a versioned name dropped its word from the index")
	}
}

// A version-2 base indexed a versioned name as one ticket term; opening it rebuilds the stems, so the system word is
// searchable again.
func TestOpeningAVersion2BaseRebuildsVersionedStems(t *testing.T) {
	s, path := open(t)
	f, err := s.Add("PostgreSQL-16 runs in a container on port 55432.", nil, "x 2026-10-20")
	if err != nil {
		t.Fatal(err)
	}
	// What version 2 wrote for "PostgreSQL-16": one ticket term, no "postgresql" stem.
	if _, err := s.db.Exec(`UPDATE fact_index SET stems = ? WHERE id = ?`, "tkpostgresql16", f.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`PRAGMA user_version = 2`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	var version int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != schemaVersion {
		t.Errorf("schema version %d, want %d (%v)", version, schemaVersion, err)
	}
	if _, ok := ids(t, s, "postgresql")[f.ID]; !ok {
		t.Error("a reopened version-2 base does not index the versioned name")
	}
}

func TestBaseOfAFutureVersionIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "facts.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`PRAGMA user_version = 99`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Error("a base of schema 99 opened")
	}
}
