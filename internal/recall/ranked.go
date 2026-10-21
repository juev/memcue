package recall

import (
	"context"
	"sort"
	"time"

	"github.com/juev/memcue/internal/store"
	"github.com/juev/memcue/internal/text"
)

// Ranker scores how much each of the facts the words found would help answer the prompt, on 0..1. A nil Ranker turns
// the pass off.
type Ranker interface {
	Scores(ctx context.Context, prompt string, facts []string) ([]float64, error)
	Min() float64
	Candidates() int
}

// minRankCandidates is the smallest candidate list worth asking about: with one fact there is nothing to choose.
const minRankCandidates = 2

// RecallRanked is recall with the words choosing the candidates and a judge choosing the order: FTS5 returns
// Candidates facts, the ranker scores them, and up to MaxFacts above Min are given. Any failure of the ranker leaves
// the ordinary lexical answer in place, so a dead endpoint costs precision, never the memory.
func RecallRanked(s *store.Store, prompt, sessionTier string, p Params, r Ranker) ([]Hit, error) {
	hits, ok := ranked(s, prompt, p, r)
	if !ok {
		return Recall(s, prompt, sessionTier, p)
	}
	return hits, nil
}

// ranked is the rerank pass; ok is false when it could not decide and the caller is to use the words' own answer.
func ranked(s *store.Store, prompt string, p Params, r Ranker) ([]Hit, bool) {
	if r == nil {
		return nil, false
	}
	found, err := s.Search(text.Terms(prompt), r.Candidates())
	if err != nil || len(found) < minRankCandidates {
		return nil, false
	}
	today := p.Today
	if today == "" {
		today = time.Now().UTC().Format(time.DateOnly)
	}
	var facts []store.Hit
	var contents []string
	for _, f := range found {
		if p.Seen[f.ID] || store.Expired(f.Expires, today) {
			continue
		}
		facts = append(facts, f)
		contents = append(contents, f.Content)
	}
	if len(facts) < minRankCandidates {
		return nil, false
	}
	scores, err := r.Scores(context.Background(), prompt, contents)
	if err != nil || len(scores) != len(facts) {
		return nil, false
	}
	order := make([]int, len(facts))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return scores[order[a]] > scores[order[b]] })
	maxFacts := p.MaxFacts
	if maxFacts <= 0 {
		maxFacts = len(order)
	}
	var hits []Hit
	for _, i := range order {
		if len(hits) >= maxFacts {
			break
		}
		if scores[i] < r.Min() {
			break
		}
		hits = append(hits, Hit{Fact: facts[i].Fact, Meaning: scores[i]})
	}
	return hits, true
}
