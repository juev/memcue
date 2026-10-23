package rerank

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/juev/memcue/internal/judge"
)

// listwiseEndpoint answers with the picks and records the request it was asked.
func listwiseEndpoint(t *testing.T, status int, picks string) (*httptest.Server, *map[string]any) {
	t.Helper()
	var seen map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("path %q auth %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		b, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(b, &seen); err != nil {
			t.Errorf("request %s", b)
		}
		answer, _ := json.Marshal(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": picks}, "finish_reason": "stop"}},
			"usage":   map[string]int{"prompt_tokens": 100, "completion_tokens": 10},
		})
		w.WriteHeader(status)
		_, _ = w.Write(answer)
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

func listwiseReader(url string) *Listwise {
	return NewListwise(&ListwiseSettings{URL: url, Model: "m", Key: "k", Timeout: 2 * time.Second,
		Candidates: DefaultListwiseCandidates})
}

var listwiseFacts = []string{"Первый факт про caddy.", "Второй факт про gradle.", "Третий факт про nginx."}

func TestListwiseTurnsPicksIntoOrderedScores(t *testing.T) {
	srv, seen := listwiseEndpoint(t, http.StatusOK, `{"facts":[2,1]}`)
	scores, err := listwiseReader(srv.URL).Scores(context.Background(), "почему падает сборка", listwiseFacts)
	if err != nil {
		t.Fatal(err)
	}
	if want := []float64{0.9, 1, 0}; !slices.Equal(scores, want) {
		t.Fatalf("scores = %v, want %v", scores, want)
	}
	body := *seen
	if body["temperature"] != float64(0) {
		t.Errorf("temperature = %v, want 0", body["temperature"])
	}
	if rf, _ := body["response_format"].(map[string]any); rf["type"] != "json_object" {
		t.Errorf("response_format = %v", body["response_format"])
	}
	messages, _ := body["messages"].([]any)
	if len(messages) != 2 {
		t.Fatalf("messages = %v", messages)
	}
	system, _ := messages[0].(map[string]any)["content"].(string)
	if !strings.Contains(system, "You rank memory facts") {
		t.Errorf("system = %q", system)
	}
	user, _ := messages[1].(map[string]any)["content"].(string)
	for _, want := range []string{"Prompt: почему падает сборка", "[1] Первый факт про caddy.",
		"[3] Третий факт про nginx.", "at most 2"} {
		if !strings.Contains(user, want) {
			t.Errorf("user message misses %q: %q", want, user)
		}
	}
	if _, ok := body["thinking"]; ok {
		t.Errorf("thinking sent to a non-DeepSeek endpoint: %v", body["thinking"])
	}
}

func TestListwiseIgnoresOutOfRangeAndRepeatedPicks(t *testing.T) {
	srv, _ := listwiseEndpoint(t, http.StatusOK, `{"facts":[0,9,-1,3,3,1]}`)
	scores, err := listwiseReader(srv.URL).Scores(context.Background(), "p", listwiseFacts)
	if err != nil {
		t.Fatal(err)
	}
	if want := []float64{0.9, 0, 1}; !slices.Equal(scores, want) {
		t.Fatalf("scores = %v, want %v", scores, want)
	}
}

// An empty pick is a verdict, not a failure: every fact scores zero, so recall serves nothing and no fallback runs.
func TestListwiseEmptyPickScoresNothing(t *testing.T) {
	srv, _ := listwiseEndpoint(t, http.StatusOK, `{"facts":[]}`)
	scores, err := listwiseReader(srv.URL).Scores(context.Background(), "p", listwiseFacts)
	if err != nil {
		t.Fatal(err)
	}
	if want := []float64{0, 0, 0}; !slices.Equal(scores, want) {
		t.Fatalf("scores = %v, want %v", scores, want)
	}
}

