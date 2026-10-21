package rerank

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// stub answers like the decisions API: one noul per fact, the first fact always the most useful.
func stub(t *testing.T, calls *atomic.Int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls != nil {
			calls.Add(1)
		}
		var req struct {
			Model     string         `json:"model"`
			State     []string       `json:"state"`
			Questions map[string]any `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode: %v", err)
		}
		if req.Model != DefaultModel {
			t.Errorf("model = %q", req.Model)
		}
		if len(req.State) < 2 || req.State[0][:8] != "Prompt: " {
			t.Errorf("state = %v", req.State)
		}
		answers := map[string]any{}
		for i := range req.Questions {
			n, err := strconv.Atoi(i[1:])
			if err != nil {
				t.Errorf("question key %q", i)
			}
			score := 0.1
			if n == 1 {
				score = 0.9
			}
			answers[i] = map[string]any{"type": "noul", "noul": score}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "stub", "answers": answers})
	}))
}

func settings(srv *httptest.Server) *Settings {
	return &Settings{URL: srv.URL, Model: DefaultModel, Key: "k", Timeout: time.Second, Min: DefaultMin,
		Candidates: DefaultCandidates}
}

func TestScoresFollowTheFacts(t *testing.T) {
	var calls atomic.Int32
	srv := stub(t, &calls)
	defer srv.Close()
	scores, err := New(settings(srv)).Scores(context.Background(), "как собрать проект?",
		[]string{"первый факт", "второй факт", "третий факт"})
	if err != nil {
		t.Fatal(err)
	}
	if len(scores) != 3 || scores[0] != 0.9 || scores[1] != 0.1 {
		t.Fatalf("scores = %v", scores)
	}
	if calls.Load() != 1 {
		t.Errorf("%d requests, want one", calls.Load())
	}
}

func TestFailurePausesTheEndpoint(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "boom", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	c := New(settings(srv))
	if _, err := c.Scores(context.Background(), "p", []string{"a", "b"}); err == nil {
		t.Fatal("want an error")
	}
	if _, err := c.Scores(context.Background(), "p", []string{"a", "b"}); !errors.Is(err, ErrPaused) {
		t.Fatalf("second call = %v, want ErrPaused", err)
	}
	if calls.Load() != 1 {
		t.Errorf("endpoint was called %d times, want 1", calls.Load())
	}
}

func TestPauseSurvivesTheProcess(t *testing.T) {
	state := filepath.Join(t.TempDir(), "rerank.pause")
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	defer srv.Close()
	first := settings(srv)
	first.StateFile = state
	if _, err := New(first).Scores(context.Background(), "p", []string{"a"}); err == nil {
		t.Fatal("want an error")
	}
	if _, err := os.Stat(state); err != nil {
		t.Fatalf("pause not written: %v", err)
	}
	second := settings(srv)
	second.StateFile = state
	if _, err := New(second).Scores(context.Background(), "p", []string{"a"}); !errors.Is(err, ErrPaused) {
		t.Fatalf("fresh process = %v, want ErrPaused", err)
	}
	if calls.Load() != 1 {
		t.Errorf("endpoint was called %d times, want 1", calls.Load())
	}
}

func TestFromEnvIsOffWithoutEndpointOrKey(t *testing.T) {
	t.Setenv("MEMCUE_RERANK_URL", "")
	t.Setenv("MEMCUE_RERANK_KEY", "")
	t.Setenv("OPENROUTER_API_KEY", "")
	if s := FromEnv(); s != nil {
		t.Fatalf("FromEnv = %+v, want nil", s)
	}
	t.Setenv("MEMCUE_RERANK_URL", "https://openrouter.ai/api/alpha/decisions")
	if s := FromEnv(); s != nil {
		t.Fatal("an endpoint without a key must stay off")
	}
	t.Setenv("MEMCUE_RERANK_KEY", "k")
	s := FromEnv()
	if s == nil || s.Model != DefaultModel || s.Min != DefaultMin || s.Candidates != DefaultCandidates ||
		s.Timeout != DefaultTimeout {
		t.Fatalf("FromEnv = %+v", s)
	}
	t.Setenv("MEMCUE_RERANK_MIN", "0.7")
	t.Setenv("MEMCUE_RERANK_CANDIDATES", "4")
	if s := FromEnv(); s.Min != 0.7 || s.Candidates != 4 {
		t.Fatalf("FromEnv = %+v", s)
	}
}
