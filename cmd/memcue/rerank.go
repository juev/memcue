package main

import (
	judgepkg "github.com/juev/memcue/internal/judge"
	"github.com/juev/memcue/internal/recall"
	"github.com/juev/memcue/internal/rerank"
	"github.com/juev/memcue/internal/store"
)

// ranker builds the rerank pass from the environment. A nil Ranker, the default, keeps recall lexical: the words
// choose the facts and the coverage gate decides.
func ranker() recall.Ranker {
	set := rerank.FromEnv()
	if set == nil {
		return nil
	}
	// Triage, rerank and duplicate share one breaker and one state file.
	set.Breaker = judgepkg.Shared
	return rerank.New(set)
}

// recallFacts is the one way recall is decided: the words, plus the judge when it is configured. A missing judge keeps
// the lexical answer.
func recallFacts(s *store.Store, prompt, tier string, p recall.Params) ([]recall.Hit, error) {
	r := ranker()
	if r == nil {
		if p.Trace != nil {
			p.Trace.Fallback = "no judge configured"
		}
		return recall.Recall(s, prompt, tier, p)
	}
	return recall.RecallJudge(s, prompt, tier, p, r)
}
