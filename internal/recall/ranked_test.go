package recall

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"testing"

	"github.com/juev/memcue/internal/store"
)

// fakeRanker answers with the score of each fact looked up by a word in its text, so a test says what it means.
type fakeRanker struct {
	scores map[string]float64 // substring of the fact -> score
	err    error
	min    float64
	asked  int
	got    []string // the facts of the last call
}

func (f *fakeRanker) Min() float64    { return f.min }
func (f *fakeRanker) Candidates() int { return 10 }
func (f *fakeRanker) Scores(_ context.Context, _ string, facts []string) ([]float64, error) {
	f.asked = len(facts)
	f.got = append([]string(nil), facts...)
	if f.err != nil {
		return nil, f.err
	}
	out := make([]float64, len(facts))
	for i, text := range facts {
		for word, score := range f.scores {
			if contains(text, word) {
				out[i] = score
			}
		}
	}
	return out, nil
}

// rankedParams relaxes the coverage gate for the synthetic facts in this file; the gate itself is tested in
// recall_test.go and in TestRankedKeepsTheTicketGate.
func rankedParams() Params {
	p := Default
	p.MinMatched = 1
	p.MinCoverage = 0.1
	return p
}

func contains(haystack, needle string) bool {
	return len(needle) == 0 || (len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

// The rerank path must keep the ticket exactness rule: PROJ-1235 cannot reach PROJ-1234 through a shared keyword.
func TestRankedKeepsTheTicketGate(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "facts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	a, err := s.AddWith("Тикет PROJ-1234: берём в работу.", []string{"PROJ", "postgres", "port"},
		"x 2026-10-20", "", store.Meta{Kind: "decision", Scope: "repo:x"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.AddWith("Тикет PROJ-1235: отложен.", []string{"PROJ", "postgres", "port"},
		"x 2026-10-20", "", store.Meta{Kind: "decision", Scope: "repo:x"})
	if err != nil {
		t.Fatal(err)
	}
	r := &fakeRanker{min: 0.5, scores: map[string]float64{"": 0.9}}
	hits, err := RecallRanked(s, "PROJ-1235 postgres port", "x", rankedParams(), r)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].ID != b.ID {
		t.Fatalf("hits = %v, want %s", ids(hits), b.ID)
	}
	for _, h := range hits {
		if h.ID == a.ID {
			t.Fatalf("a prompt for PROJ-1235 recalled PROJ-1234: %v", ids(hits))
		}
	}
}

func rankedBase(t *testing.T) (*store.Store, map[string]string) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "facts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ids := map[string]string{}
	for _, content := range []string{
		"Caddy перечитывает конфиг только с полным путём к Caddyfile.",
		"Gradle вне make требует JAVA_HOME, иначе сборка не находит JDK.",
		"Крон-задача microapp-nightly идёт каждые четыре часа.",
		"Прокси mihomo слушает 1080 и смешивает HTTP с SOCKS.",
	} {
		f, err := s.Add(content, nil, "test 2026-10-21")
		if err != nil {
			t.Fatal(err)
		}
		ids[content] = f.ID
	}
	return s, ids
}

func TestRankedPutsTheUsefulFactFirst(t *testing.T) {
	s, ids := rankedBase(t)
	r := &fakeRanker{min: 0.5, scores: map[string]float64{"JAVA_HOME": 0.93, "Caddy": 0.2, "mihomo": 0.4}}
	hits, err := RecallRanked(s, "почему сборка падает и что не так с конфигом caddy", "", rankedParams(), r)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("no hits")
	}
	if hits[0].ID != ids["Gradle вне make требует JAVA_HOME, иначе сборка не находит JDK."] {
		t.Fatalf("first hit = %q", hits[0].Content)
	}
	if math.Abs(hits[0].Meaning-0.93) > 1e-9 {
		t.Errorf("Meaning = %v, want the ranker's score", hits[0].Meaning)
	}
	if r.asked < 2 {
		t.Errorf("the ranker saw %d facts, want the candidate list", r.asked)
	}
}

func TestRankedDropsWhatIsBelowTheThreshold(t *testing.T) {
	s, _ := rankedBase(t)
	r := &fakeRanker{min: 0.8, scores: map[string]float64{"Caddy": 0.2, "mihomo": 0.4}}
	hits, err := RecallRanked(s, "какой путь нужен для конфига caddy и почему сборка падает", "", rankedParams(), r)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("hits = %d, want none above 0.8", len(hits))
	}
}

func TestDeadRankerLeavesTheWords(t *testing.T) {
	s, _ := rankedBase(t)
	prompt := "какой путь нужен для конфига caddy и почему сборка падает"
	plain, err := Recall(s, prompt, "", Default)
	if err != nil {
		t.Fatal(err)
	}
	if len(plain) == 0 {
		t.Fatal("the test needs a prompt the words do find")
	}
	hits, err := RecallRanked(s, prompt, "", Default, &fakeRanker{min: 0.5, err: errors.New("down")})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != len(plain) {
		t.Fatalf("hits = %v, lexical = %v", ids(hits), ids(plain))
	}
	for i := range plain {
		if hits[i].ID != plain[i].ID {
			t.Fatalf("hits = %v, lexical = %v", ids(hits), ids(plain))
		}
	}
}

func TestRankedSkipsWhatTheSessionSaw(t *testing.T) {
	s, _ := rankedBase(t)
	r := &fakeRanker{min: 0.5, scores: map[string]float64{"JAVA_HOME": 0.93}}
	p := rankedParams()
	first, err := RecallRanked(s, "почему сборка падает и что не так с конфигом caddy", "", p, r)
	if err != nil || len(first) == 0 {
		t.Fatalf("first = %v, %v", ids(first), err)
	}
	p.Seen = map[string]bool{first[0].ID: true}
	second, err := RecallRanked(s, "почему сборка падает и что не так с конфигом caddy", "", p, r)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range second {
		if h.ID == first[0].ID {
			t.Fatal("a fact the session already got came again")
		}
	}
}

// A provisional fact above the threshold is served even when an approved candidate earlier in the order scores below
// it: the approved-before-provisional order is not a score order, so a weak approved fact must not cut the tail.
func TestRecallJudgeKeepsProvisionalAboveThreshold(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "facts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	approved, err := s.AddWith("Approved fact about the postgres port.", []string{"postgres", "port"},
		"x 2026-10-20", "", store.Meta{})
	if err != nil {
		t.Fatal(err)
	}
	prov, err := s.AddWith("Provisional fact about the postgres port.", []string{"postgres", "port"},
		"x 2026-10-20", "", store.Meta{Review: store.Provisional})
	if err != nil {
		t.Fatal(err)
	}
	r := &fakeRanker{min: 0.5, scores: map[string]float64{"Approved": 0.1, "Provisional": 0.9}}
	p := rankedParams()
	p.MaxFacts = 2
	hits, err := RecallJudge(s, "postgres port", "x", p, r)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].ID != prov.ID {
		t.Fatalf("hits = %v, want only the provisional fact %s (approved %s scored below the threshold)",
			ids(hits), prov.ID, approved.ID)
	}
	if !hits[0].Provisional {
		t.Fatalf("hit = %+v, want the provisional flag", hits[0])
	}
}
