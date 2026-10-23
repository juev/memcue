package recall

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/juev/memcue/internal/judge"
	"github.com/juev/memcue/internal/store"
)

// meaningBase fills a base with unrelated facts: the target one and nine others.
func meaningBase(t *testing.T) (*store.Store, string) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "facts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	target, err := s.Add("Порог min_coverage поднят до 0.25: шумовых фактов стало вдвое меньше при том же числе полезных.",
		[]string{"coverage"}, "memcue 2026-10-20")
	if err != nil {
		t.Fatal(err)
	}
	others := []string{
		"Публичный Caddy монтирует каталог configs/caddy и перечитывает конфиг только с полным путём.",
		"Gradle вне make требует JAVA_HOME, иначе сборка не находит нужный JDK.",
		"Issue #20 в hledger-lsp: Neovim format-on-type не двигает курсор после TextEdit.",
		"Эксперимент byte-vs-BPE на vast.ai: арендовать RTX 4090 и запускать обучение на сутки.",
		"Прокси mihomo на порту 1080 смешанный: HTTP и SOCKS на одном порту.",
		"Чекаут /opt/loffe не оставлять грязным после мержа — вернуть на ветку main.",
		"Крон microapp-nightly ходит каждые четыре часа и доставляет в свой топик.",
		"SSH-ключ hermes работает и на GitHub, и на Forgejo через порт 443.",
		"Генерация изображений идёт по подписке и складывает файлы в кеш.",
	}
	for _, content := range others {
		if _, err := s.Add(content, nil, "test 2026-10-20"); err != nil {
			t.Fatal(err)
		}
	}
	return s, target.ID
}

func TestRecallJudgeWithoutARankerStaysLexical(t *testing.T) {
	s, _ := meaningBase(t)
	prompt := "Gradle требует JAVA_HOME, иначе сборка не находит JDK"
	plain, err := Recall(s, prompt, "", Default)
	if err != nil {
		t.Fatal(err)
	}
	hits, err := RecallJudge(s, prompt, "", Default, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != len(plain) || (len(hits) > 0 && hits[0].ID != plain[0].ID) {
		t.Fatalf("hits = %v, lexical = %v", ids(hits), ids(plain))
	}
}

func TestRecallJudgeFallsBackWhenTheJudgeFails(t *testing.T) {
	s, _ := meaningBase(t)
	// Two facts hold words of the prompt, so the judge is asked, and its failure is what sends recall to the words.
	prompt := "Gradle требует JAVA_HOME, иначе сборка не находит JDK, а Caddy конфиг"
	plain, err := Recall(s, prompt, "", Default)
	if err != nil {
		t.Fatal(err)
	}
	r := &fakeRanker{min: 0.5, err: errors.New("down")}
	hits, err := RecallJudge(s, prompt, "", Default, r)
	if err != nil {
		t.Fatal(err)
	}
	if r.asked < 2 {
		t.Fatalf("the judge was asked about %d facts, want both", r.asked)
	}
	if len(hits) != len(plain) || (len(hits) > 0 && hits[0].ID != plain[0].ID) {
		t.Fatalf("hits = %v, lexical = %v", ids(hits), ids(plain))
	}
}

func ids(hits []Hit) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.ID
	}
	return out
}

// Enabling the judge must not drop the host tie-break: two facts with the same score are ordered by place, and the
// displacement flag names the host fact left out behind another host's fact.
func TestRecallJudgeKeepsTheHostTieBreak(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "facts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	const text = "Порог покрытия поднят до 0.25 для поиска фактов."
	other, err := s.AddWith(text, nil, "demo 2026-10-20", "", store.Meta{Scope: "host:other"})
	if err != nil {
		t.Fatal(err)
	}
	current, err := s.AddWith(text, nil, "demo 2026-10-21", "", store.Meta{Scope: "host:box"})
	if err != nil {
		t.Fatal(err)
	}
	r := &fakeRanker{min: 0.5, scores: map[string]float64{"Порог": 0.9}}
	p := rankedParams()
	p.MaxFacts = 1

	p.Host = "box"
	hits, err := RecallJudge(s, "порог покрытия поиск фактов", "", p, r)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].ID != current.ID {
		t.Fatalf("hits = %v, want the fact of this host %s", ids(hits), current.ID)
	}
	if hits[0].Displaced {
		t.Error("a served fact of this host is marked displaced")
	}

	p.Host = "other"
	hits, err = RecallJudge(s, "порог покрытия поиск фактов", "", p, r)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].ID != other.ID {
		t.Fatalf("hits = %v, want the fact of this host %s", ids(hits), other.ID)
	}
	if hits[0].Displaced {
		t.Error("a served fact of this host is marked displaced")
	}
}

