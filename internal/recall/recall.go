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
	"time"

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
	Host           string          // this machine: facts whose scope is host:<Host> win ties; empty means no preference
	Seen           map[string]bool // facts the session already got: they give way to the next ones
	Today          string          // "YYYY-MM-DD" the expired facts are judged by; empty means time.Now().UTC()
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
	Coverage  float64
	Matched   int
	Displaced bool // a fact of another host took a slot while a fact of this host was left out
}

// Recall returns the facts to inject for prompt in a session of repository sessionTier ("" outside a repository).
func Recall(s *store.Store, prompt, sessionTier string, p Params) ([]Hit, error) {
	terms := text.Terms(prompt)
	var tickets []string
	for _, t := range terms {
		if text.IsTicket(t) {
			tickets = append(tickets, t)
		}
	}
	// A ticket key is a precise anchor: on its own it says enough, without a second known term.
	minTerms := p.MinTerms
	if len(tickets) > 0 {
		minTerms = 1
	}
	if len(terms) < minTerms {
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
	if len(known) < minTerms {
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
	// A prompt cannot match more terms than its facts know: a short question ("dream memcue") often keeps two terms,
	// and the dev-set minimum of three then silenced every fact about them.
	minMatched := p.MinMatched
	if len(tickets) > 0 {
		minMatched = 1
	}
	if len(known) < minMatched {
		minMatched = len(known)
	}
	today := p.Today
	if today == "" {
		today = time.Now().UTC().Format(time.DateOnly)
	}
	var hits []Hit
	for _, f := range found {
		if p.Seen[f.ID] || store.Expired(f.Expires, today) {
			continue
		}
		// A prompt that names a ticket must not recall another ticket through a shared project word: PROJ-1235
		// and PROJ-1234 can share "proj" in a keyword, but only the exact key is the subject of the prompt.
		if len(tickets) > 0 && !slices.ContainsFunc(f.Stems, func(st string) bool { return slices.Contains(tickets, st) }) {
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
		if cov := got / sum; matched >= minMatched && cov >= p.MinCoverage {
			hits = append(hits, Hit{Fact: f.Fact, Coverage: cov, Matched: matched})
		}
	}
	// Ties: a fact of this host, then one learnt in the session's repository, then a global one, then the rest;
	// then the newer fact. The host only orders facts of equal coverage; it never excludes another host's fact.
	place := func(h Hit) int {
		switch {
		case hostScope(h.Scope, p.Host):
			return 0
		case sessionTier != "" && strings.HasPrefix(h.Origin, sessionTier+" "):
			return 1
		case strings.HasPrefix(h.Origin, tier.Global+" "):
			return 2
		}
		return 3
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
	served := hits
	if len(hits) > p.MaxFacts {
		served = hits[:p.MaxFacts]
	}
	// Did another host take one of the slots while a fact of this host was left out? The log uses this to decide
	// whether a hard filter is ever needed; it never changes the choice.
	if p.Host != "" {
		omitted := false
		for _, h := range hits[len(served):] {
			if hostScope(h.Scope, p.Host) {
				omitted = true
				break
			}
		}
		if omitted {
			for i := range served {
				if otherHostScope(served[i].Scope, p.Host) {
					served[i].Displaced = true
				}
			}
		}
	}
	return served, nil
}

// hostScope reports whether scope names this host.
func hostScope(scope, host string) bool {
	return host != "" && strings.EqualFold(scope, "host:"+host)
}

// otherHostScope reports whether scope names a host other than this one.
func otherHostScope(scope, host string) bool {
	if host == "" || !strings.HasPrefix(strings.ToLower(scope), "host:") {
		return false
	}
	return !hostScope(scope, host)
}
