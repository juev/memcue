//go:build research

// Command hook is the words-only recall hook of the size ladder of #325: what the UserPromptSubmit hook does
// (decode the hook input, open the base, search, encode the answer), with no HTTP client in the binary.
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
	hits, err := recall.Search(s, recall.Query{Text: req.Prompt, N: 5, MinScore: recall.MinScore,
		Project: tier.Keys(req.CWD, "")})
	if err != nil {
		panic(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(hits); err != nil {
		panic(err)
	}
}