// A foreign fact with a higher score wins on rank, not on place; when it takes the last slot while a fact of this
// host is left out, the log must say so.
func TestRecallJudgeMarksDisplacement(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "facts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	foreign, err := s.AddWith("Порог покрытия поднят до 0.95 для поиска фактов.", nil, "demo 2026-10-20", "",
		store.Meta{Scope: "host:other"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddWith("Порог покрытия поднят до 0.25 для поиска фактов.", nil, "demo 2026-10-21", "",
		store.Meta{Scope: "host:box"}); err != nil {
		t.Fatal(err)
	}
	r := &fakeRanker{min: 0.5, scores: map[string]float64{"0.95": 0.95, "0.25": 0.8}}
	p := rankedParams()
	p.MaxFacts = 1
	p.Host = "box"
	hits, err := RecallJudge(s, "порог покрытия поиск фактов", "", p, r)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].ID != foreign.ID {
		t.Fatalf("hits = %v, want the higher-scored foreign fact %s", ids(hits), foreign.ID)
	}
	if !hits[0].Displaced {
		t.Error("the omitted fact of this host did not mark the served foreign fact as displaced")
	}
}

// gateBase holds facts that pass the default gates for gatePrompt and facts that hold one or two of its terms.
func gateBase(t *testing.T, passing int) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "facts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	for i := range passing {
		if _, err := s.Add(fmt.Sprintf("Ротация журналов nginx на хосте loffe, вариант %d.", i), nil, "test 2026-10-22"); err != nil {
			t.Fatal(err)
		}
	}
	for _, content := range []string{
		"Журналы systemd держит journald, его лимит задан в journald.conf.",
		"Конфиг nginx на loffe лежит в /etc/nginx/conf.d.",
	} {
		if _, err := s.Add(content, nil, "test 2026-10-22"); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

const gatePrompt = "почему журналы nginx на loffe не ротируются"

// With a judge, a fact the words found but the term gate turned away still reaches the judge while there is room
// among its candidates, and is served when the judge scores it; recall without a judge keeps the gate.
func TestRecallJudgeReadsAFactTheGateTurnedAway(t *testing.T) {
	s := gateBase(t, 0)
	if plain, err := Recall(s, gatePrompt, "", Default); err != nil || len(plain) != 0 {
		t.Fatalf("the gate must turn both facts away without a judge: %v %v", ids(plain), err)
	}
	trace := &Trace{}
	p := Default
	p.Trace = trace
	r := &fakeRanker{min: 0.5, scores: map[string]float64{"journald": 0.9}}
	hits, err := RecallJudge(s, gatePrompt, "", p, r)
	if err != nil {
		t.Fatal(err)
	}
	if r.asked != 2 || len(hits) != 1 || !contains(hits[0].Content, "journald") {
		t.Fatalf("asked %d, hits %v", r.asked, ids(hits))
	}
	gated := 0
	for _, st := range trace.Lexical {
		if st.Verdict == VerdictGatedToJudge {
			gated++
		}
	}
	if gated != 2 {
		t.Errorf("trace marks %d facts as sent to the judge below the gate: %+v", gated, trace.Lexical)
	}
}

// The judge reads the first facts of the search in BM25 order, whether the gate let them pass or not.
func TestRecallJudgeReadsTheFirstFactsInSearchOrder(t *testing.T) {
	s := gateBase(t, 12)
	// One rare word of the prompt: the gate turns the fact away, BM25 ranks it above the facts that share common words.
	if _, err := s.Add("Логи не ротируются, пока logrotate выключен.", nil, "test 2026-10-22"); err != nil {
		t.Fatal(err)
	}
	trace := &Trace{}
	p := Default
	p.Trace = trace
	r := &fakeRanker{min: 0.5, scores: map[string]float64{"journald": 0.9}}
	if _, err := RecallJudge(s, gatePrompt, "", p, r); err != nil {
		t.Fatal(err)
	}
	if len(trace.Lexical) <= r.Candidates() || len(r.got) != r.Candidates() {
		t.Fatalf("search returned %d facts, the judge read %d", len(trace.Lexical), len(r.got))
	}
	for i, text := range r.got {
		if text != trace.Lexical[i].Content {
			t.Fatalf("fact %d sent to the judge is %q, the search put %q there", i, text, trace.Lexical[i].Content)
		}
	}
	if !slices.ContainsFunc(r.got, func(text string) bool { return contains(text, "logrotate") }) {
		t.Errorf("the fact below the gate that BM25 ranks high was not read: %q", r.got)
	}
	for _, st := range trace.Lexical[r.Candidates():] {
		if st.Verdict == VerdictGatedToJudge {
			t.Errorf("a fact past the limit is marked as sent: %+v", st)
		}
	}
}

// A judge the shared breaker paused is skipped, not failed: the trace says so, and the words decide (#257).
func TestRecallJudgeMarksAPausedJudge(t *testing.T) {
	s, _ := meaningBase(t)
	prompt := "Gradle требует JAVA_HOME, иначе сборка не находит JDK, а Caddy конфиг"
	trace := &Trace{}
	p := Default
	p.Trace = trace
	r := &fakeRanker{min: 0.5, err: fmt.Errorf("%w: 40s left", judge.ErrPaused)}
	if _, err := RecallJudge(s, prompt, "", p, r); err != nil {
		t.Fatal(err)
	}
	if !trace.Paused || !strings.HasPrefix(trace.Fallback, "judge skipped") {
		t.Errorf("paused %v, fallback %q", trace.Paused, trace.Fallback)
	}

	trace = &Trace{}
	p.Trace = trace
	r.err = errors.New("decisions 500")
	if _, err := RecallJudge(s, prompt, "", p, r); err != nil {
		t.Fatal(err)
	}
	if trace.Paused || !strings.HasPrefix(trace.Fallback, "judge failed") {
		t.Errorf("a failure marked as a pause: paused %v, fallback %q", trace.Paused, trace.Fallback)
	}
}

// The judge path keeps the ticket exactness rule too: PROJ-1235 cannot reach PROJ-1234 through a shared keyword.
func TestRecallJudgeKeepsTheTicketGate(t *testing.T) {
	s, other, wanted := ticketBase(t)
	hits, err := RecallJudge(s, "PROJ-1235 postgres port", "x", rankedParams(),
		&fakeRanker{min: 0.5, scores: map[string]float64{"": 0.9}})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].ID != wanted {
		t.Fatalf("hits = %v, want %s", ids(hits), wanted)
	}
	for _, h := range hits {
		if h.ID == other {
			t.Fatalf("a prompt for PROJ-1235 recalled PROJ-1234: %v", ids(hits))
		}
	}
}

// A fact the session already got gives way to the next one, with the judge on as well.
func TestRecallJudgeSkipsWhatTheSessionSaw(t *testing.T) {
	s, _ := rankedBase(t)
	p := rankedParams()
	r := &fakeRanker{min: 0.5, scores: map[string]float64{"JAVA_HOME": 0.93}}
	first, err := RecallJudge(s, "почему сборка падает и что не так с конфигом caddy", "", p, r)
	if err != nil || len(first) == 0 {
		t.Fatalf("first = %v, %v", ids(first), err)
	}
	p.Seen = map[string]bool{first[0].ID: true}
	second, err := RecallJudge(s, "почему сборка падает и что не так с конфигом caddy", "", p, r)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range second {
		if h.ID == first[0].ID {
			t.Fatal("a fact the session already got came again")
		}
	}
}
