// Package recall finds facts for a text. Search serves the queries the agent writes itself (#361). Recall finds the
// facts close to a text, which dream and vet use to group a fact with its neighbours.
//
// Recall takes candidates from FTS5 over all facts, in BM25 order. A fact passes when it holds enough of the text's
// informative terms: a minimum number of them and a minimum IDF-weighted share. The facts that pass come in BM25
// order: the share gates, it does not rank (#338).
package recall

import (
	"cmp"
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
	// MinContained lets a fact pass below MinCoverage when the text holds nearly all of the fact's own terms: the
	// IDF-weighted share of them it covers. Coverage alone is one-sided: a long restatement of a short fact covers
	// all of it, while the short fact holds only a part of the long one's terms. Zero leaves it off.
	MinContained float64
	Seen         map[string]bool // facts left out, such as the fact whose neighbours are sought
	Today        string          // local "YYYY-MM-DD" the expired facts are judged by; empty means store.Today()
}

// Hit is a fact close to the text.
type Hit struct {
	store.Fact
	Coverage    float64
	Matched     int
	Provisional bool    // auto-approved for a TTL, comes after approved facts
	bm25        float64 // the search's BM25 score, lower is better
}

// Recall returns the facts close to text: those that pass the gates of p, at most p.MaxFacts.
func Recall(s *store.Store, text string, p Params) ([]Hit, error) {
	hits, err := recallCandidates(s, text, p)
	if err != nil {
		return nil, err
	}
	// BM25 order, approved before provisional. The coverage sort that stood here served a useful fact to fewer
	// prompts with more noise: on 409 blind-labelled Claude Code prompts where the two orders differ, 180 against 205
	// with 41% noise against 30% (docs/research/search-chain-2026-10.md). Ties of equal BM25, such as the same text
	// stored twice: a global one first, then the newer fact.
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Provisional != hits[j].Provisional {
			return !hits[i].Provisional
		}
		if hits[i].bm25 != hits[j].bm25 {
			return hits[i].bm25 < hits[j].bm25
		}
		if pi, pj := place(hits[i].Fact, nil, ""), place(hits[j].Fact, nil, ""); pi != pj {
			return pi < pj
		}
		return newer(hits[i].Fact, hits[j].Fact)
	})
	if maxFacts := capFacts(p.MaxFacts, len(hits)); len(hits) > maxFacts {
		hits = hits[:maxFacts]
	}
	return hits, nil
}

// place ranks a fact for a tie of equal BM25 score: this host first, then the session's repository,
// then a global origin, then everything else. A fact was learnt in the session's project when its origin starts with
// any of the project's keys: a mirror's other host or a fork's upstream counts. The host only orders facts; it never
// excludes another host's fact.
func place(h store.Fact, project []string, host string) int {
	switch {
	case hostScope(h.Scope, host):
		return 0
	case slices.ContainsFunc(project, func(k string) bool { return k != "" && strings.HasPrefix(h.Origin, k+" ") }):
		return 1
	case strings.HasPrefix(h.Origin, tier.Global+" "):
		return 2
	}
	return 3
}

// newer orders two facts of the same rank: by creation time first, because a base written before the monotonic ids
// can hold same-millisecond facts whose ids are in random order, then by id for a stable order.
func newer(a, b store.Fact) bool {
	if !a.Created.Equal(b.Created) {
		return a.Created.After(b.Created)
	}
	return a.ID > b.ID
}

// capFacts returns how many of the available facts a pass may serve. A non-positive MaxFacts means no cap: every
// caller takes that as "all of them", so a zero never serves nothing and a negative one never slices past the start.
func capFacts(maxFacts, available int) int {
	if maxFacts <= 0 {
		return available
	}
	return maxFacts
}

// recallCandidates applies the lexical gates and returns the facts that pass, in Search order and without the
// MaxFacts cap. Recall adds the tie order and the cap.
func recallCandidates(s *store.Store, prompt string, p Params) (hits []Hit, err error) {
	var short []Hit // below MinCoverage, for the containment check
	terms := text.Terms(prompt)
	var tickets, numbers []string
	for _, t := range terms {
		switch {
		case text.IsTicket(t):
			tickets = append(tickets, t)
		case text.IsNumberTerm(t):
			numbers = append(numbers, t)
		}
	}
	// A ticket key is a precise anchor: on its own it says enough, without a second known term. A version or date
	// is exact too, so a prompt that is nothing but one ("0.17.4") must not be silenced either; it only ever adds
	// candidates, it never filters the pool the way a ticket does.
	minTerms := p.MinTerms
	if len(tickets) > 0 || (len(numbers) > 0 && len(numbers) == len(terms)) {
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
	// A numeric term is a bonus, not a filter: it adds candidates and weighs in the ranking, but it must not
	// become one of the distinct terms a fact has to hold, or facts that answer the question without repeating
	// the number would drop out.
	minMatched := p.MinMatched - len(numbers)
	if minMatched < 1 {
		minMatched = 1
	}
	if len(tickets) > 0 {
		minMatched = 1
	}
	if len(known) < minMatched {
		minMatched = len(known)
	}
	today := p.Today
	if today == "" {
		today = store.Today()
	}
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
		cov := got / sum
		if matched < minMatched {
			continue
		}
		if cov < p.MinCoverage {
			if p.MinContained > 0 {
				short = append(short, Hit{Fact: f.Fact, Coverage: cov, Matched: matched,
					Provisional: f.Review == store.Provisional, bm25: f.BM25})
			}
			continue
		}
		hits = append(hits, Hit{Fact: f.Fact, Coverage: cov, Matched: matched,
			Provisional: f.Review == store.Provisional, bm25: f.BM25})
	}
	if len(short) == 0 {
		return hits, nil
	}
	contained, err := containedIn(s, prompt, short, weight, total, p.MinContained)
	if err != nil {
		return nil, err
	}
	// Search order is BM25 order, and the facts kept by containment take their place in it.
	return slices.SortedStableFunc(slices.Values(append(hits, contained...)), func(a, b Hit) int {
		return cmp.Compare(a.bm25, b.bm25)
	}), nil
}

// containedIn keeps the candidates whose own terms, those of their text and keywords, the prompt covers for at least
// minShare of their IDF weight. weight holds the IDF of the prompt's terms and grows by the candidates' others.
func containedIn(s *store.Store, prompt string, candidates []Hit, weight map[string]float64, total int,
	minShare float64) ([]Hit, error) {
	stems := text.Stems(prompt)
	own := make([][]string, len(candidates))
	var unknown []string
	for i, h := range candidates {
		own[i] = text.Terms(h.Content + " " + strings.Join(h.Keywords, " "))
		for _, t := range own[i] {
			if _, ok := weight[t]; !ok && !slices.Contains(unknown, t) {
				unknown = append(unknown, t)
			}
		}
	}
	if len(unknown) > 0 {
		df, _, err := s.DocFreq(unknown)
		if err != nil {
			return nil, err
		}
		for _, t := range unknown {
			if df[t] > 0 {
				weight[t] = math.Log(1 + float64(total)/float64(df[t]))
			}
		}
	}
	var out []Hit
	for i, h := range candidates {
		var got, sum float64
		for _, t := range own[i] {
			sum += weight[t]
			if slices.ContainsFunc(stems, func(st string) bool { return text.Covers(t, st) }) {
				got += weight[t]
			}
		}
		if sum > 0 && got/sum >= minShare {
			out = append(out, h)
		}
	}
	return out, nil
}

// hostScope reports whether scope names this host.
func hostScope(scope, host string) bool {
	return host != "" && strings.EqualFold(scope, "host:"+host)
}
