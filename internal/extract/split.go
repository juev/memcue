package extract

import (
	_ "embed"
)

// Split is one compound fact and the simple facts it holds.
type Split struct {
	ID     string   `json:"id"`
	Parts  []string `json:"parts"`
	Reason string   `json:"reason"`
}

//go:embed split_prompt.md
var splitPrompt string
