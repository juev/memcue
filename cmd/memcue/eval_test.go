package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/juev/memcue/internal/store"
)

// A non-positive -max-facts means no cap on the eval path too: 0 used to serve no facts and a negative value
// panicked while slicing the hits with a negative index.
func TestEvalNonPositiveMaxFactsServesEveryFact(t *testing.T) {
	// Keep the eval pass lexical: an endpoint configured in the developer's environment would change the answer.
	for _, k := range []string{"MEMCUE_JUDGE_URL", "MEMCUE_JUDGE_MODEL", "MEMCUE_JUDGE_KEY"} {
		t.Setenv(k, "")
	}
	dir := env(t)
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{
		"Прокси mihomo нужен для caddy, а gradle собирается отдельно.",
		"Прокси mihomo и caddy перезапускаются после правки gradle.",
		"Прокси mihomo кэширует caddy на время сборки gradle.",
	} {
		if _, err := s.Add(content, []string{"прокси", "mihomo", "caddy", "gradle"}, "x 2026-10-21"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	setPath := filepath.Join(dir, "eval.json")
	set, err := json.Marshal(map[string]any{
		"prompts": []map[string]any{{"id": "p1", "tier": "", "text": "прокси mihomo caddy gradle"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(setPath, set, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ maxFacts, file string }{{"-1", "neg.json"}, {"0", "zero.json"}} {
		var out bytes.Buffer
		unlabeled := filepath.Join(dir, tc.file)
		if err := runEval([]string{"-set", setPath, "-unlabeled", unlabeled, "-max-facts", tc.maxFacts}, &out); err != nil {
			t.Fatalf("-max-facts %s: %v", tc.maxFacts, err)
		}
		b, err := os.ReadFile(unlabeled)
		if err != nil {
			t.Fatal(err)
		}
		var pairs []struct {
			Prompt string `json:"prompt"`
			Fact   string `json:"fact"`
		}
		if err := json.Unmarshal(b, &pairs); err != nil {
			t.Fatalf("%q: %v", b, err)
		}
		if len(pairs) != 3 {
			t.Fatalf("-max-facts %s recalled %d facts, want all 3: %q", tc.maxFacts, len(pairs), out.String())
		}
	}
}
