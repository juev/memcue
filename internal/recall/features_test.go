package recall

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/juev/memcue/internal/store"
)

// featureRanker scores each candidate by its id and records what it was given.
type featureRanker struct {
	scores   map[string]float64
	prompt   PromptFeatures
	features []Features
	texts    int // calls of Scores, which a feature reader must not get
}

func (f *featureRanker) Min() float64    { return 0.5 }
func (f *featureRanker) Candidates() int { return 10 }
func (f *featureRanker) Scores(context.Context, string, []string) ([]float64, error) {
	f.texts++
	return nil, errors.New("texts")
}
func (f *featureRanker) ScoreFeatures(_ context.Context, p PromptFeatures, fs []Features) ([]float64, error) {
	f.prompt, f.features = p, fs
	out := make([]float64, len(fs))
	for i, x := range fs {
		out[i] = f.scores[x.ID]
	}
	return out, nil
}

func TestRecallJudgeHandsAFeatureRankerTheFeatures(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "facts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	caddy, err := s.AddWith("Публичный Caddy монтирует каталог configs/caddy и перечитывает конфиг только с полным путём.",
		[]string{"caddy", "reload"}, "infra 2026-10-20", "2027-01-01",
		store.Meta{Kind: "pitfall", Scope: "host:loffe", Source: "user"})
	if err != nil {
		t.Fatal(err)
	}
	if caddy, err = s.Correct(caddy.ID, caddy.Content+" Конфиг Caddy проверять перед reload.", caddy.Keywords); err != nil {
		t.Fatal(err)
	}
	other, err := s.Add("Конфиг mihomo держит порт 1080 для HTTP и SOCKS.", nil, "infra 2026-10-20")
	if err != nil {
		t.Fatal(err)
	}
	r := &featureRanker{scores: map[string]float64{caddy.ID: 0.4, other.ID: 0.9}}
	hits, err := RecallJudge(s, "Caddy перечитывает конфиг после reload?", "", rankedParams(), r)
	if err != nil {
		t.Fatal(err)
	}
	if r.texts != 0 {
		t.Errorf("Scores was called %d times; a feature reader reads features", r.texts)
	}
	if len(hits) != 1 || hits[0].ID != other.ID {
		t.Fatalf("hits = %v, want only the fact the reader scored above its threshold", ids(hits))
	}
	if r.prompt.Terms == 0 || r.prompt.Known == 0 || r.prompt.Runes != len([]rune("Caddy перечитывает конфиг после reload?")) ||
		r.prompt.Weights <= 0 {
		t.Errorf("prompt features %+v", r.prompt)
	}
	if len(r.features) != 2 {
		t.Fatalf("features of %d candidates, want 2", len(r.features))
	}
	var f Features
	for i, x := range r.features {
		if x.Rank != i {
			t.Errorf("rank %d at position %d", x.Rank, i)
		}
		if x.ID == caddy.ID {
			f = x
		}
	}
	if !f.Passed || f.Matched < 3 || f.Coverage <= 0 || f.MatchedMax <= 0 || f.MatchedMax > 1 {
		t.Errorf("gate features %+v", f)
	}
	if f.ContentCov <= 0 || f.KeywordCov <= 0 || f.QueryCov != 0 || f.Ticket {
		t.Errorf("coverage features %+v", f)
	}
	if f.Kind != "pitfall" || f.Scope != "host" || f.Source != "user" || !f.Expires || f.Provisional {
		t.Errorf("metadata features %+v", f)
	}
	if f.Versions != 2 || f.Keywords != 2 || f.Queries != 0 || f.Runes != len([]rune(caddy.Content)) {
		t.Errorf("shape features %+v", f)
	}
	// The other fact was written after the correction, so the corrected caddy fact is the older one.
	if f.AgeDays <= 0 {
		t.Errorf("age %v, want the time since the newest version", f.AgeDays)
	}
}

func TestFeaturesMarkTheTicketOfThePrompt(t *testing.T) {
	s, a, b := ticketBase(t)
	r := &featureRanker{scores: map[string]float64{a: 0.9, b: 0.9}}
	if _, err := RecallJudge(s, "PROJ-1234 и PROJ-1235: какой postgres port?", "", rankedParams(), r); err != nil {
		t.Fatal(err)
	}
	if r.prompt.Tickets != 2 || len(r.features) != 2 {
		t.Fatalf("prompt %+v, %d candidates", r.prompt, len(r.features))
	}
	for _, f := range r.features {
		if !f.Ticket || f.Kind != "decision" || f.Scope != "repo" {
			t.Errorf("features %+v", f)
		}
	}
}
