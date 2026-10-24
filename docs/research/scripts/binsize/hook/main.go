//go:build research

// Command hook is the words-only recall hook of the size ladder of #325: what the UserPromptSubmit hook does without a
// judge (decode the hook input, open the base, recall, encode the answer), with no HTTP client in the binary.
package main

import (
	"encoding/json"
	"os"

	"github.com/juev/memcue/internal/recall"
	"github.com/juev/memcue/internal/store"
	"github.com/juev/memcue/internal/tier"
)

func main() {
	var req struct {
		Prompt string `json:"prompt"`
		CWD    string `json:"cwd"`
	}
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		panic(err)
	}
	s, err := store.Open(os.Args[1])
	if err != nil {
		panic(err)
	}
	hits, err := recall.Recall(s, req.Prompt, tier.Keys(req.CWD, ""), recall.Default)
	if err != nil {
		panic(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(hits); err != nil {
		panic(err)
	}
}
