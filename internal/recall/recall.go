// Package recall decides which facts a prompt should bring into the agent's context.
//
// Candidates come from FTS5 over all facts. A fact passes when it holds enough of the prompt's informative terms:
// a minimum number of them and a minimum IDF-weighted share. Where a fact was learnt does not filter it; facts
// learnt in the session's repository only win ties.
package recall

import (
	"math"
	"slices"
	"sort"
	"strings"

	"github.com/juev/memcue/internal/store"
	"github.com/juev/memcue/internal/text"
	"github.com/juev/memcue/internal/tier"
)

// Params are the knobs of the decision.
type Params struct {
	Candidates     int     // facts taken from FTS5
	MinTerms       int     // a prompt with fewer informative terms the facts know recalls nothing
	MinMatched     int     // distinct prompt terms a fact must hold
	MinCoverage    float64 // IDF-weighted share of the prompt's terms a fact must hold
	MaxPromptTerms int     // a longer prompt keeps its rarest terms only
	MaxFacts       int
	Seen           map[string]bool // facts the session already got: they give way to the next ones
}

// Default holds the values chosen on the dev set of facts (2026-10-19, 2335 facts migrated from the notes, 180 real
// prompts): 48 prompts got facts, 77 facts, 19 useful, 40 noise; 26 of 30 recall questions found a fact of their
// note. Three matched terms cut the noise; coverage above 0.25 starts losing facts of other repositories.
var Default = Params{
	Candidates:     80,
	MinTerms:       2,
	MinMatched:     3,
	MinCoverage:    0.15,
	MaxPromptTerms: 24,
	MaxFacts:       2,
}

// Hit is a fact chosen for the prompt.
type Hit struct {
	store.Fact
	Coverage float64
	Matched  int
}

// Recall returns the facts to inject for prompt in a session of repository sessionTier ("" outside a repository).
func Recall(s *store.Store, prompt, sessionTier string, p Params) ([]Hit, error) {
	terms := text.Terms(prompt)
	if len(terms) < p.MinTerms {
		return nil, nil
	}
	df, total, err := s.DocFreq(terms)
	if err != nil {
		return nil, err
	}
	weight := map[string]float64{}
	var known []string
	for _, t := range terms {
		if df[t] > 0 { // a word no fact holds says nothing about which fact matters
			weight[t] = math.Log(1 + float64(total)/float64(df[t]))
			known = append(known, t)
		}
	}
	if len(known) < p.MinTerms {
		return nil, nil
	}
	if p.MaxPromptTerms > 0 && len(known) > p.MaxPromptTerms {
		sort.SliceStable(known, func(i, j int) bool { return weight[known[i]] > weight[known[j]] })
		known = known[:p.MaxPromptTerms]
	}
	var sum float64
	for _, t := range known {
		sum += weight[t]
	}

	found, err := s.Search(known, p.Candidates)
	if err != nil {
		return nil, err
	}
	var hits []Hit
	for _, f := range found {
		if p.Seen[f.ID] {
			continue
		}
		var got float64
		matched := 0
		for _, t := range known {
			if slices.ContainsFunc(f.Stems, func(st string) bool { return text.Covers(t, st) }) {
				got += weight[t]
				matched++
			}
		}
		if cov := got / sum; matched >= p.MinMatched && cov >= p.MinCoverage {
			hits = append(hits, Hit{Fact: f.Fact, Coverage: cov, Matched: matched})
		}
	}
	// Ties: a fact learnt in the session's repository, then a global one, then the rest; then the newer fact.
	place := func(h Hit) int {
		switch {
		case sessionTier != "" && strings.HasPrefix(h.Origin, sessionTier+" "):
			return 0
		case strings.HasPrefix(h.Origin, tier.Global+" "):
			return 1
		}
		return 2
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Coverage != hits[j].Coverage {
			return hits[i].Coverage > hits[j].Coverage
		}
		if pi, pj := place(hits[i]), place(hits[j]); pi != pj {
			return pi < pj
		}
		return hits[i].ID > hits[j].ID
	})
	if len(hits) > p.MaxFacts {
		hits = hits[:p.MaxFacts]
	}
	return hits, nil
}
