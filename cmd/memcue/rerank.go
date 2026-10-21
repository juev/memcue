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
		set.StateFile = filepath.Join(dir, "rerank.pause")
	}
	return rerank.New(set)
}

// recallFacts is the one way recall is decided: by the words alone, by the words plus the meaning pass, or by the words
// with a judge choosing the order. Every extra reader is off unless its endpoint is named.
func recallFacts(s *store.Store, prompt, tier string, p recall.Params) ([]recall.Hit, error) {
	if r := ranker(); r != nil {
		return recall.RecallRanked(s, prompt, tier, p, r)
	}
	return recall.RecallHybrid(s, prompt, tier, p, vectorParams())
}
