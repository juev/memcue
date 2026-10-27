package recall

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/juev/memcue/internal/store"
)

var facts = []struct{ content, keywords, origin string }{
	{"Gradle вне make требует JAVA_HOME=/opt/homebrew/opt/openjdk@21, иначе сборка не находит JDK.", "gradle, jdk, java", "lists 2026-10-09"},
	{"Issue #20 в hledger-lsp: Neovim format-on-type не двигает курсор после TextEdit.", "neovim, format", "hledger-lsp 2026-09-01"},
	{"Публичный Caddy монтирует каталог configs/caddy; перезагрузка конфига только с путём /etc/caddy/configs/Caddyfile.", "caddy reload", "server-migration 2026-09-24"},
	{"Эксперимент byte-vs-BPE на vast.ai: арендовать RTX 4090, запускать обучение на сутки.", "vast, gpu", "llm 2026-08-01"},
	{"Прокси mihomo на 127.0.0.1:1080 для тестового стенда, NO_PROXY содержит staging.example.com.", "прокси, proxy", "loadtest 2026-09-10"},
	{"Прокси mihomo на 127.0.0.1:1080 смешанный: HTTP и SOCKS на одном порту.", "прокси, proxy", "_global 2026-08-01"},
}

func setup(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "facts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	for _, f := range facts {
		if _, err := s.AddWith(f.content, splitKW(f.keywords), f.origin, "", store.Meta{}); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

// A provisional fact is available only after the approved facts, and an expired one is not recalled at all.
func TestProvisionalComesAfterApprovedAndExpires(t *testing.T) {
	path := filepath.Join(t.TempDir(), "facts.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	approved, err := s.AddWith("Caddy config.", nil, "x", "", store.Meta{})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	// Nothing writes a provisional fact any more (#494, #514), but a live base still holds such rows from the vet; the
	// next open indexes them.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	const expiredID = "01JRECALLPROVISIONAL000002"
	for _, r := range [][3]string{
		{"01JRECALLPROVISIONAL000001", "Caddy reload path for the public config.", time.Now().AddDate(0, 0, 7).Format(time.DateOnly)},
		{expiredID, "Caddy reload path from an old note.", time.Now().AddDate(0, 0, -1).Format(time.DateOnly)},
	} {
		if _, err := db.Exec(`INSERT INTO facts(id, version, content, origin, created, status, expires, review)
			VALUES (?, 1, ?, 'x', '2026-10-20T12:00:00Z', 'active', ?, 'provisional')`, r[0], r[1], r[2]); err != nil {
			t.Fatal(err)
		}
	}
	_ = db.Close()
	if s, err = store.Open(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	p := Params{Candidates: 10, MinTerms: 1, MinMatched: 1, MinCoverage: 0.1, MaxFacts: 3}
	hits, err := Recall(s, "caddy reload path", p)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) < 2 || hits[0].ID != approved.ID || !hits[1].Provisional {
		t.Fatalf("hits = %+v, want approved first and provisional second", hits)
	}
	for _, h := range hits {
		if h.ID == expiredID {
			t.Fatalf("expired provisional fact was recalled: %+v", h)
		}
	}
}

func splitKW(s string) []string {
	if s == "" {
		return nil
	}
	return []string{s}
}

func recall(t *testing.T, s *store.Store, prompt string) []Hit {
	t.Helper()
	hs, err := Recall(s, prompt, testParams)
	if err != nil {
		t.Fatal(err)
	}
	return hs
}

func TestConfirmationsRecallNothing(t *testing.T) {
	s := setup(t)
	for _, p := range []string{"да, запускай эксперимент", "давай закрывать", "ок", "продолжаем"} {
		if got := recall(t, s, p); len(got) != 0 {
			t.Errorf("%q recalled %v", p, got)
		}
	}
}

// The facts that pass the gates are served in BM25 order: the coverage share gates, it does not rank (#338). A short
// fact on the two rarest prompt terms comes before a long one that holds every term with a larger share.
func TestPassingFactsAreServedInBM25Order(t *testing.T) {
	s := setup(t)
	short, err := s.AddWith("Конфиг kafka без zookeeper.", []string{"kafka", "zookeeper"}, "infra 2026-10-01", "", store.Meta{})
	if err != nil {
		t.Fatal(err)
	}
	filler := strings.Repeat("брокеры топики ретеншн мониторинг алерты дашборды бэкапы ротация ", 8)
	long, err := s.AddWith("Кластер kafka в staging: "+filler+"zookeeper больше не нужен, конфиг лежит в репозитории.",
		[]string{"kafka"}, "infra 2026-10-02", "", store.Meta{})
	if err != nil {
		t.Fatal(err)
	}
	p := testParams
	p.MinMatched, p.MinCoverage = 2, 0
	hs, err := Recall(s, "конфиг кластера kafka zookeeper", p)
	if err != nil || len(hs) != 2 {
		t.Fatalf("got %+v %v, want two facts", hs, err)
	}
	byID := map[string]Hit{hs[0].ID: hs[0], hs[1].ID: hs[1]}
	if !(byID[short.ID].Coverage < byID[long.ID].Coverage && byID[short.ID].bm25 < byID[long.ID].bm25) {
		t.Fatalf("setup: want the short fact below on coverage and above on BM25, got %+v", hs)
	}
	if hs[0].ID != short.ID {
		t.Errorf("first is %q, want the BM25 leader %q", hs[0].Content, short.Content)
	}
}

func TestAtMostMaxFacts(t *testing.T) {
	s := setup(t)
	p := testParams
	p.MaxFacts, p.MinMatched, p.MinCoverage = 1, 1, 0
	hs, err := Recall(s, "прокси mihomo caddy gradle", p)
	if err != nil || len(hs) != 1 {
		t.Errorf("got %d %v", len(hs), err)
	}
}

// A non-positive MaxFacts means no cap, the rule Recall follows: it must serve every
// fact instead of none and must never slice the hits with a negative index. A positive value still truncates.
func TestNonPositiveMaxFactsIsNoCap(t *testing.T) {
	s := setup(t)
	p := testParams
	p.MinMatched, p.MinCoverage = 1, 0
	const prompt = "прокси mihomo caddy gradle"
	p.MaxFacts = 100 // a cap above any candidate list: "no cap" as well
	all, err := Recall(s, prompt, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) < 3 {
		t.Fatalf("the test needs several facts to tell a cap from no cap, got %v", ids(all))
	}
	for _, maxFacts := range []int{0, -1} {
		p.MaxFacts = maxFacts
		got, err := Recall(s, prompt, p)
		if err != nil {
			t.Fatalf("MaxFacts %d: %v", maxFacts, err)
		}
		if len(got) != len(all) {
			t.Fatalf("MaxFacts %d served %v, want all %v", maxFacts, ids(got), ids(all))
		}
		for i := range all {
			if got[i].ID != all[i].ID {
				t.Fatalf("MaxFacts %d served %v, want %v", maxFacts, ids(got), ids(all))
			}
		}
	}
	p.MaxFacts = 1
	got, err := Recall(s, prompt, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != all[0].ID {
		t.Fatalf("MaxFacts 1 served %v, want the first of %v", ids(got), ids(all))
	}
}

// A two-word prompt asks about two terms: the dev-set minimum of three matched terms silenced every fact about them.
func TestShortPromptWithKnownTermsRecalls(t *testing.T) {
	s := setup(t)
	hs := recall(t, s, "прокси mihomo")
	if len(hs) == 0 {
		t.Fatalf(`"прокси mihomo" recalled nothing`)
	}
}

// A ticket key is an exact anchor, not a project word: PROJ-1234 recalls for PROJ-1234 and never for PROJ-1235.
func TestTicketKeyRecallsExactOnly(t *testing.T) {
	s := setup(t)
	a, err := s.AddWith("Тикет PROJ-1234: берём в работу.", []string{"PROJ"}, "x 2026-10-20", "",
		store.Meta{Kind: "decision", Scope: "repo:x"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.AddWith("Тикет PROJ-1235: отложен.", []string{"PROJ"}, "x 2026-10-20", "",
		store.Meta{Kind: "decision", Scope: "repo:x"})
	if err != nil {
		t.Fatal(err)
	}
	for prompt, want := range map[string]string{"давай брать PROJ-1234": a.ID, "PROJ-1235": b.ID} {
		got := recall(t, s, prompt)
		if len(got) == 0 || got[0].ID != want {
			t.Errorf("%q recalled %+v, want %s", prompt, got, want)
		}
	}
	if got := recall(t, s, "давай брать PROJ-1236"); len(got) != 0 {
		t.Errorf("PROJ-1236 recalled %+v", got)
	}
	// A prompt naming the wrong ticket must not reach the other one through the shared keyword "PROJ".
	if got := recall(t, s, "PROJ-1235 postgres port"); len(got) != 1 || got[0].ID != b.ID {
		t.Errorf("wrong ticket plus postgres recalled %+v", got)
	}
}

// A versioned token in a prompt is not a ticket: it must not enable the exact-term filter and drop a fact that shares
// the ordinary words. The fact spells the version without a hyphen, as a person would in a note.
func TestVersionedPromptIsNotATicket(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "facts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	f, err := s.AddWith("Postgres 16 runs in a container on port 55432.", nil, "demo 2026-10-19", "", store.Meta{})
	if err != nil {
		t.Fatal(err)
	}
	hs, err := Recall(s, "Which port does postgres-16 use?", testParams)
	if err != nil {
		t.Fatal(err)
	}
	if len(hs) == 0 || hs[0].ID != f.ID {
		t.Errorf("versioned prompt recalled %+v, want %s", hs, f.ID)
	}
}

func TestExpiredFactIsNotRecalled(t *testing.T) {
	s := setup(t)
	f, err := s.AddWith("Стенд нагрузки поднимается на порту 8443 в staging.", []string{"staging"},
		"loadtest 2026-10-01", "2026-10-10", store.Meta{})
	if err != nil {
		t.Fatal(err)
	}
	p := testParams
	p.Today = "2026-10-19"
	hs, err := Recall(s, "на каком порту поднимается стенд нагрузки в staging", p)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hs {
		if h.ID == f.ID {
			t.Error("expired fact recalled")
		}
	}
	p.Today = "2026-10-05" // still inside its last day
	hs, err = Recall(s, "на каком порту поднимается стенд нагрузки в staging", p)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, h := range hs {
		if h.ID == f.ID {
			found = true
		}
	}
	if !found {
		t.Error("fact not recalled before its expiry")
	}
}

// A fact written before the monotonic ids can have an older id and a newer created time: "newer" must use created
// first, and only fall back to the id when the timestamps are equal.
func TestNewerOrdersByCreatedThenID(t *testing.T) {
	old := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	recent := old.Add(time.Hour)
	byCreated := store.Fact{ID: "01AAA", Created: recent}
	byID := store.Fact{ID: "01ZZZ", Created: old}
	if !newer(byCreated, byID) || newer(byID, byCreated) {
		t.Fatal("newer did not prefer the later created time")
	}
	same := store.Fact{ID: "01ZZZ", Created: recent}
	if !newer(same, byCreated) {
		t.Fatal("equal created times must fall back to the larger id")
	}
}

// A text that holds nearly all of a fact's own terms finds it under MinContained even when the fact holds too small a
// share of the text's terms for MinCoverage; a fact with details of its own does not pass that way.
func TestMinContainedFindsAFactTheTextRestates(t *testing.T) {
	s := setup(t)
	// The other words are known to the base, so they weigh in the share of the text's terms a fact holds.
	prompt := "Прокси mihomo на 127.0.0.1:1080 смешанный: HTTP и SOCKS на одном порту, proxy; через него идёт " +
		"сборка Gradle с JDK, перезагрузка конфига Caddy и обучение на vast.ai."
	p := Params{Candidates: 80, MinTerms: 2, MinMatched: 3, MinCoverage: 0.6, MaxPromptTerms: 24, MaxFacts: 5}
	hits, err := Recall(s, prompt, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("coverage alone found %v: the prompt no longer shows the case", ids(hits))
	}
	p.MinContained = 0.8
	hits, err = Recall(s, prompt, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || !strings.Contains(hits[0].Content, "SOCKS") {
		t.Fatalf("hits %v, want the mixed-proxy fact alone", ids(hits))
	}
}
