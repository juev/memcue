// Package recall decides which facts a prompt should bring into the agent's context.
//
// Candidates come from FTS5 over all facts, in BM25 order. A fact passes when it holds enough of the prompt's
// informative terms: a minimum number of them and a minimum IDF-weighted share. The facts that pass are served in
// BM25 order: the share gates, it does not rank (#338). In a session of a repository, a fact scoped to another
// repository or a directory must hold a larger share to pass (ForeignCoverage, #345), or is not searched at all
// (FilterForeign); facts learnt in the session's repository win ties.
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
	// ForeignCoverage is the share a fact scoped to another project (repo: or dir: that is not one of the session's
	// repository keys) must hold; one at or below MinCoverage changes nothing. A session with no repository key, such
	// as the Claude config directory or a directory outside git, has no other project.
	ForeignCoverage float64
	FilterForeign   bool            // facts of another project are not searched at all
	Host            string          // this machine: facts whose scope is host:<Host> win ties; empty means no preference
	Seen            map[string]bool // facts the session already got: they give way to the next ones
	Today           string          // local "YYYY-MM-DD" the expired facts are judged by; empty means store.Today()
	Trace           *Trace          // records the decision when set; nil records nothing
}

// Default holds the values chosen on the dev set of facts (2026-10-19, 2335 facts migrated from the notes, 180 real
// prompts): 48 prompts got facts, 77 facts, 19 useful, 40 noise; 26 of 30 recall questions found a fact of their
// note. Three matched terms cut the noise; coverage above 0.25 starts losing facts of other repositories.
// ForeignCoverage comes from the replay of #345 on 432 Claude Code prompts where the place changes what is served,
// checked by hand where it changes whether a needed fact is served: 0.25 for facts of other projects cut the noise
// facts by 19% and lost a needed fact in 3 prompts; the hard filter cut 37% and lost about 20
// (docs/research/scope-filter-2026-10.md).
var Default = Params{
	Candidates:      80,
	MinTerms:        2,
	MinMatched:      3,
	MinCoverage:     0.15,
	MaxPromptTerms:  24,
	MaxFacts:        2,
	ForeignCoverage: 0.25,
}

// Hit is a fact chosen for the prompt.
type Hit struct {
	store.Fact
	Coverage    float64
	Matched     int
	Displaced   bool   // a fact of another host took a slot while a fact of this host was left out
	Source      string // which reader proposed the fact: lexical
	Provisional bool   // auto-approved for a TTL, recalled after approved facts
	// Meaning is the judge score after rerank; 0 when the words alone decided.
	Meaning float64
	rank    int     // position in the search's BM25 order
	bm25    float64 // the search's BM25 score, lower is better
	gated   bool    // the term or coverage gate turned the fact away
}

// Recall returns the facts to inject for prompt in a session whose project has the keys of project (tier.Keys; none
// outside a project).
func Recall(s *store.Store, prompt string, project []string, p Params) ([]Hit, error) {
	hits, _, err := recallCandidates(s, prompt, project, p)
	if err != nil {
		return nil, err
	}
	// BM25 order, approved before provisional. The coverage sort that stood here served a useful fact to fewer
	// prompts with more noise: on 409 blind-labelled Claude Code prompts where the two orders differ, 180 against 205
	// with 41% noise against 30% (docs/research/search-chain-2026-10.md). Ties of equal BM25, such as the same text
	// stored twice: a fact of this host, then one learnt in the session's repository, then a global one, then the rest;
	// then the newer fact. The host only orders tied facts; it never excludes another host's fact.
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Provisional != hits[j].Provisional {
			return !hits[i].Provisional
		}
		if hits[i].bm25 != hits[j].bm25 {
			return hits[i].bm25 < hits[j].bm25
		}
		if pi, pj := place(hits[i], project, p.Host), place(hits[j], project, p.Host); pi != pj {
			return pi < pj
		}
		return newer(hits[i], hits[j])
	})
	served := hits
	if maxFacts := capFacts(p.MaxFacts, len(hits)); len(hits) > maxFacts {
		served = hits[:maxFacts]
	}
	p.Trace.final(hits, len(served))
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

