package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	judgepkg "github.com/juev/memcue/internal/judge"
	"github.com/juev/memcue/internal/localrank"
	"github.com/juev/memcue/internal/recall"
	"github.com/juev/memcue/internal/rerank"
	"github.com/juev/memcue/internal/store"
)

// ranker builds the rerank pass from the environment. MEMCUE_RERANK names the reader: `judge` is Jev scoring each
// candidate, `local` is the tree model built into memcue (#321), `listwise` is the main chat model asking one listwise
// question (#281), `off` keeps the words. Without the variable Jev runs when its endpoint is configured and the words
// decide otherwise, so recall without a judge key makes no model call. The local reader is opt-in: on the gold base it
// served a useful fact to as many prompts as the words with half their noise (docs/research/local-readers-2026-10.md),
// but it learnt that base, and on a small fresh base it scored a fact that held every prompt term below its threshold.
// The listwise reader is opt-in too: it served as many useful prompts as Jev with two more noise facts and three times
// the latency (docs/research/listwise-2026-10.md). A nil Ranker keeps recall lexical, with the words choosing and the
// coverage gate deciding.
func ranker() recall.Ranker {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("MEMCUE_RERANK"))) {
	case "off", "none", "words", "0":
		return nil
	case "listwise", "main":
		return listwiseRanker()
	case "local":
		return localrank.Ranker{}
	}
	if r := jevRanker(); r != nil {
		return r
	}
	return nil
}

// jevRanker is the pass that asks the decisions endpoint one yes/no question per candidate.
func jevRanker() recall.Ranker {
	set := rerank.FromEnv()
	if set == nil {
		return nil
	}
	// Triage, rerank and duplicate share one breaker and one state file.
	set.Breaker = judgepkg.Shared
	return rerank.New(set)
}

// listwiseRanker is the pass that asks the main chat model which facts would help, best first.
func listwiseRanker() recall.Ranker {
	set := rerank.ListwiseFromEnv()
	if set == nil {
		return nil
	}
	set.Breaker = listwisePause()
	return rerank.NewListwise(set)
}

// listwisePause is the pause file of the listwise reader. It is its own, not judge.pause: a dead main model must not
// silence the Jev roles, and a dead Jev must not silence the reader (#281).
var listwisePause = sync.OnceValue(func() *judgepkg.Breaker {
	dir, err := stateDir()
	if err != nil {
		return judgepkg.New("")
	}
	return judgepkg.New(filepath.Join(dir, "listwise.pause"))
})

// recallFacts is the one way recall is decided: the words, plus the configured reader. A missing reader keeps the
// lexical answer.
func recallFacts(s *store.Store, prompt, tier string, p recall.Params) ([]recall.Hit, error) {
	r := ranker()
	if r == nil {
		if p.Trace != nil {
			p.Trace.Fallback = "no rerank reader configured"
		}
		return recall.Recall(s, prompt, tier, p)
	}
	return recall.RecallJudge(s, prompt, tier, p, r)
}
