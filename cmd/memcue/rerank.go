package main

import (
	"path/filepath"

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
	// The hook is a fresh process on every prompt, so a pause after a failure is remembered in a file.
	if dir, err := stateDir(); err == nil {
		set.StateFile = filepath.Join(dir, "judge.pause")
	}
	return rerank.New(set)
}

// recallFacts is the one way recall is decided: the words, plus vector candidates, plus the judge when it is
// configured. A missing judge keeps the lexical answer; a missing embedder only removes the vector candidates.
func recallFacts(s *store.Store, prompt, tier string, p recall.Params) ([]recall.Hit, error) {
	r := ranker()
	if r == nil {
		return recall.Recall(s, prompt, tier, p)
	}
	return recall.RecallJudge(s, prompt, tier, p, r, embedder())
}
