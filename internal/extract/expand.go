package extract

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

// Expansion is the search queries the model wrote for one fact.
type Expansion struct {
	ID      string   `json:"id"`
	Queries []string `json:"queries"`
}

// ExpandResult is the model's answer over a batch of facts.
type ExpandResult struct {
	Facts   []Expansion `json:"facts"`
	CostUSD float64
	Tokens  Tokens
}

//go:embed expand_prompt.md
var expandPrompt string

const expandSchema = `{"type":"object","additionalProperties":false,"required":["facts"],"properties":{
"facts":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["id","queries"],
"properties":{"id":{"type":"string"},"queries":{"type":"array","items":{"type":"string"}}}}}}}`

const expandShape = "\n\nОтвет — только JSON-объект, без пояснений и без markdown: " +
	`{"facts": [{"id": "…", "queries": ["…", "…"]}]}` + "\n"

// ExpandFacts asks the main model, without reasoning, for the words and short phrases a user would ask about each fact
// of the rendered input with. Queries are trimmed, empty ones and commas inside one are dropped (the store joins them
// with ", "); the caller checks the ids against what it sent.
func ExpandFacts(ctx context.Context, input string) (ExpandResult, error) {
	rp, err := callModel(ctx, Endpoint{URL: API.URL, Model: Model, Key: API.Key, Quick: true}, expandPrompt,
		expandSchema, expandShape, input)
	if err != nil {
		return ExpandResult{}, err
	}
	var r ExpandResult
	if err := json.Unmarshal(rp.payload, &r); err != nil {
		return ExpandResult{}, fmt.Errorf("queries: %w", err)
	}
	r.CostUSD, r.Tokens = rp.cost, rp.tokens
	for i := range r.Facts {
		r.Facts[i].ID = strings.TrimSpace(r.Facts[i].ID)
		r.Facts[i].Queries = CleanQueries(r.Facts[i].Queries)
	}
	return r, nil
}

// CleanQueries trims the queries, drops empty ones and repeats, and replaces a comma inside one with a space.
func CleanQueries(queries []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, q := range queries {
		q = strings.Join(strings.Fields(strings.ReplaceAll(q, ",", " ")), " ")
		if q == "" || seen[strings.ToLower(q)] {
			continue
		}
		seen[strings.ToLower(q)] = true
		out = append(out, q)
	}
	return out
}
