//go:build research

// Command candidates writes, for every active fact, its neighbours found by memcue recall with a wide net: the candidate
// pairs a pairwise triage would judge. Experiment only, not part of memcue.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/juev/memcue/internal/recall"
	"github.com/juev/memcue/internal/store"
)

type fact struct {
	ID      string `json:"id"`
	Written string `json:"written"`
	Origin  string `json:"origin"`
	Text    string `json:"text"`
}

type row struct {
	Fact       fact      `json:"fact"`
	Candidates []fact    `json:"candidates"`
	Coverage   []float64 `json:"coverage"`
}

func main() {
	s, err := store.Open(os.Args[1])
	if err != nil {
		panic(err)
	}
	defer func() { _ = s.Close() }()
	active, err := s.Active()
	if err != nil {
		panic(err)
	}
	// usage: go run -tags research ./docs/research/scripts/candidates FACTS.db MIN_COVERAGE MAX_FACTS
	minCov, err := strconv.ParseFloat(os.Args[2], 64)
	if err != nil {
		panic(err)
	}
	maxFacts, err := strconv.Atoi(os.Args[3])
	if err != nil {
		panic(err)
	}
	p := recall.Params{Candidates: 80, MinTerms: 2, MinMatched: 2, MinCoverage: minCov, MaxPromptTerms: 24, MaxFacts: maxFacts}
	conv := func(f store.Fact) fact {
		return fact{f.ID, f.Created.Format(time.DateOnly), f.Origin, strings.Join(strings.Fields(f.Content), " ")}
	}
	enc := json.NewEncoder(os.Stdout)
	for _, f := range active {
		q := p
		q.Seen = map[string]bool{f.ID: true}
		hits, err := recall.Recall(s, f.Content+" "+strings.Join(f.Keywords, " "), "", q)
		if err != nil {
			panic(err)
		}
		r := row{Fact: conv(f)}
		for _, h := range hits {
			if h.ID == f.ID {
				continue
			}
			r.Candidates = append(r.Candidates, conv(h.Fact))
			r.Coverage = append(r.Coverage, h.Coverage)
		}
		if err := enc.Encode(r); err != nil {
			panic(err)
		}
	}
	fmt.Fprintln(os.Stderr, "facts", len(active))
}
