package store

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
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

func TestAddBatchStoresEveryFact(t *testing.T) {
	s, _ := open(t)
	facts, err := s.AddBatch([]FactInput{
		{Content: "Первая партия: очередь RabbitMQ на порту 5672.", Keywords: []string{"rabbitmq", "queue"}, Origin: "test"},
		{Content: "Вторая партия: кеш Redis на порту 6379.", Keywords: []string{"redis", "cache"}, Origin: "test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 2 {
		t.Fatalf("got %d facts", len(facts))
	}
	got := ids(t, s, "rabbitmq redis")
	for _, f := range facts {
		if _, ok := got[f.ID]; !ok {
			t.Errorf("fact %s not searchable", f.ID)
		}
	}
}

// Facts written in one batch get increasing ids, so a reader that orders by id cannot reverse their creation order.
func TestAddBatchIDsIncrease(t *testing.T) {
	s, _ := open(t)
	inputs := make([]FactInput, 0, 20)
	for i := 0; i < 20; i++ {
		inputs = append(inputs, FactInput{Content: fmt.Sprintf("Факт номер %d о демо-базе.", i), Origin: "test"})
	}
	facts, err := s.AddBatch(inputs)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(facts); i++ {
		if facts[i-1].ID >= facts[i].ID {
			t.Fatalf("ids of one batch are not increasing: %s then %s", facts[i-1].ID, facts[i].ID)
		}
	}
}

// A base written before the monotonic ids can hold same-millisecond facts whose ids are in random order. Candidates
// must still come newest first by created, not by the accidental id order.
func TestCandidatesOrderByCreatedNotID(t *testing.T) {
	s, _ := open(t)
	facts, err := s.AddBatch([]FactInput{
		{Content: "Первый кандидат.", Meta: Meta{Review: Candidate}},
		{Content: "Второй кандидат.", Meta: Meta{Review: Candidate}},
		{Content: "Третий кандидат.", Meta: Meta{Review: Candidate}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// The smallest id gets the newest created time, the largest id the oldest: an id-only ORDER BY would reverse this.
	created := []string{
		"2026-10-21T12:00:00Z",
		"2026-10-20T12:00:00Z",
		"2026-10-19T12:00:00Z",
	}
	for i, f := range facts {
		if _, err := s.db.Exec(`UPDATE facts SET created = ? WHERE id = ?`, created[i], f.ID); err != nil {
			t.Fatal(err)
		}
	}
	cands, err := s.Candidates()
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 3 {
		t.Fatalf("candidates = %d, want 3", len(cands))
	}
	for i, want := range []string{facts[0].ID, facts[1].ID, facts[2].ID} {
		if cands[i].ID != want {
			t.Fatalf("candidate %d = %s, want %s (order: %v)", i, cands[i].ID, want, cands)
		}
	}
}

func TestAddBatchIsAllOrNothing(t *testing.T) {
	s, _ := open(t)
	if _, err := s.AddBatch([]FactInput{
		{Content: "Этот факт не должен сохраниться.", Origin: "test"},
		{Content: "  ", Origin: "test"},
	}); err == nil {
		t.Fatal("expected an error for the empty fact")
	}
	rows, err := s.Rows()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("a failed batch left %d rows", len(rows))
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
	if _, err := s.Correct(f.ID, "Vaultwarden снова работает", nil); !errors.Is(err, ErrRetired) {
		t.Fatalf("correcting a retired fact: %v, want ErrRetired", err)
	}
	if len(ids(t, s, "vaultwarden работает")) != 0 {
		t.Error("a refused correction put the retired fact back into the index")
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

// An expiry is a local calendar day: the same instant is 21 October in UTC and 22 October in a +03:00 zone.
func TestTodayAtUsesTheTimesLocation(t *testing.T) {
	instant := time.Date(2026, 10, 21, 23, 30, 0, 0, time.UTC)
	if got := TodayAt(instant); got != "2026-10-21" {
		t.Errorf("UTC date = %s, want 2026-10-21", got)
	}
	if got := TodayAt(instant.In(time.FixedZone("MSK", 3*3600))); got != "2026-10-22" {
		t.Errorf("+03:00 date = %s, want 2026-10-22", got)
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

func TestProvisionalIsRecallableButNotTrusted(t *testing.T) {
	s, _ := open(t)
	until := time.Now().AddDate(0, 0, 7).Format(time.DateOnly)
	cand, err := s.AddWith("В контейнере gitea порт 3000.", []string{"gitea"}, "x", "",
		Meta{Review: Candidate})
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.ApproveProvisional(cand.ID, until)
	if err != nil {
		t.Fatal(err)
	}
	if p.Review != Provisional || p.Expires != until || p.Version != 2 {
		t.Errorf("provisional = %+v", p)
	}
	if _, ok := ids(t, s, "gitea")[cand.ID]; !ok {
		t.Error("provisional fact missing from search")
	}
	active, err := s.Active()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range active {
		if f.ID == cand.ID {
			t.Error("provisional fact is in the trusted active set")
		}
	}
	if cs, _ := s.Candidates(); len(cs) != 0 {
		t.Errorf("provisional fact is back in the human queue: %+v", cs)
	}
	promoted, err := s.Approve(cand.ID)
	if err != nil {
		t.Fatal(err)
	}
	if promoted.Review != "" || promoted.Expires != "" {
		t.Errorf("promoted = %+v, want an approved fact without the provisional TTL", promoted)
	}
	cand2, err := s.AddWith("В контейнере gitea порт 3001.", nil, "x", "", Meta{Review: Candidate})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApproveProvisional(cand2.ID, until); err != nil {
		t.Fatal(err)
	}
	ret, err := s.RetireCandidate(cand2.ID)
	if err != nil || ret.Status != Retired {
		t.Fatalf("retire provisional = %+v %v", ret, err)
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

// A split retires the compound candidate and stores each part as a candidate of its own, with the origin, expiry and
// meta of the original.
func TestSplitCandidate(t *testing.T) {
	s, _ := open(t)
	meta := Meta{Kind: "environment", Scope: "host:loffe", Source: "tool", Evidence: "memcue version", Review: Candidate}
	f, err := s.AddWith("На loffe стоит memcue 0.4.0; бинарь лежит в /opt/data/bin.", []string{"memcue"},
		"hermes 2026-10-22", "2027-01-01", meta)
	if err != nil {
		t.Fatal(err)
	}
	parts, err := s.SplitCandidate(f.ID, []string{" На loffe стоит memcue 0.4.0. ", "Бинарь memcue на loffe лежит в /opt/data/bin."})
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 2 || parts[0].Content != "На loffe стоит memcue 0.4.0." {
		t.Fatalf("parts %+v", parts)
	}
	for _, p := range parts {
		got, err := s.Get(p.ID)
		if err != nil || got.Origin != f.Origin || got.Expires != f.Expires || got.Meta != meta ||
			got.Status != Active || got.Version != 1 || len(got.Keywords) != 0 {
			t.Errorf("part %+v %v", got, err)
		}
	}
	if got, _ := s.Get(f.ID); got.Status != Retired || got.Version != 2 || got.Content != f.Content {
		t.Errorf("original %+v", got)
	}
	cands, err := s.Candidates()
	if err != nil || len(cands) != 2 {
		t.Errorf("candidates %+v %v", cands, err)
	}
}

// A split that cannot be applied whole changes nothing: an approved fact, one part, or an empty part.
func TestSplitCandidateRefuses(t *testing.T) {
	s, _ := open(t)
	approved, _ := s.Add("Одобренный факт про порт 3000 и путь /srv.", nil, "")
	if _, err := s.SplitCandidate(approved.ID, []string{"Порт 3000.", "Путь /srv."}); !errors.Is(err, ErrNotCandidate) {
		t.Errorf("splitting an approved fact: %v", err)
	}
	cand, _ := s.AddWith("Кандидат про порт 3000 и путь /srv.", nil, "", "", Meta{Review: Candidate})
	for _, parts := range [][]string{{"Порт 3000."}, {"Порт 3000.", "  "}} {
		if _, err := s.SplitCandidate(cand.ID, parts); err == nil {
			t.Errorf("split into %q was accepted", parts)
		}
	}
	rows, err := s.Rows()
	if err != nil || len(rows) != 2 {
		t.Errorf("a refused split wrote to the base: %d rows %v", len(rows), err)
	}
	if got, _ := s.Get(cand.ID); got.Status != Active || got.Review != Candidate || got.Version != 1 {
		t.Errorf("candidate after refused splits %+v", got)
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

// Retirement is final: a correction must not revive a fact a vet, a review rejection or a person retired.
func TestCorrectingARetiredFactIsRefused(t *testing.T) {
	s, _ := open(t)
	f, err := s.Add("Факт про порт gitea 3000.", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Retire(f.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Correct(f.ID, "Факт про порт gitea 3001.", nil); !errors.Is(err, ErrRetired) {
		t.Fatalf("correcting a retired fact: %v, want ErrRetired", err)
	}
	got, err := s.Get(f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != Retired || got.Version != 2 || got.Content != "Факт про порт gitea 3000." {
		t.Errorf("a refused correction changed the fact: %+v", got)
	}
	if _, ok := ids(t, s, "порт gitea")[f.ID]; ok {
		t.Error("a refused correction put a retired fact back into the index")
	}
}

// Candidates come newest first, so a day's batch does not start with yesterday's leftovers.
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

// A base already at the current schema still gets an additive index: a new index must reach existing installations,
// not only a fresh file. This is the regression from #151, where facts_created was created only by migrate().
func TestOpeningACurrentBaseAddsMissingIndexes(t *testing.T) {
	s, path := open(t)
	if _, err := s.db.Exec(`DROP INDEX facts_created`); err != nil {
		t.Fatal(err)
	}
	var version int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != schemaVersion {
		t.Fatalf("setup: schema %d, want %d (%v)", version, schemaVersion, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name = 'facts_created'`).
		Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("facts_created after reopening a current base: %d, want 1", n)
	}
}

// A textconv must not write to the file it reads: OpenReadOnly does not migrate a version-3 base, tighten its mode,
// rebuild the index or change a byte.
func TestOpenReadOnlyDoesNotTouchTheBase(t *testing.T) {
	s, path := open(t)
	if _, err := s.Add("Gitea слушает порт 3000.", []string{"gitea"}, "demo 2026-10-20"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`PRAGMA user_version = 3`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ro, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := ro.Dump(io.Discard); err != nil {
		t.Fatalf("dump of a version-3 base: %v", err)
	}
	var version int
	if err := ro.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != 3 {
		t.Errorf("read-only open migrated the base: version %d, %v", version, err)
	}
	if err := ro.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("a read-only dump changed the base file")
	}
}

// A missing path is not a base and must not be created by dump.
func TestOpenReadOnlyMissingBaseCreatesNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.db")
	if _, err := OpenReadOnly(path); err == nil {
		t.Fatal("a missing base opened read-only")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("read-only open created %s: %v", path, err)
	}
}

// A base from a newer memcue is refused, not read as if this version understood it.
func TestOpenReadOnlyRefusesANewerSchema(t *testing.T) {
	s, path := open(t)
	if _, err := s.db.Exec(`PRAGMA user_version = 99`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenReadOnly(path); err == nil {
		t.Fatal("a schema-99 base opened read-only")
	}
}

// A version-3 base has no fact_vectors; opening it must create the table, which an older binary may still write.
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
	saveVector(t, s, f.ID)
	if n := countVectors(t, s); n != 1 {
		t.Errorf("vectors after migration = %d, want 1", n)
	}
}

// saveVector stores a vector the way an older memcue did, straight into the table.
func saveVector(t *testing.T, s *Store, id string) {
	t.Helper()
	if _, err := s.db.Exec(`INSERT INTO fact_vectors (id, tag, dim, text_hash, vec) VALUES (?, 'bge', 1024, 'h', ?)`,
		id, make([]byte, 4096)); err != nil {
		t.Fatal(err)
	}
}

func countVectors(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM fact_vectors`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Opening a base that holds the vectors of an older memcue deletes them and gives the space back (#260); the table
// stays for the binaries that still know it.
func TestOpenDeletesStoredVectors(t *testing.T) {
	s, path := open(t)
	for i := range 300 {
		f, err := s.Add(fmt.Sprintf("Факт %d про порт %d.", i, 3000+i), nil, "x 2026-10-22")
		if err != nil {
			t.Fatal(err)
		}
		saveVector(t, s, f.ID)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := countVectors(t, s); n != 0 {
		t.Errorf("vectors after open = %d, want none", n)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// 300 vectors of 4 KiB are about 1.2 MB; the facts themselves are far smaller.
	if after.Size() >= before.Size()/2 {
		t.Errorf("base is %d bytes after open, %d before: the space was not given back", after.Size(), before.Size())
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

// AddBatch calls from several goroutines share one ULID entropy source; the locked reader keeps the ids unique and
// the race detector quiet.
func TestAddBatchConcurrentIDsAreUnique(t *testing.T) {
	s, _ := open(t)
	const writers = 16
	ids := make(chan string, writers)
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			facts, err := s.AddBatch([]FactInput{{Content: fmt.Sprintf("Concurrent fact %d.", i), Origin: "x 2026-10-20"}})
			if err != nil {
				t.Errorf("AddBatch %d: %v", i, err)
				return
			}
			ids <- facts[0].ID
		}(i)
	}
	wg.Wait()
	close(ids)
	seen := map[string]bool{}
	for id := range ids {
		if seen[id] {
			t.Fatalf("duplicate id %s", id)
		}
		seen[id] = true
	}
	if len(seen) != writers {
		t.Fatalf("ids = %d, want %d", len(seen), writers)
	}
}

// A card carries the rules of the session's repository next to its decisions, and no rule or decision of another
// repository.
func TestCardFactsCarryRepositoryRules(t *testing.T) {
	s, _ := open(t)
	add := func(content, kind, scope string) Fact {
		t.Helper()
		f, err := s.AddWith(content, nil, "test 2026-10-20", "", Meta{Kind: kind, Scope: scope})
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	own := add("Shop rule.", "rule", "repo:shop")
	other := add("Lists rule.", "rule", "repo:lists")
	pitfall := add("Shop pitfall.", "pitfall", "repo:shop")
	got, err := s.CardFacts("shop", "2026-10-20")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != own.ID {
		t.Errorf("card facts %+v, want only %s (not %s or %s)", got, own.ID, other.ID, pitfall.ID)
	}
}

// Relabel writes the labels as a new version that keeps the content and the creation time, and refuses a fact that
// changed since the caller read it.
func TestRelabelKeepsContentAndDate(t *testing.T) {
	s, _ := open(t)
	f, err := s.Add("Gradle needs JAVA_HOME.", []string{"gradle"}, "lists 2026-10-09 (lists/feedback_gradle.md)")
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Relabel(f.ID, f.Version, "rule", "repo:lists", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != f.Version+1 || got.Kind != "rule" || got.Scope != "repo:lists" || got.Content != f.Content ||
		!got.Created.Equal(f.Created) || got.Origin != f.Origin {
		t.Errorf("relabelled %+v from %+v", got, f)
	}
	if _, err := s.Relabel(f.ID, f.Version, "pitfall", "global", ""); !errors.Is(err, ErrChanged) {
		t.Errorf("a stale version: %v, want ErrChanged", err)
	}
	if _, err := s.Relabel(f.ID, got.Version, "rule", "global", "tomorrow"); err == nil {
		t.Error("a bad expiry was accepted")
	}
	r, err := s.Retire(f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Relabel(f.ID, r.Version, "rule", "global", ""); !errors.Is(err, ErrRetired) {
		t.Errorf("a retired fact: %v, want ErrRetired", err)
	}
}

// Restore undoes a retire: an approved fact is found again with its content and labels, a rejected candidate goes back
// to review, and a fact that is not retired is refused.
func TestRestoreUndoesARetire(t *testing.T) {
	s, _ := open(t)
	f, err := s.AddWith("Порт gitea на loffe 3000.", []string{"gitea"}, "test 2026-10-22", "",
		Meta{Kind: "environment", Scope: "host:loffe", Source: "tool"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Restore(f.ID); !errors.Is(err, ErrNotRetired) {
		t.Errorf("restoring an active fact: %v", err)
	}
	if _, err := s.Retire(f.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.Restore(f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 3 || got.Status != Active || got.Review != "" || got.Content != f.Content || got.Meta != f.Meta {
		t.Errorf("restored %+v from %+v", got, f)
	}
	if _, ok := ids(t, s, "порт gitea")[f.ID]; !ok {
		t.Error("a restored fact is not found by search")
	}

	c, _ := s.AddWith("Кандидат про порт forgejo 3000.", []string{"forgejo"}, "", "", Meta{Review: Candidate})
	if _, err := s.RetireCandidate(c.ID); err != nil {
		t.Fatal(err)
	}
	got, err = s.Restore(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != Active || got.Review != Candidate {
		t.Errorf("a rejected candidate came back as %+v, want a candidate", got)
	}
}

// An approved fact splits into approved parts that recall finds; a provisional one into provisional parts.
func TestSplitFact(t *testing.T) {
	s, _ := open(t)
	meta := Meta{Kind: "environment", Scope: "host:loffe", Source: "tool"}
	f, err := s.AddWith("На loffe стоит memcue 0.4.0; бинарь лежит в /opt/data/bin.", []string{"memcue"},
		"hermes 2026-10-22", "", meta)
	if err != nil {
		t.Fatal(err)
	}
	parts, err := s.SplitFact(f.ID, f.Version, []string{"На loffe стоит memcue 0.4.0.", "Бинарь memcue на loffe лежит в /opt/data/bin."})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(f.ID); got.Status != Retired {
		t.Errorf("original %+v", got)
	}
	for _, p := range parts {
		if got, _ := s.Get(p.ID); got.Status != Active || got.Review != "" || got.Meta != meta || got.Origin != f.Origin {
			t.Errorf("part %+v", got)
		}
	}
	hits, err := s.Search([]string{"opt"}, 10)
	if err != nil || len(hits) != 1 || hits[0].ID != parts[1].ID {
		t.Errorf("search for a part %+v %v", hits, err)
	}
	prov, _ := s.AddWith("Временный факт про порт 3000 и путь /srv.", nil, "", "2026-11-01", Meta{Review: Provisional})
	pparts, err := s.SplitFact(prov.ID, prov.Version, []string{"Порт 3000.", "Путь /srv."})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(pparts[0].ID); got.Review != Provisional || got.Expires != "2026-11-01" {
		t.Errorf("provisional part %+v", got)
	}
}

// SplitFact refuses a fact that changed since it was read, a retired fact and a candidate, and writes nothing then.
func TestSplitFactRefuses(t *testing.T) {
	s, _ := open(t)
	parts := []string{"Порт 3000.", "Путь /srv."}
	f, _ := s.Add("Порт 3000 и путь /srv.", nil, "")
	if _, err := s.Correct(f.ID, "Порт 3000 и путь /srv/app.", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SplitFact(f.ID, 1, parts); !errors.Is(err, ErrChanged) {
		t.Errorf("a changed fact: %v", err)
	}
	gone, _ := s.Add("Снятый факт про порт 3000 и путь /srv.", nil, "")
	if _, err := s.Retire(gone.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SplitFact(gone.ID, 2, parts); !errors.Is(err, ErrRetired) {
		t.Errorf("a retired fact: %v", err)
	}
	cand, _ := s.AddWith("Кандидат про порт 3000 и путь /srv.", nil, "", "", Meta{Review: Candidate})
	if _, err := s.SplitFact(cand.ID, 1, parts); !errors.Is(err, ErrAlreadyCandidate) {
		t.Errorf("a candidate: %v", err)
	}
	rows, err := s.Rows()
	if err != nil || len(rows) != 5 {
		t.Errorf("a refused split wrote to the base: %d rows %v", len(rows), err)
	}
}

// The queries of a fact are searched like its words, and SetQueries writes them as a new version that keeps the rest.
func TestQueriesAreSearchedAndSet(t *testing.T) {
	s, _ := open(t)
	f, err := s.Add("Caddy-private монтирует файл конфига; после замены пересоздать контейнер.", []string{"caddy"}, "x")
	if err != nil {
		t.Fatal(err)
	}
	if hits, _ := s.Search([]string{"прокси"}, 10); len(hits) != 0 {
		t.Fatalf("found before the queries: %+v", hits)
	}
	got, err := s.SetQueries(f.ID, f.Version, []string{"обратный прокси", "reverse proxy"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 2 || got.Content != f.Content || len(got.Queries) != 2 || got.Keywords[0] != "caddy" {
		t.Errorf("version %+v", got)
	}
	hits, err := s.Search([]string{"прокси"}, 10)
	if err != nil || len(hits) != 1 || hits[0].ID != f.ID || len(hits[0].Queries) != 2 {
		t.Errorf("search by a query word: %+v %v", hits, err)
	}
	if _, err := s.SetQueries(f.ID, 1, []string{"x"}); !errors.Is(err, ErrChanged) {
		t.Errorf("a stale version: %v", err)
	}
	if _, err := s.Retire(f.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetQueries(f.ID, 3, []string{"x"}); !errors.Is(err, ErrRetired) {
		t.Errorf("a retired fact: %v", err)
	}
}

// A correction of the text drops the queries written for the old text; a correction of the keywords alone keeps them.
func TestCorrectDropsQueriesOfAnOldText(t *testing.T) {
	s, _ := open(t)
	facts, err := s.AddBatch([]FactInput{{Content: "Порт gitea 3000.", Queries: []string{"git сервер"}}})
	if err != nil {
		t.Fatal(err)
	}
	f := facts[0]
	same, err := s.Correct(f.ID, f.Content, []string{"gitea"})
	if err != nil || len(same.Queries) != 1 {
		t.Errorf("keywords only: %+v %v", same, err)
	}
	moved, err := s.Correct(f.ID, "Порт gitea 3001.", nil)
	if err != nil || len(moved.Queries) != 0 {
		t.Errorf("new text: %+v %v", moved, err)
	}
}

// A textconv of a base written before the queries column existed reads it with no queries instead of failing.
func TestOpenReadOnlyReadsABaseWithoutQueries(t *testing.T) {
	s, path := open(t)
	f, err := s.Add("Gitea слушает порт 3000.", []string{"gitea"}, "demo 2026-10-20")
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`ALTER TABLE facts DROP COLUMN queries`, `PRAGMA user_version = 4`} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	ro, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ro.Close() }()
	var b bytes.Buffer
	if err := ro.Dump(&b); err != nil || !strings.Contains(b.String(), f.ID) {
		t.Fatalf("dump of a version-4 base: %q %v", b.String(), err)
	}
	if got, err := ro.Get(f.ID); err != nil || got.Content != f.Content || len(got.Queries) != 0 {
		t.Errorf("get %+v %v", got, err)
	}
}

// A version-4 base whose index lost the stems of the queries, as the rebuild of an older memcue leaves it, is
// reindexed once when a current memcue opens it.
func TestOpeningAVersion4BaseIndexesTheQueries(t *testing.T) {
	s, path := open(t)
	facts, err := s.AddBatch([]FactInput{{Content: "Caddy-private монтирует файл конфига.", Queries: []string{"обратный прокси"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`UPDATE fact_index SET stems = 'caddy-privat монтир файл конфиг'`,
		`INSERT INTO fact_fts(fact_fts) VALUES('rebuild')`,
		`PRAGMA user_version = 4`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if hits, _ := s.Search([]string{"прокс"}, 10); len(hits) != 0 {
		t.Fatalf("setup: the index still holds the queries: %+v", hits)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s2.Close() }()
	hits, err := s2.Search([]string{"прокс"}, 10)
	if err != nil || len(hits) != 1 || hits[0].ID != facts[0].ID {
		t.Errorf("search by a query word after the migration: %+v %v", hits, err)
	}
}

// A rule the user stated comes before newer ones the model wrote: on a migrated base most rules share one date.
func TestCardFactsPutStatedRulesFirst(t *testing.T) {
	s, _ := open(t)
	add := func(content, source string) Fact {
		t.Helper()
		f, err := s.AddWith(content, nil, "test 2026-10-20", "", Meta{Kind: "rule", Scope: "global", Source: source})
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	stated := add("Stated rule.", "user")
	confirmed := add("Confirmed rule.", "user_confirmed")
	written := add("Written rule.", "tool")
	got, err := s.CardFacts("", "2026-10-20")
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, f := range got {
		order = append(order, f.ID)
	}
	if want := []string{confirmed.ID, stated.ID, written.ID}; !slices.Equal(order, want) {
		t.Errorf("card order %v, want the stated rules newest first, then %s", order, written.ID)
	}
}
