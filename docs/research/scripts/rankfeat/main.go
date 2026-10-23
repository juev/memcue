//go:build research

// Command rankfeat writes, for every prompt of a judgecand candidates file, the numbers a local ranker could read about
// each of the prompt's judge candidates: the words' own signals (#316). The numbers are the ones recall hands the local
// reader (#321), taken through the same path. It stops if the candidates differ from the file's, so the features
// belong to the lists the readers were scored on. Experiment only, not part of memcue.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/juev/memcue/internal/recall"
	"github.com/juev/memcue/internal/rerank"
	"github.com/juev/memcue/internal/store"
)

type in struct {
	ID         string `json:"id"`
	Text       string `json:"text"`
	Candidates []struct {
		ID string `json:"id"`
	} `json:"candidates"`
}

type out struct {
	ID string `json:"id"`
	recall.PromptFeatures
	Features []recall.Features `json:"features"` // in the candidates' order
	Words    []string          `json:"words"`    // what the words alone serve: the answer recall falls back to
}

// recorder takes the features RecallJudge would score and refuses to answer, so recall falls back to the words.
type recorder struct {
	prompt   recall.PromptFeatures
	features []recall.Features
}

func (r *recorder) Scores(context.Context, string, []string) ([]float64, error) {
	return nil, errors.New("features only")
}
func (r *recorder) ScoreFeatures(_ context.Context, p recall.PromptFeatures, fs []recall.Features) ([]float64, error) {
	r.prompt, r.features = p, fs
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
		rec := &recorder{}
		words, err := recall.RecallJudge(s, row.Text, "", p, rec)
		if err != nil {
			panic(err)
		}
		var ids, want []string
		for _, f := range rec.features {
			ids = append(ids, f.ID)
		}
		for _, c := range row.Candidates {
			want = append(want, c.ID)
		}
		if !slices.Equal(ids, want) {
			panic(fmt.Sprintf("%s: candidates differ from the file\n%v\n%v", row.ID, ids, want))
		}
		o := out{ID: row.ID, PromptFeatures: rec.prompt, Features: rec.features}
		for _, h := range words {
			o.Words = append(o.Words, h.ID)
		}
		if err := enc.Encode(o); err != nil {
			panic(err)
		}
	}
	if err := sc.Err(); err != nil {
		panic(err)
	}
}
