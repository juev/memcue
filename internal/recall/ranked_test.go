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
}

func (f *fakeRanker) Min() float64       { return f.min }
func (f *fakeRanker) Candidates() int    { return 10 }
func (f *fakeRanker) Scores(_ context.Context, _ string, facts []string) ([]float64, error) {
	f.asked = len(facts)
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
	hits, err := RecallRanked(s, "почему сборка падает и что не так с конфигом caddy", "", Default, r)
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
	hits, err := RecallRanked(s, "какой путь нужен для конфига caddy и почему сборка падает", "", Default, r)
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
	p := Default
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
