package recall

import (
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
		if _, err := s.Add(f.content, splitKW(f.keywords), f.origin); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

// A provisional fact is available only after the approved facts, and an expired one is not recalled at all.
func TestProvisionalComesAfterApprovedAndExpires(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "facts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	approved, err := s.Add("Caddy config.", nil, "x")
	if err != nil {
		t.Fatal(err)
	}
	cand, err := s.AddWith("Caddy reload path for the public config.", nil, "x", "",
		store.Meta{Review: store.Candidate})
	if err != nil {
		t.Fatal(err)
	}
	until := time.Now().AddDate(0, 0, 7).Format(time.DateOnly)
	if _, err := s.ApproveProvisional(cand.ID, until); err != nil {
		t.Fatal(err)
	}
	expired, err := s.AddWith("Caddy reload path from an old note.", nil, "x", "",
		store.Meta{Review: store.Candidate})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApproveProvisional(expired.ID, time.Now().AddDate(0, 0, -1).Format(time.DateOnly)); err != nil {
		t.Fatal(err)
	}
	p := Params{Candidates: 10, MinTerms: 1, MinMatched: 1, MinCoverage: 0.1, MaxFacts: 3}
	hits, err := Recall(s, "caddy reload path", nil, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) < 2 || hits[0].ID != approved.ID || !hits[1].Provisional {
		t.Fatalf("hits = %+v, want approved first and provisional second", hits)
	}
	for _, h := range hits {
		if h.ID == expired.ID {
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

// project is the key set of a session in the repository tier, none outside one.
func project(tier string) []string {
	if tier == "" {
		return nil
	}
	return []string{tier}
}

func recall(t *testing.T, s *store.Store, prompt, tier string) []Hit {
	t.Helper()
	hs, err := Recall(s, prompt, project(tier), Default)
	if err != nil {
		t.Fatal(err)
	}
	return hs
}

func TestConfirmationsRecallNothing(t *testing.T) {
	s := setup(t)
	for _, p := range []string{"да, запускай эксперимент", "давай закрывать", "ок", "продолжаем"} {
		if got := recall(t, s, p, "server-migration"); len(got) != 0 {
			t.Errorf("%q recalled %v", p, got)
		}
	}
}

func TestFactFromAnotherRepositoryIsRecalled(t *testing.T) {
	s := setup(t)
	got := recall(t, s, "Хочу запустить gradle руками без make, какой JAVA_HOME выставить для JDK?", "perigee")
	if len(got) == 0 || got[0].Origin != "lists 2026-10-09" {
		t.Errorf("got %+v", got)
	}
}

// The same text learnt in two places ties on BM25: the session's repository wins, and outside one the global fact.
func TestOwnRepositoryWinsTies(t *testing.T) {
	s := setup(t)
	for _, origin := range []string{"loadtest 2026-09-10", "_global 2026-08-01"} {
		if _, err := s.Add("Брокер rabbitmq слушает 5672, панель управления на 15672.", []string{"rabbitmq"},
			origin); err != nil {
			t.Fatal(err)
		}
	}
	p := Default
	p.MinMatched = 2
	for tier, want := range map[string]string{"loadtest": "loadtest 2026-09-10", "": "_global 2026-08-01"} {
		got, err := Recall(s, "брокер rabbitmq", project(tier), p)
		if err != nil || len(got) != 2 || got[0].bm25 != got[1].bm25 {
			t.Fatalf("want a tie of two, got %+v %v", got, err)
		}
		if got[0].Origin != want {
			t.Errorf("tier %q: first is %q, want %q", tier, got[0].Origin, want)
		}
	}
}

// The facts that pass the gates are served in BM25 order: the coverage share gates, it does not rank (#338). A short
// fact on the two rarest prompt terms comes before a long one that holds every term with a larger share.
func TestPassingFactsAreServedInBM25Order(t *testing.T) {
	s := setup(t)
	short, err := s.Add("Конфиг kafka без zookeeper.", []string{"kafka", "zookeeper"}, "infra 2026-10-01")
	if err != nil {
		t.Fatal(err)
	}
	filler := strings.Repeat("брокеры топики ретеншн мониторинг алерты дашборды бэкапы ротация ", 8)
	long, err := s.Add("Кластер kafka в staging: "+filler+"zookeeper больше не нужен, конфиг лежит в репозитории.",
		[]string{"kafka"}, "infra 2026-10-02")
	if err != nil {
		t.Fatal(err)
	}
	p := Default
	p.MinMatched, p.MinCoverage = 2, 0
	hs, err := Recall(s, "конфиг кластера kafka zookeeper", nil, p)
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
	p := Default
	p.MaxFacts, p.MinMatched, p.MinCoverage = 1, 1, 0
	hs, err := Recall(s, "прокси mihomo caddy gradle", nil, p)
	if err != nil || len(hs) != 1 {
		t.Errorf("got %d %v", len(hs), err)
	}
}

// A non-positive MaxFacts means no cap, the rule Recall and RecallJudge follow: it must serve every
// fact instead of none and must never slice the hits with a negative index. A positive value still truncates.
func TestNonPositiveMaxFactsIsNoCap(t *testing.T) {
	s := setup(t)
	p := Default
	p.MinMatched, p.MinCoverage = 1, 0
	const prompt = "прокси mihomo caddy gradle"
	p.MaxFacts = 100 // a cap above any candidate list: "no cap" as well
	all, err := Recall(s, prompt, nil, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) < 3 {
		t.Fatalf("the test needs several facts to tell a cap from no cap, got %v", ids(all))
	}
	for _, maxFacts := range []int{0, -1} {
		p.MaxFacts = maxFacts
		got, err := Recall(s, prompt, nil, p)
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
	got, err := Recall(s, prompt, nil, p)
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
	hs := recall(t, s, "прокси mihomo", "loadtest")
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
		got := recall(t, s, prompt, "x")
		if len(got) == 0 || got[0].ID != want {
			t.Errorf("%q recalled %+v, want %s", prompt, got, want)
		}
	}
	if got := recall(t, s, "давай брать PROJ-1236", "x"); len(got) != 0 {
		t.Errorf("PROJ-1236 recalled %+v", got)
	}
	// A prompt naming the wrong ticket must not reach the other one through the shared keyword "PROJ".
	if got := recall(t, s, "PROJ-1235 postgres port", "x"); len(got) != 1 || got[0].ID != b.ID {
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
	f, err := s.Add("Postgres 16 runs in a container on port 55432.", nil, "demo 2026-10-19")
	if err != nil {
		t.Fatal(err)
	}
	hs, err := Recall(s, "Which port does postgres-16 use?", nil, Default)
	if err != nil {
		t.Fatal(err)
	}
	if len(hs) == 0 || hs[0].ID != f.ID {
		t.Errorf("versioned prompt recalled %+v, want %s", hs, f.ID)
	}
}

// At equal coverage a fact of this host wins the tie, without excluding the other host.
func TestCurrentHostWinsTies(t *testing.T) {
	s := setup(t)
	a, err := s.AddWith("Кэш memcached на этом хосте.", []string{"кэш", "memcached"},
		"loadtest 2026-10-10", "", store.Meta{Kind: "environment", Scope: "host:mac"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddWith("Кэш memcached на этом хосте.", []string{"кэш", "memcached"},
		"loadtest 2026-10-10", "", store.Meta{Kind: "environment", Scope: "host:loffe"}); err != nil {
		t.Fatal(err)
	}
	p := Default
	p.Host = "mac"
	p.MinMatched, p.MinCoverage, p.MaxFacts = 2, 0, 1
	hs, err := Recall(s, "кэш memcached", nil, p)
	if err != nil || len(hs) != 1 || hs[0].ID != a.ID {
		t.Errorf("got %+v %v, want %s", hs, err, a.ID)
	}
}

// A fact of another host that takes a slot while a fact of this host is left out is marked in the hit and the log.
func TestOtherHostDisplacementIsMarked(t *testing.T) {
	s := setup(t)
	if _, err := s.AddWith("Кэш на этом хосте.", []string{"кэш"}, "loadtest 2026-10-10", "",
		store.Meta{Kind: "environment", Scope: "host:mac"}); err != nil {
		t.Fatal(err)
	}
	b, err := s.AddWith("Кэш redis на 127.0.0.1:6379, порт по умолчанию.", []string{"кэш", "redis"},
		"loadtest 2026-10-10", "", store.Meta{Kind: "environment", Scope: "host:loffe"})
	if err != nil {
		t.Fatal(err)
	}
	p := Default
	p.Host = "mac"
	p.MinMatched, p.MinCoverage, p.MaxFacts = 1, 0, 1
	hs, err := Recall(s, "кэш redis", nil, p)
	if err != nil || len(hs) != 1 || hs[0].ID != b.ID || !hs[0].Displaced {
		t.Errorf("got %+v %v, want displaced %s", hs, err, b.ID)
	}
}

func TestExpiredFactIsNotRecalled(t *testing.T) {
	s := setup(t)
	f, err := s.AddExpiring("Стенд нагрузки поднимается на порту 8443 в staging.", []string{"staging"},
		"loadtest 2026-10-01", "2026-10-10")
	if err != nil {
		t.Fatal(err)
	}
	p := Default
	p.Today = "2026-10-19"
	hs, err := Recall(s, "на каком порту поднимается стенд нагрузки в staging", []string{"loadtest"}, p)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hs {
		if h.ID == f.ID {
			t.Error("expired fact recalled")
		}
	}
	p.Today = "2026-10-05" // still inside its last day
	hs, err = Recall(s, "на каком порту поднимается стенд нагрузки в staging", []string{"loadtest"}, p)
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
	byCreated := Hit{Fact: store.Fact{ID: "01AAA", Created: recent}}
	byID := Hit{Fact: store.Fact{ID: "01ZZZ", Created: old}}
	if !newer(byCreated, byID) || newer(byID, byCreated) {
		t.Fatal("newer did not prefer the later created time")
	}
	same := Hit{Fact: store.Fact{ID: "01ZZZ", Created: recent}}
	if !newer(same, byCreated) {
		t.Fatal("equal created times must fall back to the larger id")
	}
}

// A fact learnt under any key of the session's project wins a tie: a fork's session prefers a fact of its upstream
// over the same text learnt elsewhere.
func TestAnyProjectKeyWinsTies(t *testing.T) {
	s := setup(t)
	for _, origin := range []string{"example.com/elsewhere/z 2026-09-10", "example.com/them/upstream 2026-08-01"} {
		if _, err := s.Add("Брокер nats слушает 4222, мониторинг на 8222.", []string{"nats"}, origin); err != nil {
			t.Fatal(err)
		}
	}
	p := Default
	p.MinMatched = 2
	got, err := Recall(s, "брокер nats", []string{"example.com/me/fork", "example.com/them/upstream"}, p)
	if err != nil || len(got) != 2 || got[0].bm25 != got[1].bm25 {
		t.Fatalf("want a tie of two, got %+v %v", got, err)
	}
	if got[0].Origin != "example.com/them/upstream 2026-08-01" {
		t.Errorf("first is %q, want the upstream's fact", got[0].Origin)
	}
}

// foreignSetup stores the same text under five scopes, so that every fact holds the same share of a prompt, and one
// more fact that makes "логи" a known term the five do not hold.
func foreignSetup(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "facts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	for _, sc := range []string{"repo:example.com/me/app", "repo:example.com/them/lib", "dir:/srv/work", "global",
		"service:queue"} {
		if _, err := s.AddWith("Брокер rabbitmq слушает 5672, панель на 15672 ("+sc+").", []string{"rabbitmq"},
			"x 2026-10-01", "", store.Meta{Scope: sc}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Add("Логи nginx лежат в /var/log/nginx.", nil, "x 2026-10-01"); err != nil {
		t.Fatal(err)
	}
	return s
}

func scopesOf(hits []Hit) map[string]bool {
	out := map[string]bool{}
	for _, h := range hits {
		out[h.Scope] = true
	}
	return out
}

// In a repository's session a fact of another repository or directory passes only with ForeignCoverage; its own
// repository's fact and the places that hold anywhere pass as before (#345).
func TestForeignFactNeedsItsCoverage(t *testing.T) {
	s := foreignSetup(t)
	p := Params{Candidates: 10, MinTerms: 1, MinMatched: 2, MinCoverage: 0.1, MaxFacts: 10}
	prompt := "брокер rabbitmq панель логи" // the facts hold three of the four terms
	got, err := Recall(s, prompt, []string{"example.com/me/app"}, p)
	if err != nil || len(got) != 5 {
		t.Fatalf("no ForeignCoverage: want all five, got %v %v", scopesOf(got), err)
	}
	p.ForeignCoverage = 0.99
	tr := &Trace{}
	p.Trace = tr
	got, err = Recall(s, prompt, []string{"example.com/me/app"}, p)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"repo:example.com/me/app": true, "global": true, "service:queue": true}
	if sc := scopesOf(got); len(sc) != len(want) || !sc["repo:example.com/me/app"] || !sc["global"] || !sc["service:queue"] {
		t.Errorf("served %v, want %v", sc, want)
	}
	foreign := 0
	for _, st := range tr.Lexical {
		if st.Verdict == VerdictForeign {
			foreign++
		}
	}
	if foreign != 2 {
		t.Errorf("trace has %d facts turned away as another project's, want 2: %+v", foreign, tr.Lexical)
	}
}

// FilterForeign leaves the facts of another project out of the search itself: they never reach the gates.
func TestFilterForeignLeavesThemOutOfTheSearch(t *testing.T) {
	s := foreignSetup(t)
	tr := &Trace{}
	p := Params{Candidates: 10, MinTerms: 1, MinMatched: 2, MinCoverage: 0.1, MaxFacts: 10, FilterForeign: true,
		Trace: tr}
	got, err := Recall(s, "брокер rabbitmq панель", []string{"example.com/me/app"}, p)
	if err != nil {
		t.Fatal(err)
	}
	if sc := scopesOf(got); len(sc) != 3 || sc["repo:example.com/them/lib"] || sc["dir:/srv/work"] {
		t.Errorf("served %v, want the own repository, global and the service", sc)
	}
	if len(tr.Lexical) != 3 {
		t.Errorf("the search returned %d facts, want 3", len(tr.Lexical))
	}
}

// A session with no repository key has no other project: the Claude config directory, a directory outside git and
// a session with no key serve every place as before.
func TestSessionWithoutRepositoryHasNoForeignFacts(t *testing.T) {
	s := foreignSetup(t)
	p := Params{Candidates: 10, MinTerms: 1, MinMatched: 2, MinCoverage: 0.1, MaxFacts: 10, ForeignCoverage: 0.99,
		FilterForeign: true}
	for _, project := range [][]string{nil, {"_global"}, {"dir:/opt/data"}} {
		got, err := Recall(s, "брокер rabbitmq панель логи", project, p)
		if err != nil || len(got) != 5 {
			t.Errorf("project %v: want all five, got %v %v", project, scopesOf(got), err)
		}
	}
}
