//go:build research

// Command chain measures the lexical search chain of #330 on a copy of a base: for every prompt it runs recall twice,
// with the shipped candidate limit and with none, and records what the limit cuts before the gates see it, which
// facts FTS5 returns that the gate's own term test does not hold, and what each step costs. One JSON line per prompt
// goes to stdout, the totals to stderr. Experiment only, not part of memcue.
//
// usage: go run -tags research ./docs/research/scripts/chain FACTS.db eval.json > chain.jsonl
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/juev/memcue/internal/recall"
	"github.com/juev/memcue/internal/store"
	"github.com/juev/memcue/internal/text"
)

// unlimited stands for "no limit": larger than any base this runs on.
const unlimited = 1 << 30

type lost struct {
	ID       string  `json:"id"`
	Rank     int     `json:"rank"` // position in the unlimited BM25 order
	Coverage float64 `json:"coverage"`
	// CovRank is the position among all passing facts by coverage, best first: how soon a later prompt of the
	// session, which skips the facts already served, would reach it.
	CovRank int    `json:"cov_rank"`
	Content string `json:"content"`
}

type row struct {
	ID         string   `json:"id"`
	Text       string   `json:"text"`
	Stopped    string   `json:"stopped,omitempty"`
	Terms      int      `json:"terms"`       // prompt terms the facts know
	Pool       int      `json:"pool"`        // facts FTS5 returns with no limit
	Passed     int      `json:"passed"`      // facts the gates pass with the shipped limit
	PassedAll  int      `json:"passed_all"`  // with no limit
	Lost       []lost   `json:"lost"`        // passed with no limit, beyond the shipped limit
	MaxRank    int      `json:"max_rank"`    // deepest BM25 rank of a fact that passes, -1 when none does
	Served     []string `json:"served"`      // what the prompt gets with the shipped limit
	ServedRank []int    `json:"served_rank"` // BM25 rank of each served fact
	BM25Top    []string `json:"bm25_top"`    // the first passing facts in BM25 order, as many as were served
	ServedAll  []string `json:"served_all"`  // with no limit
	NoTerm     int      `json:"no_term"`     // facts FTS5 returned whose stems the gate finds no prompt term in
	DocFreqMs  float64  `json:"docfreq_ms"`  // the per-term document counts
	SearchMs   float64  `json:"search_ms"`   // one Search with the shipped limit
	RecallMs   float64  `json:"recall_ms"`   // the whole recall with the shipped limit
	SearchAllM float64  `json:"search_all_ms"`
}

func main() {
	s, err := store.Open(os.Args[1])
	if err != nil {
		panic(err)
	}
	defer func() { _ = s.Close() }()
	b, err := os.ReadFile(os.Args[2])
	if err != nil {
		panic(err)
	}
	var in struct {
		Prompts []struct {
			ID   string `json:"id"`
			Text string `json:"text"`
		} `json:"prompts"`
	}
	if err := json.Unmarshal(b, &in); err != nil {
		panic(err)
	}
	enc := json.NewEncoder(os.Stdout)
	var prompts, searched, withLost, servedDiff, withNoTerm, lostFacts int
	var docfreq, search, total float64
	maxRank := -1
	for _, p := range in.Prompts {
		r := run(s, p.ID, p.Text)
		prompts++
		if r.Stopped == "" {
			searched++
			docfreq += r.DocFreqMs
			search += r.SearchMs
			total += r.RecallMs
		}
		if len(r.Lost) > 0 {
			withLost++
			lostFacts += len(r.Lost)
		}
		if !slices.Equal(r.Served, r.ServedAll) {
			servedDiff++
		}
		if r.NoTerm > 0 {
			withNoTerm++
		}
		maxRank = max(maxRank, r.MaxRank)
		if err := enc.Encode(r); err != nil {
			panic(err)
		}
	}
	fmt.Fprintf(os.Stderr, "prompts %d, searched %d\n", prompts, searched)
	fmt.Fprintf(os.Stderr, "lost to the limit: %d facts in %d prompts; served set changed in %d prompts\n",
		lostFacts, withLost, servedDiff)
	fmt.Fprintf(os.Stderr, "deepest rank of a passing fact: %d\n", maxRank)
	fmt.Fprintf(os.Stderr, "prompts with facts FTS5 returned and the gate holds no term of: %d\n", withNoTerm)
	if searched > 0 {
		fmt.Fprintf(os.Stderr, "mean per searched prompt: docfreq %.2f ms, search %.2f ms, recall %.2f ms\n",
			docfreq/float64(searched), search/float64(searched), total/float64(searched))
	}
}

