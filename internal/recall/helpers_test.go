package recall

import (
	"context"
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

// rankedParams relaxes the coverage gate for the synthetic facts in these tests; the gate itself is tested in
// recall_test.go and in TestRecallJudgeKeepsTheTicketGate.
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

// ticketBase holds two facts of one project whose keywords share the project word.
func ticketBase(t *testing.T) (*store.Store, string, string) {
	t.Helper()
	s, err := store.Open(t.TempDir() + "/facts.db")
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
	return s, a.ID, b.ID
}

// rankedBase holds four unrelated facts the words can find.
func rankedBase(t *testing.T) (*store.Store, map[string]string) {
	t.Helper()
	s, err := store.Open(t.TempDir() + "/facts.db")
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
