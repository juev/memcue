// Package localrank is the local reader of recall (#321): gradient-boosted trees over the words' own signals about each
// candidate, trained to predict Jev's probability. It makes no remote call and answers in microseconds. It runs only
// when MEMCUE_RERANK=local names it: the trees learnt one large base, and on a small fresh one (no queries, few facts)
// they score facts below the threshold that the words would serve.
//
// On the gold set (docs/research/local-readers-2026-10.md) it serves a useful fact to as many prompts as the words
// with the coverage gate and half their noise; Jev serves twice as many. The model is trained by
// docs/research/scripts/local_train.py on the prompt pool of #320, never on the gold set.
package localrank

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/dmitryikh/leaves"

	"github.com/juev/memcue/internal/recall"
)

const (
	// Min is the score a fact needs: Jev's own threshold, which the model learnt to predict. On the gold set it served
	// 47 prompts a useful fact first and 30 noise facts; 0.34, the threshold that agreed best with Jev on the pool,
	// served 56 and 96, more noise than the words.
	Min = 0.5
	// Candidates is how many facts in BM25 order the reader scores: the list length of the training data.
	Candidates = 20
)

//go:embed model.txt
var modelText []byte

var (
	kinds   = []string{"rule", "decision", "pitfall", "environment"}
	scopes  = []string{"global", "host", "repo"}
	sources = []string{"user", "user_confirmed", "tool", "agent", "note", "memory"}
	// nFeatures is the length of the vector; the model must expect exactly this many.
	nFeatures = 17 + len(kinds) + len(scopes) + len(sources) + 7 + 1 + 5
)

// model loads the embedded trees once.
var model = sync.OnceValues(func() (*leaves.Ensemble, error) {
	m, err := leaves.LGEnsembleFromReader(bufio.NewReader(bytes.NewReader(modelText)), false)
	if err != nil {
		return nil, fmt.Errorf("localrank: load model: %w", err)
	}
	if m.NFeatures() != nFeatures {
		return nil, fmt.Errorf("localrank: model expects %d features, the reader builds %d", m.NFeatures(), nFeatures)
	}
	return m, nil
})

// Ranker is the local reader. Its zero value is ready to use.
type Ranker struct{}

// Min is the score a fact needs.
func (Ranker) Min() float64 { return Min }

// Candidates is how many facts the reader scores.
func (Ranker) Candidates() int { return Candidates }

// Scores refuses: the reader reads features, and recall hands it them through ScoreFeatures.
func (Ranker) Scores(context.Context, string, []string) ([]float64, error) {
	return nil, errors.New("localrank: the local reader scores features, not texts")
}

// ScoreFeatures scores each candidate on 0..1.
func (Ranker) ScoreFeatures(_ context.Context, p recall.PromptFeatures, fs []recall.Features) ([]float64, error) {
	m, err := model()
	if err != nil {
		return nil, err
	}
	out := make([]float64, len(fs))
	for i, v := range vectors(p, fs) {
		out[i] = min(1, max(0, m.PredictSingle(v, 0)))
	}
	return out, nil
}

// vectors builds the model's input for each candidate: the row_features of docs/research/scripts/local_train.py, in
// the same order. The candidate's own signals come first, then the list around it, then the prompt.
func vectors(p recall.PromptFeatures, fs []recall.Features) [][]float64 {
	n := len(fs)
	if n == 0 {
		return nil
	}
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool { return fs[order[i]].Coverage > fs[order[j]].Coverage })
	covRank := make([]int, n)
	for r, i := range order {
		covRank[i] = r
	}
	best, second := fs[order[0]].Coverage, 0.0
	if n > 1 {
		second = fs[order[1]].Coverage
	}
	passed := 0
	for _, f := range fs {
		if f.Passed {
			passed++
		}
	}
	out := make([][]float64, n)
	for i, f := range fs {
		v := make([]float64, 0, nFeatures)
		v = append(v, float64(f.Rank), float64(f.Rank)/float64(max(1, n)),
			b(f.Passed), f.Coverage, float64(f.Matched), f.MatchedMax,
			f.ContentCov, f.KeywordCov, f.QueryCov, b(f.Ticket),
			float64(f.Runes), float64(f.Keywords), float64(f.Queries), b(f.Provisional), f.AgeDays, b(f.Expires),
			float64(f.Versions))
		v = oneHot(v, f.Kind, kinds)
		v = oneHot(v, f.Scope, scopes)
		v = oneHot(v, f.Source, sources)
		overBest := 0.0
		if best != 0 {
			overBest = f.Coverage / best
		}
		v = append(v, f.Coverage-best, overBest, float64(covRank[i]),
			best, best-second, float64(passed), float64(n),
			float64(f.Matched)/float64(max(1, p.Known)),
			float64(p.Terms), float64(p.Known), float64(p.Runes), float64(p.Tickets), p.Weights)
		out[i] = v
	}
	return out
}

func b(x bool) float64 {
	if x {
		return 1
	}
	return 0
}

func oneHot(v []float64, value string, values []string) []float64 {
	for _, x := range values {
		v = append(v, b(value == x))
	}
	return v
}