func run(s *store.Store, id, prompt string) row {
	r := row{ID: id, Text: prompt, MaxRank: -1, Lost: []lost{}}
	p := recall.Default
	p.Today = "2026-10-23" // the day the base was archived, so expiries read as they did then
	tr := &recall.Trace{}
	p.Trace = tr
	start := time.Now()
	hits, err := recall.Recall(s, prompt, "", p)
	r.RecallMs = ms(time.Since(start))
	if err != nil {
		panic(err)
	}
	r.Stopped = tr.Stopped
	r.Served = ids(hits)
	passed := map[string]bool{}
	for _, st := range tr.Lexical {
		if st.Verdict == recall.VerdictPassed {
			passed[st.ID] = true
		}
	}
	r.Passed = len(passed)
	for _, h := range hits {
		r.ServedRank = append(r.ServedRank, slices.IndexFunc(tr.Lexical, func(st recall.Step) bool { return st.ID == h.ID }))
	}
	for _, st := range tr.Lexical {
		if st.Verdict == recall.VerdictPassed && len(r.BM25Top) < len(hits) {
			r.BM25Top = append(r.BM25Top, st.ID)
		}
	}
	if r.Stopped != "" {
		r.ServedAll = r.Served
		return r
	}
	known := make([]string, len(tr.Terms))
	for i, t := range tr.Terms {
		known[i] = t.Term
	}
	r.Terms = len(known)

	start = time.Now()
	if _, _, err := s.DocFreq(text.Terms(prompt)); err != nil {
		panic(err)
	}
	r.DocFreqMs = ms(time.Since(start))
	start = time.Now()
	if _, err := s.Search(known, p.Candidates); err != nil {
		panic(err)
	}
	r.SearchMs = ms(time.Since(start))
	start = time.Now()
	if _, err := s.Search(known, unlimited); err != nil {
		panic(err)
	}
	r.SearchAllM = ms(time.Since(start))

	all := p
	all.Candidates = unlimited
	trAll := &recall.Trace{}
	all.Trace = trAll
	hitsAll, err := recall.Recall(s, prompt, "", all)
	if err != nil {
		panic(err)
	}
	r.ServedAll = ids(hitsAll)
	r.Pool = len(trAll.Lexical)
	for rank, st := range trAll.Lexical {
		if len(st.Matched) == 0 && st.Verdict != recall.VerdictSeen && st.Verdict != recall.VerdictExpired &&
			st.Verdict != recall.VerdictOtherTicket {
			r.NoTerm++
		}
		if st.Verdict != recall.VerdictPassed {
			continue
		}
		r.PassedAll++
		r.MaxRank = max(r.MaxRank, rank)
		if !passed[st.ID] {
			r.Lost = append(r.Lost, lost{ID: st.ID, Rank: rank, Coverage: st.Coverage, Content: st.Content})
		}
	}
	covs := []float64{}
	for _, st := range trAll.Lexical {
		if st.Verdict == recall.VerdictPassed {
			covs = append(covs, st.Coverage)
		}
	}
	for i := range r.Lost {
		for _, c := range covs {
			if c > r.Lost[i].Coverage {
				r.Lost[i].CovRank++
			}
		}
	}
	return r
}

func ids(hits []recall.Hit) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.ID
	}
	return out
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }
