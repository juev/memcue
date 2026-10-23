package recall

import (
	"context"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/juev/memcue/internal/store"
	"github.com/juev/memcue/internal/text"
)

// FeatureRanker is a Ranker that reads the words' own signals about each candidate instead of its text: the local
// tree reader (#321). RecallJudge hands it the features in place of calling Scores.
type FeatureRanker interface {
	Ranker
	ScoreFeatures(ctx context.Context, prompt PromptFeatures, candidates []Features) ([]float64, error)
}

// PromptFeatures are the numbers about the prompt a local reader sees, as the gates weighed it.
type PromptFeatures struct {
	Terms   int     `json:"terms"`   // prompt terms
	Known   int     `json:"known"`   // prompt terms some fact holds, as the gate weighs them
	Runes   int     `json:"runes"`   // prompt length
	Tickets int     `json:"tickets"` // ticket keys in the prompt
	Weights float64 `json:"weights"` // sum of the known terms' weights
}

// Features are the numbers about one candidate a local reader sees.
type Features struct {
	ID          string  `json:"id"`
	Rank        int     `json:"rank"`   // position among the candidates, in BM25 order
	Passed      bool    `json:"passed"` // the term and coverage gate let it through
	Coverage    float64 `json:"coverage"`
	Matched     int     `json:"matched"`
	MatchedMax  float64 `json:"matched_max"` // weight of the rarest matched term over the rarest prompt term
	ContentCov  float64 `json:"content_cov"` // coverage counted on the content alone
	KeywordCov  float64 `json:"keyword_cov"` // on the keywords alone
	QueryCov    float64 `json:"query_cov"`   // on the queries alone
	Ticket      bool    `json:"ticket"`      // the fact holds a ticket key of the prompt
	Runes       int     `json:"runes"`       // length of the content
	Keywords    int     `json:"keywords"`    // number of keywords
	Queries     int     `json:"queries"`     // number of queries
	Kind        string  `json:"kind"`        // rule, decision, pitfall, environment or empty
	Scope       string  `json:"scope"`       // global, host, repo or empty
	Source      string  `json:"source"`      // user, agent, note, ...
	Provisional bool    `json:"provisional"` // auto-approved for a TTL
	AgeDays     float64 `json:"age_days"`    // days between the fact's version and the newest fact of the base
	Expires     bool    `json:"expires"`     // the fact has an expiry date
	Versions    int     `json:"versions"`    // the fact's version number: how many times it changed, plus one
}

// query is the prompt as the gates weighed it.
type query struct {
	prompt  string
	terms   int // all prompt terms, known or not
	known   []string
	weight  map[string]float64
	sum     float64
	tickets []string
}

// features describes the candidates for a FeatureRanker. newest is the creation time of the newest fact version in
// the base: the age of a fact is counted from it, so a base that has not grown for a while does not age all at once.
func features(q query, candidates []Hit, newest time.Time) (PromptFeatures, []Features) {
	p := PromptFeatures{Terms: q.terms, Known: len(q.known), Runes: utf8.RuneCountInString(q.prompt),
		Tickets: len(q.tickets), Weights: q.sum}
	var rarest float64
	for _, t := range q.known {
		rarest = max(rarest, q.weight[t])
	}
	out := make([]Features, len(candidates))
	for i, h := range candidates {
		f := Features{ID: h.ID, Rank: i, Passed: !h.gated, Coverage: h.Coverage, Matched: h.Matched,
			Runes: utf8.RuneCountInString(h.Content), Keywords: len(h.Keywords), Queries: len(h.Queries),
			Kind: h.Kind, Scope: scopeKind(h.Scope), Source: h.Meta.Source, Provisional: h.Review == store.Provisional,
			AgeDays: newest.Sub(h.Created).Hours() / 24, Expires: h.Expires != "", Versions: h.Version}
		for _, t := range h.held {
			f.MatchedMax = max(f.MatchedMax, q.weight[t])
		}
		if rarest > 0 {
			f.MatchedMax /= rarest
		}
		content := text.Stems(h.Content)
		keywords := text.Stems(strings.Join(h.Keywords, " "))
		queries := text.Stems(strings.Join(h.Queries, " "))
		f.ContentCov = q.cover(content)
		f.KeywordCov = q.cover(keywords)
		f.QueryCov = q.cover(queries)
		all := slices.Concat(content, keywords, queries)
		f.Ticket = slices.ContainsFunc(q.tickets, func(t string) bool { return slices.Contains(all, t) })
		out[i] = f
	}
	return p, out
}

// cover is the IDF-weighted share of the prompt's known terms the stems hold, as the coverage gate counts it.
func (q query) cover(stems []string) float64 {
	if q.sum == 0 {
		return 0
	}
	var got float64
	for _, t := range q.known {
		if slices.ContainsFunc(stems, func(st string) bool { return text.Covers(t, st) }) {
			got += q.weight[t]
		}
	}
	return got / q.sum
}

// scopeKind is the kind of a scope without its name: host:loffe is host.
func scopeKind(scope string) string {
	kind, _, _ := strings.Cut(scope, ":")
	return kind
}
