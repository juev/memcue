//go:build research

// Command rankfeat writes, for every prompt of a judgecand candidates file, the numbers a local ranker could read about
// each of the prompt's judge candidates: the words' own signals (#316). It recomputes the candidates on the same base
// and stops if they differ from the file's, so the features belong to the lists the readers were scored on.
// Experiment only, not part of memcue.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/juev/memcue/internal/recall"
	"github.com/juev/memcue/internal/rerank"
	"github.com/juev/memcue/internal/store"
	"github.com/juev/memcue/internal/text"
)

type in struct {
	ID         string `json:"id"`
	Text       string `json:"text"`
	Candidates []struct {
		ID string `json:"id"`
	} `json:"candidates"`
}

type feat struct {
	ID          string  `json:"id"`
	Rank        int     `json:"rank"`
	Passed      bool    `json:"passed"`
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
	Versions    int     `json:"versions"`    // how many times it was corrected, plus one
}

type out struct {
	ID       string   `json:"id"`
	Terms    int      `json:"terms"`    // prompt terms
	Known    int      `json:"known"`    // prompt terms some fact holds, as the gate weighs them
	Runes    int      `json:"runes"`    // prompt length
	Tickets  int      `json:"tickets"`  // ticket keys in the prompt
	Features []feat   `json:"features"` // in the candidates' order
	Weights  float64  `json:"weights"`  // sum of the known terms' weights
	Words    []string `json:"words"`    // what the words alone serve: the answer recall falls back to
}

// recorder takes the facts RecallJudge would send and refuses to answer, as in judgecand.
type recorder struct{ facts []string }

func (r *recorder) Scores(_ context.Context, _ string, facts []string) ([]float64, error) {
	r.facts = facts
	return nil, errors.New("recorded")
}
func (r *recorder) Min() float64    { return rerank.DefaultMin }
func (r *recorder) Candidates() int { return rerank.DefaultCandidates }

func main() {
	// usage: go run -tags research ./docs/research/scripts/rankfeat FACTS.db candidates.jsonl TODAY > features.jsonl
	s, err := store.OpenReadOnly(os.Args[1])
	if err != nil {
		panic(err)
	}
	defer func() { _ = s.Close() }()
	rows, err := s.Rows()
	if err != nil {
		panic(err)
	}
	byID := map[string]store.Fact{}
	versions := map[string]int{}
	// Two facts can hold the same content; judgecand maps content to the fact of the last row, and so does this.
	byContent := map[string]string{}
	var newest time.Time
	for _, f := range rows { // ordered by version: the latest wins
		byID[f.ID] = f
		byContent[f.Content] = f.ID
		versions[f.ID]++
		if f.Created.After(newest) {
			newest = f.Created
		}
	}
	p := recall.Default
	p.Today = os.Args[3]

	file, err := os.Open(os.Args[2])
	if err != nil {
		panic(err)
	}
	defer func() { _ = file.Close() }()
	sc := bufio.NewScanner(file)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	enc := json.NewEncoder(os.Stdout)
	for sc.Scan() {
		var row in
		if err := json.Unmarshal(sc.Bytes(), &row); err != nil {
			panic(err)
		}
		tr := &recall.Trace{}
		p.Trace = tr
		rec := &recorder{}
		words, err := recall.RecallJudge(s, row.Text, "", p, rec)
		if err != nil {
			panic(err)
		}
		var ids []string
		for _, c := range rec.facts {
			ids = append(ids, byContent[c])
		}
		var want []string
		for _, c := range row.Candidates {
			want = append(want, c.ID)
		}
		if !slices.Equal(ids, want) {
			panic(fmt.Sprintf("%s: candidates differ from the file\n%v\n%v", row.ID, ids, want))
		}

		weight := map[string]float64{}
		var sum, rarest float64
		for _, t := range tr.Terms {
			weight[t.Term] = t.Weight
			sum += t.Weight
			rarest = max(rarest, t.Weight)
		}
		terms := text.Terms(row.Text)
		var tickets []string
		for _, t := range terms {
			if text.IsTicket(t) {
				tickets = append(tickets, t)
			}
		}
		steps := map[string]recall.Step{}
		for _, st := range tr.Lexical {
			steps[st.ID] = st
		}
		o := out{ID: row.ID, Terms: len(terms), Known: len(tr.Terms), Runes: utf8.RuneCountInString(row.Text),
			Tickets: len(tickets), Weights: sum}
		for _, h := range words {
			o.Words = append(o.Words, h.ID)
		}
		for rank, id := range ids {
			f, st := byID[id], steps[id]
			ft := feat{ID: id, Rank: rank, Passed: st.Verdict == recall.VerdictPassed, Coverage: st.Coverage,
				Matched: len(st.Matched), Runes: utf8.RuneCountInString(f.Content), Keywords: len(f.Keywords),
				Queries: len(f.Queries), Kind: f.Kind, Scope: scopeKind(f.Scope), Source: f.Source,
				Provisional: f.Review == store.Provisional, AgeDays: newest.Sub(f.Created).Hours() / 24,
				Expires: f.Expires != "", Versions: versions[id]}
			for _, t := range st.Matched {
				ft.MatchedMax = max(ft.MatchedMax, weight[t])
			}
			if rarest > 0 {
				ft.MatchedMax /= rarest
			}
			stems := text.Stems(f.Content)
			ft.ContentCov = cover(tr.Terms, stems, sum)
			ft.KeywordCov = cover(tr.Terms, text.Stems(strings.Join(f.Keywords, " ")), sum)
			ft.QueryCov = cover(tr.Terms, text.Stems(strings.Join(f.Queries, " ")), sum)
			all := append(append(stems, text.Stems(strings.Join(f.Keywords, " "))...), text.Stems(strings.Join(f.Queries, " "))...)
			ft.Ticket = slices.ContainsFunc(tickets, func(t string) bool { return slices.Contains(all, t) })
			o.Features = append(o.Features, ft)
		}
		if err := enc.Encode(o); err != nil {
			panic(err)
		}
	}
	if err := sc.Err(); err != nil {
		panic(err)
	}
}

// cover is the IDF-weighted share of the prompt's terms the stems hold, as the coverage gate counts it.
func cover(terms []recall.Term, stems []string, sum float64) float64 {
	if sum == 0 {
		return 0
	}
	var got float64
	for _, t := range terms {
		if slices.ContainsFunc(stems, func(st string) bool { return text.Covers(t.Term, st) }) {
			got += t.Weight
		}
	}
	return got / sum
}

func scopeKind(scope string) string {
	kind, _, _ := strings.Cut(scope, ":")
	return kind
}
