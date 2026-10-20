package main

import (
	"bytes"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/juev/memcue/internal/embed"
	"github.com/juev/memcue/internal/recall"
	"github.com/juev/memcue/internal/store"
)

// vectorServer answers like an OpenAI-compatible endpoint: one vector per input, sized to the text so two different
// texts never collide.
func vectorServer(t *testing.T, calls *int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls != nil {
			*calls++
		}
		var req struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode: %v", err)
			return
		}
		data := make([]map[string]any, len(req.Input))
		for i, text := range req.Input {
			data[i] = map[string]any{"index": i, "embedding": []float32{float32(len(text)), 1}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
}

func seedFacts(t *testing.T, n int) {
	t.Helper()
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	for i := 0; i < n; i++ {
		if _, err := s.Add("Факт номер "+strings.Repeat("x", i+1)+" про сборку и окружение.",
			[]string{"сборка"}, "test 2026-10-21"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestEmbedFillsTheVectorsOfActiveFacts(t *testing.T) {
	env(t)
	seedFacts(t, 3)
	calls := 0
	srv := vectorServer(t, &calls)
	defer srv.Close()
	t.Setenv("MEMCUE_EMBED_URL", srv.URL)
	t.Setenv("MEMCUE_EMBED_MODEL", "test-model")
	t.Setenv("MEMCUE_EMBED_KEY_ENV", "MEMCUE_TEST_KEY")

	var out, errOut bytes.Buffer
	if err := runEmbed(nil, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "embedded 3 facts") {
		t.Errorf("output = %q", out.String())
	}
	if calls != 1 {
		t.Errorf("%d requests, want one batch", calls)
	}
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	client := embed.New(&embed.Settings{URL: srv.URL, Model: "test-model", KeyEnv: "MEMCUE_TEST_KEY"})
	tag := client.Tag()
	n, err := s.TagVectors(tag)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("%d vectors stored, want 3", n)
	}
	pending, err := s.MissingVectors(tag, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("%d facts still pending", len(pending))
	}
}

func TestEmbedDryRunWritesNothing(t *testing.T) {
	env(t)
	seedFacts(t, 2)
	calls := 0
	srv := vectorServer(t, &calls)
	defer srv.Close()
	t.Setenv("MEMCUE_EMBED_URL", srv.URL)
	t.Setenv("MEMCUE_EMBED_MODEL", "test-model")

	var out, errOut bytes.Buffer
	if err := runEmbed([]string{"-dry-run"}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "2 facts need a vector") {
		t.Errorf("output = %q", out.String())
	}
	if calls != 0 {
		t.Errorf("%d requests, want none for a dry run", calls)
	}
}

func TestEmbedIsOffWithoutEndpoint(t *testing.T) {
	env(t)
	t.Setenv("MEMCUE_EMBED_URL", "")
	t.Setenv("MEMCUE_EMBED_MODEL", "")
	var out, errOut bytes.Buffer
	if err := runEmbed(nil, &out, &errOut); err == nil {
		t.Fatal("want an error naming the missing settings")
	}
	if vp := vectorParams(); vp.Embedder != nil {
		t.Fatal("without settings the meaning pass must have no embedder")
	}
}

func TestHookStaysLexicalWhenTheEndpointIsDown(t *testing.T) {
	dir := env(t)
	seed(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	t.Setenv("MEMCUE_EMBED_URL", srv.URL)
	t.Setenv("MEMCUE_EMBED_MODEL", "test-model")

	in := `{"prompt":"Как запустить gradle без make, какой JAVA_HOME нужен для JDK?","cwd":"` + dir + `"}`
	var warm bytes.Buffer
	if err := runHook(strings.NewReader(in), &warm); err != nil {
		t.Fatal(err)
	}
	// The fact comes from the words, exactly as it did before the feature existed.
	if !strings.Contains(warm.String(), "JAVA_HOME") {
		t.Fatalf("hook output = %q", warm.String())
	}
}

func TestVectorParamsReadTheMargin(t *testing.T) {
	t.Setenv("MEMCUE_EMBED_MARGIN", "2.5")
	t.Setenv("MEMCUE_EMBED_MAX_FACTS", "3")
	vp := vectorParams()
	if math.Abs(vp.Margin-2.5) > 1e-9 || vp.MaxFacts != 3 {
		t.Fatalf("vectorParams = %+v", vp)
	}
	t.Setenv("MEMCUE_EMBED_MARGIN", "not a number")
	if vp := vectorParams(); math.Abs(vp.Margin-recall.DefaultVectorMargin) > 1e-9 {
		t.Fatalf("a malformed margin must keep the default, got %v", vp.Margin)
	}
}
