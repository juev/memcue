// Package rerank asks a typed decisions model (Jev) which of the facts the words found would actually help answer the
// prompt. It is the cheap half of the earlier idea: the candidates come from FTS5, so nothing has to be embedded, and
// one request carries ten facts instead of the whole base.
//
// Off unless MEMCUE_JUDGE_URL, _MODEL and _KEY are set; recall then keeps its lexical answer whenever
// the call fails, times out or finds nothing above the threshold.
package rerank

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/juev/memcue/internal/decisions"
)

// Defaults measured on 120 real prompts: ten candidates, a 0.5 threshold, and a call that took 0.7 s at the median.
const (
	DefaultModel      = "typesafe/jev-1.14"
	DefaultTimeout    = 2 * time.Second
	DefaultMin        = 0.5
	DefaultCandidates = 10
	PauseFirst        = 60 * time.Second
	PauseMax          = 15 * time.Minute
)

// ErrPaused is returned, without a call, while a failed endpoint is left alone.
var ErrPaused = errors.New("rerank endpoint is paused after a failure")

// Settings describe the endpoint. A nil *Settings means the rerank pass is off.
type Settings struct {
	URL        string
	Model      string
	Key        string
	Timeout    time.Duration
	Min        float64
	Candidates int
	StateFile  string // where the pause is remembered between processes
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
	set    *Settings
	client decisions.Client

	mu       sync.Mutex
	pausedTo time.Time
	pause    time.Duration
}

// New builds a ranker for the settings.
func New(s *Settings) *Ranker {
	return &Ranker{
		set: s,
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
func (r *Ranker) Paused() (bool, time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	left := r.left()
	if left <= 0 {
		r.pausedTo = time.Time{}
		return false, 0
	}
	return true, left
}

// Scores asks one yes/no question per fact: would it help answer the prompt. The answer is aligned with facts.
func (r *Ranker) Scores(ctx context.Context, prompt string, facts []string) ([]float64, error) {
	if len(facts) == 0 {
		return nil, nil
	}
	r.mu.Lock()
	if paused, left := r.pausedLocked(); paused {
		r.mu.Unlock()
		return nil, fmt.Errorf("%w: %s left", ErrPaused, left.Round(time.Second))
	}
	r.mu.Unlock()

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
		return nil, r.fail(err)
	}
	out := make([]float64, len(facts))
	for i := range facts {
		answer, ok := result.Answers[fmt.Sprintf("f%d", i+1)]
		if !ok {
			return nil, r.fail(fmt.Errorf("no answer for fact %d", i+1))
		}
		out[i] = answer.Noul
	}
	r.ok()
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

func (r *Ranker) fail(err error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pause < PauseFirst {
		r.pause = PauseFirst
	} else if r.pause *= 2; r.pause > PauseMax {
		r.pause = PauseMax
	}
	r.pausedTo = time.Now().Add(r.pause)
	if r.set.StateFile != "" {
		if mkErr := os.MkdirAll(filepath.Dir(r.set.StateFile), 0o700); mkErr == nil {
			_ = os.WriteFile(r.set.StateFile, []byte(r.pausedTo.UTC().Format(time.RFC3339)), 0o600)
		}
	}
	return err
}

func (r *Ranker) ok() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pause, r.pausedTo = 0, time.Time{}
	if r.set.StateFile != "" {
		_ = os.Remove(r.set.StateFile)
	}
}

// pausedLocked must be called with the lock held.
func (r *Ranker) pausedLocked() (bool, time.Duration) {
	left := r.left()
	if left <= 0 {
		r.pausedTo = time.Time{}
		return false, 0
	}
	return true, left
}

// left is the remaining pause, from memory and from the file a previous process may have written.
func (r *Ranker) left() time.Duration {
	until := r.pausedTo
	if r.set.StateFile != "" {
		if b, err := os.ReadFile(r.set.StateFile); err == nil {
			if t, err := time.Parse(time.RFC3339, strings.TrimSpace(string(b))); err == nil && t.After(until) {
				until = t
			}
		}
	}
	return time.Until(until)
}

func parseFloat(s string) (float64, error) { return strconv.ParseFloat(s, 64) }

func parseInt(s string) (int, error) { return strconv.Atoi(s) }
