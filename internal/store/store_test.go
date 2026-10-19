package store

import (
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
