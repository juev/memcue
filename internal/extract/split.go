package extract

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

// Split is one compound fact and the simple facts it holds.
type Split struct {
	ID     string   `json:"id"`
	Parts  []string `json:"parts"`
	Reason string   `json:"reason"`
}

// SplitResult is the model's answer over a batch of facts: only the compound ones.
type SplitResult struct {
	Splits  []Split `json:"splits"`
	CostUSD float64
	Tokens  Tokens
}

//go:embed split_prompt.md
var splitPrompt string

const splitSchema = `{"type":"object","additionalProperties":false,"required":["splits"],"properties":{
"splits":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["id","parts","reason"],
"properties":{"id":{"type":"string"},"parts":{"type":"array","items":{"type":"string"}},"reason":{"type":"string"}}}}}}`

const splitShape = "\n\nОтвет — только JSON-объект, без пояснений и без markdown: " +
	`{"splits": [{"id": "…", "parts": ["…", "…"], "reason": "…"}]}` + "\n"

// SplitFacts asks the model which facts of the rendered input hold several claims, and into which parts. The parts are
// trimmed and empty ones dropped; the caller checks the ids and the number of parts against what it sent.
func SplitFacts(ctx context.Context, input string) (SplitResult, error) {
	rp, err := call(ctx, splitPrompt, splitSchema, splitShape, input)
	if err != nil {
		return SplitResult{}, err
	}
	var r SplitResult
	if err := json.Unmarshal(rp.payload, &r); err != nil {
		return SplitResult{}, fmt.Errorf("splits: %w", err)
	}
	r.CostUSD, r.Tokens = rp.cost, rp.tokens
	for i := range r.Splits {
		r.Splits[i].ID = strings.TrimSpace(r.Splits[i].ID)
		r.Splits[i].Reason = strings.TrimSpace(r.Splits[i].Reason)
		var parts []string
		for _, p := range r.Splits[i].Parts {
			if p = strings.TrimSpace(p); p != "" {
				parts = append(parts, p)
			}
		}
		r.Splits[i].Parts = parts
	}
	return r, nil
}
