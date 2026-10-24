//go:build research

// Command hookjev is the recall hook with the Jev pass of the size ladder of #325: the hook step plus internal/rerank,
// which brings net/http and crypto/tls.
package main

import (
	"encoding/json"
	"os"

	"github.com/juev/memcue/internal/recall"
	"github.com/juev/memcue/internal/rerank"
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
	var r recall.Ranker
	if set := rerank.FromEnv(); set != nil {
		r = rerank.New(set)
	}
	hits, err := recall.RecallJudge(s, req.Prompt, tier.Of(req.CWD, ""), recall.Default, r)
	if err != nil {
		panic(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(hits); err != nil {
		panic(err)
	}
}
