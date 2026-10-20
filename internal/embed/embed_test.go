package embed

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func settings(t *testing.T, srv *httptest.Server) *Settings {
	t.Helper()
	return &Settings{URL: srv.URL, Model: "test-model", KeyEnv: "MEMCUE_TEST_KEY", Timeout: time.Second}
}

func TestEmbedReturnsNormalizedVectorsInInputOrder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/embeddings" {
			t.Errorf("path = %q, want /embeddings", got)
		}
		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Model != "test-model" || len(req.Input) != 2 {
			t.Errorf("request = %+v", req)
		}
		// The answer comes back out of order on purpose: the index decides where a vector belongs.
		_, _ = w.Write([]byte(`{"data":[{"index":1,"embedding":[0,2]},{"index":0,"embedding":[3,4]}]}`))
	}))
	defer srv.Close()

	c := New(settings(t, srv))
	vectors, err := c.Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vectors) != 2 {
		t.Fatalf("got %d vectors", len(vectors))
	}
	if math.Abs(float64(vectors[0][0])-0.6) > 1e-6 || math.Abs(float64(vectors[0][1])-0.8) > 1e-6 {
		t.Errorf("first vector not normalized: %v", vectors[0])
	}
	if vectors[1][1] != 1 {
		t.Errorf("second vector not in place: %v", vectors[1])
	}
}

func TestEmbedFailurePausesTheEndpoint(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, `{"error":{"message":"boom"}}`, http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := New(settings(t, srv))
	if _, err := c.Embed(context.Background(), []string{"a"}); err == nil {
		t.Fatal("a failing endpoint must return an error")
	}
	if _, err := c.Embed(context.Background(), []string{"a"}); !errors.Is(err, ErrPaused) {
		t.Fatalf("second call = %v, want ErrPaused", err)
	}
	if calls.Load() != 1 {
		t.Errorf("endpoint was called %d times, want 1: the pause must stop the call", calls.Load())
	}
	if paused, left := c.Paused(); !paused || left <= 0 || left > PauseFirst {
		t.Errorf("Paused() = %v, %v", paused, left)
	}
}

func TestPauseSurvivesTheProcess(t *testing.T) {
	state := filepath.Join(t.TempDir(), "embed.pause")
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	defer srv.Close()

	first := settings(t, srv)
	first.StateFile = state
	if _, err := New(first).Embed(context.Background(), []string{"a"}); err == nil {
		t.Fatal("want an error")
	}
	if _, err := os.Stat(state); err != nil {
		t.Fatalf("pause was not written: %v", err)
	}
	// A hook is a fresh process on every turn: the second one must not pay the timeout again.
	second := settings(t, srv)
	second.StateFile = state
	if _, err := New(second).Embed(context.Background(), []string{"a"}); !errors.Is(err, ErrPaused) {
		t.Fatalf("fresh client = %v, want ErrPaused", err)
	}
	if calls.Load() != 1 {
		t.Errorf("endpoint was called %d times, want 1", calls.Load())
	}
}

func TestSuccessClearsThePause(t *testing.T) {
	var fail atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"index":0,"embedding":[1,0]}]}`))
	}))
	defer srv.Close()

	c := New(settings(t, srv))
	fail.Store(true)
	if _, err := c.Embed(context.Background(), []string{"a"}); err == nil {
		t.Fatal("want an error")
	}
	fail.Store(false)
	c.mu.Lock()
	c.pausedTo = time.Time{} // the pause elapsed
	c.mu.Unlock()
	if _, err := c.Embed(context.Background(), []string{"a"}); err != nil {
		t.Fatal(err)
	}
	if paused, _ := c.Paused(); paused {
		t.Error("a working endpoint must not stay paused")
	}
}

func TestFromEnvIsOffWithoutEndpointOrModel(t *testing.T) {
	t.Setenv("MEMCUE_EMBED_URL", "")
	t.Setenv("MEMCUE_EMBED_MODEL", "")
	if s := FromEnv(); s != nil {
		t.Fatalf("FromEnv with nothing set = %+v, want nil", s)
	}
	t.Setenv("MEMCUE_EMBED_URL", "https://routerai.ru/api/v1")
	t.Setenv("MEMCUE_EMBED_MODEL", "bge-m3")
	s := FromEnv()
	if s == nil || s.URL != "https://routerai.ru/api/v1" || s.Timeout != DefaultTimeout || s.KeyEnv != "OPENAI_API_KEY" {
		t.Fatalf("FromEnv = %+v", s)
	}
}

func TestTagSeparatesModelsAddressesAndSpaces(t *testing.T) {
	base := &Settings{URL: "https://RouterAI.ru/api/v1/", Model: "bge-m3"}
	other := &Settings{URL: "https://routerai.ru/api/v1", Model: "bge-m3"}
	if base.Tag() != other.Tag() {
		t.Errorf("the same address written two ways gave different tags: %q and %q", base.Tag(), other.Tag())
	}
	if (&Settings{URL: base.URL, Model: "qwen3-embedding-8b"}).Tag() == base.Tag() {
		t.Error("two models share one tag")
	}
	if (&Settings{URL: base.URL, Model: base.Model, Space: "v2"}).Tag() == base.Tag() {
		t.Error("the space label does not change the tag")
	}
	if got := (&Settings{URL: "http://127.0.0.1:11434/api", Model: "bge-m3"}).Tag(); got != "http://127.0.0.1:11434/api/bge-m3" {
		t.Errorf("local tag = %q", got)
	}
}
