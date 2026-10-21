package recall

import (
	"context"
	"sort"

	"github.com/juev/memcue/internal/store"
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
	facts, err := recallCandidates(s, prompt, p)
	if err != nil || len(facts) < minRankCandidates {
		return nil, false
	}
	if n := r.Candidates(); n > 0 && len(facts) > n {
		facts = facts[:n]
	}
	contents := make([]string, len(facts))
	for i, f := range facts {
		contents[i] = f.Content
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
		hits = append(hits, Hit{Fact: facts[i].Fact, Coverage: facts[i].Coverage, Matched: facts[i].Matched,
			Meaning: scores[i]})
	}
	return hits, true
}