func TestListwiseReadsAFencedAnswer(t *testing.T) {
	srv, _ := listwiseEndpoint(t, http.StatusOK, "```json\n{\"facts\":[1]}\n```")
	scores, err := listwiseReader(srv.URL).Scores(context.Background(), "p", listwiseFacts)
	if err != nil {
		t.Fatal(err)
	}
	if want := []float64{1, 0, 0}; !slices.Equal(scores, want) {
		t.Fatalf("scores = %v, want %v", scores, want)
	}
}

// A failed call and a broken answer are failures the caller turns into the lexical fallback, never an empty pick.
func TestListwiseFailuresAreErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		answer string
	}{
		"endpoint error": {http.StatusInternalServerError, `{"error":"down"}`},
		"not an answer":  {http.StatusOK, `dunno`},
		"wrong shape":    {http.StatusOK, `{"picks":[1]}`},
	} {
		t.Run(name, func(t *testing.T) {
			srv, _ := listwiseEndpoint(t, tc.status, tc.answer)
			if _, err := listwiseReader(srv.URL).Scores(context.Background(), "p", listwiseFacts); err == nil {
				t.Fatal("want an error")
			}
		})
	}
}

// A timeout is a failure, and the breaker then keeps the reader out of the next call.
func TestListwiseTimeoutPausesTheReader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"{\"facts\":[1]}"}}]}`)
	}))
	t.Cleanup(srv.Close)
	r := NewListwise(&ListwiseSettings{URL: srv.URL, Model: "m", Key: "k", Timeout: 100 * time.Millisecond,
		Candidates: DefaultListwiseCandidates})
	if _, err := r.Scores(context.Background(), "p", listwiseFacts); err == nil {
		t.Fatal("want a timeout error")
	}
	if _, err := r.Scores(context.Background(), "p", listwiseFacts); !errors.Is(err, judge.ErrPaused) {
		t.Fatalf("second call = %v, want ErrPaused", err)
	}
}

func TestListwiseFromEnvIsOffWithoutAnEndpoint(t *testing.T) {
	for _, k := range []string{"MEMCUE_API_URL", "MEMCUE_MODEL", "MEMCUE_API_KEY", "MEMCUE_MAIN_RERANK_TIMEOUT",
		"MEMCUE_MAIN_RERANK_CANDIDATES"} {
		t.Setenv(k, "")
	}
	if s := ListwiseFromEnv(); s != nil {
		t.Fatalf("ListwiseFromEnv = %+v, want nil", s)
	}
	t.Setenv("MEMCUE_API_URL", "https://api.deepseek.com")
	t.Setenv("MEMCUE_MODEL", "deepseek-flash")
	if s := ListwiseFromEnv(); s == nil || s.Timeout != DefaultListwiseTimeout ||
		s.Candidates != DefaultListwiseCandidates {
		t.Fatalf("ListwiseFromEnv = %+v", s)
	}
	t.Setenv("MEMCUE_MAIN_RERANK_TIMEOUT", "1500ms")
	t.Setenv("MEMCUE_MAIN_RERANK_CANDIDATES", "5")
	if s := ListwiseFromEnv(); s.Timeout != 1500*time.Millisecond || s.Candidates != 5 {
		t.Fatalf("ListwiseFromEnv = %+v", s)
	}
	t.Setenv("MEMCUE_MAIN_RERANK_CANDIDATES", "1")
	if s := ListwiseFromEnv(); s.Candidates != DefaultListwiseCandidates {
		t.Fatalf("a candidate count below two must keep the default: %+v", s)
	}
}

// The reader reports its own threshold: the configured Jev probability must not decide which pick survives.
func TestListwiseMinIsItsOwn(t *testing.T) {
	if got := listwiseReader("http://127.0.0.1:1").Min(); got != listwiseMin {
		t.Fatalf("Min = %v, want %v", got, listwiseMin)
	}
	if listwiseMin <= 0 || listwiseMin >= listwiseFirstScore {
		t.Fatalf("listwiseMin = %v, must sit between an unpicked 0 and a pick", listwiseMin)
	}
}
