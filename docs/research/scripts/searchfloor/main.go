//go:build research

// Command searchfloor writes, for every prompt, the top facts of memcue search with what a floor could be built on
// (#407): the BM25 score and its share of the best one, the IDF-weighted share of the prompt's terms the fact holds,
// and how many of the prompt's rare terms it holds. A floor is then chosen offline over these numbers.
//
//	go run -tags research ./docs/research/scripts/searchfloor FACTS.db PROMPTS.json N > hits.jsonl
//
// PROMPTS.json is a list of {"id": "...", "q": "...", "cwd": "..."}. Experiment only, not part of memcue.
package main

import (
	"encoding/json"
	"math"
	"os"
	"slices"
	"strconv"

	"github.com/juev/memcue/internal/recall"
	"github.com/juev/memcue/internal/store"
	"github.com/juev/memcue/internal/text"
	"github.com/juev/memcue/internal/tier"
)

type prompt struct {
	ID  string `json:"id"`
	Q   string `json:"q"`
	CWD string `json:"cwd"`
}

type hit struct {
	ID       string  `json:"id"`
	Content  string  `json:"content"`
	BM25     float64 `json:"bm25"`
	Ratio    float64 `json:"ratio"`    // BM25 / best BM25 of the prompt: 1 for the first fact
	Coverage float64 `json:"coverage"` // IDF-weighted share of the prompt's known terms the fact holds
	Matched  int     `json:"matched"`  // distinct prompt terms the fact holds
	Rare     int     `json:"rare"`     // of them, terms held by at most 1 % of the facts
}

type row struct {
	ID    string `json:"id"`
	Terms int    `json:"terms"` // known terms of the prompt
	Hits  []hit  `json:"hits"`
}

func main() {
	s, err := store.Open(os.Args[1])
	if err != nil {
		panic(err)
	}
	defer func() { _ = s.Close() }()
	var prompts []prompt
	b, err := os.ReadFile(os.Args[2])
	if err != nil {
		panic(err)
	}
	if err := json.Unmarshal(b, &prompts); err != nil {
		panic(err)
	}
	n, err := strconv.Atoi(os.Args[3])
	if err != nil {
		panic(err)
	}
	enc := json.NewEncoder(os.Stdout)
	for _, p := range prompts {
		terms := text.Terms(p.Q)
		df, total, err := s.DocFreq(terms)
		if err != nil {
			panic(err)
		}
		weight := map[string]float64{}
		var known []string
		var sum float64
		for _, t := range terms {
			if df[t] > 0 {
				weight[t] = math.Log(1 + float64(total)/float64(df[t]))
				known = append(known, t)
				sum += weight[t]
			}
		}
		hits, err := recall.Search(s, p.Q, tier.Keys(p.CWD, ""), "", n, 0)
		if err != nil {
			panic(err)
		}
		r := row{ID: p.ID, Terms: len(known)}
		for _, h := range hits {
			stems := h.Stems
			var got float64
			matched, rare := 0, 0
			for _, t := range known {
				if slices.ContainsFunc(stems, func(st string) bool { return text.Covers(t, st) }) {
					got += weight[t]
					matched++
					if float64(df[t]) <= 0.01*float64(total) {
						rare++
					}
				}
			}
			x := hit{ID: h.ID, Content: h.Content, BM25: h.BM25, Matched: matched, Rare: rare}
			if sum > 0 {
				x.Coverage = got / sum
			}
			if len(hits) > 0 && hits[0].BM25 != 0 {
				x.Ratio = h.BM25 / hits[0].BM25
			}
			r.Hits = append(r.Hits, x)
		}
		if err := enc.Encode(r); err != nil {
			panic(err)
		}
	}
}
