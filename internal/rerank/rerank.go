// Package rerank asks a typed decisions model (Jev) which of the facts the words found would actually help answer the
// prompt. It is the cheap half of the earlier idea: the candidates come from FTS5, so nothing has to be embedded, and
// one request carries ten facts instead of the whole base.
//
// Off unless MEMCUE_JUDGE_URL, _MODEL and _KEY are set; recall then keeps its lexical answer whenever
// the call fails, times out or finds nothing above the threshold.
package rerank

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/juev/memcue/internal/decisions"
	"github.com/juev/memcue/internal/judge"
)

// Defaults measured on 120 real prompts: ten candidates, a 0.5 threshold, and a call that took 0.7 s at the median.
const (
	DefaultModel      = "typesafe/jev-1.14"
	DefaultTimeout    = 2 * time.Second
	DefaultMin        = 0.5
	DefaultCandidates = 10
	PauseFirst        = judge.PauseFirst
	PauseMax          = judge.PauseMax
)

// ErrPaused is returned, without a call, while a failed endpoint is left alone.
var ErrPaused = judge.ErrPaused

// Settings describe the endpoint. A nil *Settings means the rerank pass is off.
type Settings struct {
	URL        string
	Model      string
	Key        string
	Timeout    time.Duration
	Min        float64
	Candidates int
	StateFile  string         // where the pause is remembered between processes
	Breaker    *judge.Breaker // shared with triage and duplicate; nil keeps a private one
}

// FromEnv reads the one judge endpoint and the rerank knobs: MEMCUE_JUDGE_URL, _MODEL and _KEY, then
// MEMCUE_JUDGE_RERANK_TIMEOUT, _MIN and _CANDIDATES. It returns nil when the judge is not fully configured.
func FromEnv() *Settings {
	raw := strings.TrimSpace(os.Getenv("MEMCUE_JUDGE_URL"))
	model := strings.TrimSpace(os.Getenv("MEMCUE_JUDGE_MODEL"))
	key := strings.TrimSpace(os.Getenv("MEMCUE_JUDGE_KEY"))
	if raw == "" || model == "" || key == "" {
		return nil
	}
	s := &Settings{
		URL:        raw,
		Model:      model,
		Key:        key,
		Timeout:    DefaultTimeout,
		Min:        DefaultMin,
		Candidates: DefaultCandidates,
	}
	if v := strings.TrimSpace(os.Getenv("MEMCUE_JUDGE_RERANK_MIN")); v != "" {
		if f, err := parseFloat(v); err == nil && f > 0 && f <= 1 {
			s.Min = f
		}
	}
	if v := strings.TrimSpace(os.Getenv("MEMCUE_JUDGE_RERANK_CANDIDATES")); v != "" {
		if n, err := parseInt(v); err == nil && n >= 2 && n <= 50 {
			s.Candidates = n
		}
	}
	if v := strings.TrimSpace(os.Getenv("MEMCUE_JUDGE_RERANK_TIMEOUT")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d >= 200*time.Millisecond && d <= 10*time.Second {
			s.Timeout = d
		}
	}
	return s
}

// Ranker is one endpoint plus the pause that keeps a dead one from costing a timeout on every turn.
type Ranker struct {
	set     *Settings
	client  decisions.Client
	breaker *judge.Breaker
}

// New builds a ranker for the settings.
func New(s *Settings) *Ranker {
	breaker := s.Breaker
	if breaker == nil {
		breaker = judge.New(s.StateFile)
	}
	return &Ranker{
		set:     s,
		breaker: breaker,
		client: decisions.Client{
			URL:   s.URL,
			Model: s.Model,
			Key:   s.Key,
			HTTP:  &http.Client{Timeout: s.Timeout},
		},
	}
}

// Min is the probability below which a fact is not worth a slot.
func (r *Ranker) Min() float64 { return r.set.Min }

// Candidates is how many facts FTS5 should hand over.
func (r *Ranker) Candidates() int { return r.set.Candidates }

// Paused reports whether the endpoint is being left alone, and for how much longer.
func (r *Ranker) Paused() (bool, time.Duration) { return r.breaker.Paused() }

// Scores asks one yes/no question per fact: would it help answer the prompt. The answer is aligned with facts.
func (r *Ranker) Scores(ctx context.Context, prompt string, facts []string) ([]float64, error) {
	if len(facts) == 0 {
		return nil, nil
	}
	if err := r.breaker.Check(); err != nil {
		return nil, err
	}

	state := append([]string{"Prompt: " + prompt, "Candidate facts:"},
		numbered(facts)...)
	questions := make(map[string]decisions.Question, len(facts))
	for i := range facts {
		questions[fmt.Sprintf("f%d", i+1)] = decisions.Question{
			Type:         "noul",
			Instructions: fmt.Sprintf("Would fact [%d] help answer the prompt?", i+1),
		}
	}
	ctx, cancel := context.WithTimeout(ctx, r.set.Timeout)
	defer cancel()
	result, err := r.client.Ask(ctx, state, questions)
	if err != nil {
		return nil, r.breaker.Fail(err)
	}
	out := make([]float64, len(facts))
	for i := range facts {
		answer, ok := result.Answers[fmt.Sprintf("f%d", i+1)]
		if !ok {
			return nil, r.breaker.Fail(fmt.Errorf("no answer for fact %d", i+1))
		}
		out[i] = answer.Noul
	}
	r.breaker.OK()
	return out, nil
}

func numbered(facts []string) []string {
	out := make([]string, len(facts))
	for i, f := range facts {
		out[i] = fmt.Sprintf("[%d] %s", i+1, oneLine(f))
	}
	return out
}

// oneLine keeps a fact on one line: the numbered state is read as a list of entries.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func parseFloat(s string) (float64, error) { return strconv.ParseFloat(s, 64) }

func parseInt(s string) (int, error) { return strconv.Atoi(s) }