// place ranks a fact for a tie of equal BM25 score or judge score: this host first, then the session's repository,
// then a global origin, then everything else. A fact was learnt in the session's project when its origin starts with
// any of the project's keys: a mirror's other host or a fork's upstream counts. The host only orders facts; it never
// excludes another host's fact.
func place(h Hit, project []string, host string) int {
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
func newer(a, b Hit) bool {
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
// MaxFacts cap. Recall adds the tie order and the cap; the rerank pass uses the same gates but lets the judge order.
// gated are the facts the term and coverage gates turned away, in Search order: a judge may still read them.
func recallCandidates(s *store.Store, prompt string, project []string, p Params) (hits, gated []Hit, err error) {
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
	if p.Trace != nil {
		p.Trace.Stopped, p.Trace.Terms, p.Trace.Lexical = "", nil, nil
	}
	if len(terms) < minTerms {
		p.Trace.stop("the prompt has too few terms")
		return nil, nil, nil
	}
	df, total, err := s.DocFreq(terms)
	if err != nil {
		return nil, nil, err
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
		p.Trace.stop("too few prompt terms occur in any fact")
		return nil, nil, nil
	}
	if p.MaxPromptTerms > 0 && len(known) > p.MaxPromptTerms {
		sort.SliceStable(known, func(i, j int) bool { return weight[known[i]] > weight[known[j]] })
		known = known[:p.MaxPromptTerms]
	}
	var sum float64
	for _, t := range known {
		sum += weight[t]
	}
	if p.Trace != nil {
		for _, t := range known {
			p.Trace.Terms = append(p.Trace.Terms, Term{Term: t, Weight: weight[t]})
		}
	}

	own := ownScopes(project)
	var places []string
	if own != nil && p.FilterForeign {
		places = own
	}
	found, err := s.SearchIn(known, p.Candidates, places)
	if err != nil {
		return nil, nil, err
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
	if p.Trace != nil {
		p.Trace.MinMatched, p.Trace.MinCoverage = minMatched, p.MinCoverage
	}
	today := p.Today
	if today == "" {
		today = store.Today()
	}
	for rank, f := range found {
		if p.Seen[f.ID] {
			p.Trace.lexical(f.Fact, nil, 0, VerdictSeen)
			continue
		}
		if store.Expired(f.Expires, today) {
			p.Trace.lexical(f.Fact, nil, 0, VerdictExpired)
			continue
		}
		// A prompt that names a ticket must not recall another ticket through a shared project word: PROJ-1235
		// and PROJ-1234 can share "proj" in a keyword, but only the exact key is the subject of the prompt.
		if len(tickets) > 0 && !slices.ContainsFunc(f.Stems, func(st string) bool { return slices.Contains(tickets, st) }) {
			p.Trace.lexical(f.Fact, nil, 0, VerdictOtherTicket)
			continue
		}
		var got float64
		matched := 0
		var held []string
		for _, t := range known {
			if slices.ContainsFunc(f.Stems, func(st string) bool { return text.Covers(t, st) }) {
				got += weight[t]
				matched++
				if p.Trace != nil {
					held = append(held, t)
				}
			}
		}
		cov := got / sum
		h := Hit{Fact: f.Fact, Coverage: cov, Matched: matched, Source: "lexical",
			Provisional: f.Review == store.Provisional, rank: rank, bm25: f.BM25}
		switch {
		case matched < minMatched:
			p.Trace.lexical(f.Fact, held, cov, VerdictFewTerms)
			h.gated = true
			gated = append(gated, h)
		case cov < p.MinCoverage:
			p.Trace.lexical(f.Fact, held, cov, VerdictLowCoverage)
			h.gated = true
			gated = append(gated, h)
		case cov < p.ForeignCoverage && foreign(f.Scope, own):
			p.Trace.lexical(f.Fact, held, cov, VerdictForeign)
			h.gated = true
			gated = append(gated, h)
		default:
			p.Trace.lexical(f.Fact, held, cov, VerdictPassed)
			hits = append(hits, h)
		}
	}
	return hits, gated, nil
}

// ownScopes are the scopes of the session's repository keys, tier.Scope of each; nil when the session has none, so
// that no fact counts as another project's: the Claude config directory (global), a directory outside git (dir:,
// every Hermes session since #343, whose project is #347's to decide) and a session with no key.
func ownScopes(project []string) []string {
	var out []string
	for _, k := range project {
		if k != "" && k != tier.Global && !strings.HasPrefix(k, tier.DirPrefix) {
			out = append(out, tier.Scope(k))
		}
	}
	return out
}

// foreign reports whether scope ties a fact to a project other than the session's: a repository or a directory that
// is none of own. Global, host:, service: and empty scopes hold from any repository; so does everything when own is
// nil.
func foreign(scope string, own []string) bool {
	if own == nil {
		return false
	}
	sc := strings.ToLower(scope)
	if !strings.HasPrefix(sc, "repo:") && !strings.HasPrefix(sc, "dir:") {
		return false
	}
	return !slices.ContainsFunc(own, func(o string) bool { return strings.EqualFold(o, sc) })
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
