//go:build research

// Command judgecand writes, for every prompt of a gold set, the candidates recall hands to the judge (the first facts
// the words found, in BM25 order, as RecallJudge picks them) and the facts the words alone serve. A judge is then asked
// about the same lists outside memcue (judge_ab.py). Experiment only, not part of memcue.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/juev/memcue/internal/recall"
	"github.com/juev/memcue/internal/rerank"
	"github.com/juev/memcue/internal/store"
)

type candidate struct {
	ID          string `json:"id"`
	Content     string `json:"content"`
	Provisional bool   `json:"provisional"`
}

type row struct {
	ID         string      `json:"id"`
	Text       string      `json:"text"`
	Candidates []candidate `json:"candidates"`
	Lexical    []lexical   `json:"lexical"` // what the words alone serve: the answer recall falls back to
}

type lexical struct {
	ID       string  `json:"id"`
	Content  string  `json:"content"`
	Coverage float64 `json:"coverage"`
	Matched  int     `json:"matched"`
}

// recorder takes the facts RecallJudge would send and refuses to answer, so recall falls back without a call.
type recorder struct{ facts []string }

func (r *recorder) Scores(_ context.Context, _ string, facts []string) ([]float64, error) {
	r.facts = facts
	return nil, errors.New("recorded")
}
func (r *recorder) Min() float64    { return rerank.DefaultMin }
func (r *recorder) Candidates() int { return rerank.DefaultCandidates }

func main() {
	// usage: go run -tags research ./docs/research/scripts/judgecand FACTS.db eval.json > candidates.jsonl
	s, err := store.Open(os.Args[1])
	if err != nil {
		panic(err)
	}
	defer func() { _ = s.Close() }()
	b, err := os.ReadFile(os.Args[2])
	if err != nil {
		panic(err)
	}
	var set struct {
		Prompts []struct {
			ID   string `json:"id"`
			Tier string `json:"tier"`
			Text string `json:"text"`
		} `json:"prompts"`
	}
	if err := json.Unmarshal(b, &set); err != nil {
		panic(err)
	}
	// Recall serves provisional facts too, which Active leaves out; Rows is ordered by version, so the latest wins.
	rows, err := s.Rows()
	if err != nil {
		panic(err)
	}
	byContent := map[string]store.Fact{}
	for _, f := range rows {
		byContent[f.Content] = f
	}
	enc := json.NewEncoder(os.Stdout)
	for _, p := range set.Prompts {
		text := p.Text
		if r := []rune(text); len(r) > 4000 { // cmd/memcue maxPrompt
			text = string(r[:4000])
		}
		rec := &recorder{}
		words, err := recall.RecallJudge(s, text, p.Tier, recall.Default, rec)
		if err != nil {
			panic(err)
		}
		out := row{ID: p.ID, Text: text}
		for _, h := range words {
			out.Lexical = append(out.Lexical, lexical{h.ID, h.Content, h.Coverage, h.Matched})
		}
		for _, c := range rec.facts {
			f, ok := byContent[c]
			if !ok {
				panic(fmt.Sprintf("candidate not found among the facts: %.60s", strings.TrimSpace(c)))
			}
			out.Candidates = append(out.Candidates, candidate{f.ID, f.Content, f.Review == store.Provisional})
		}
		if err := enc.Encode(out); err != nil {
			panic(err)
		}
	}
}
