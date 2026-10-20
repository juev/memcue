package extract

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

// Action is one change a dream asks for: retire a fact, or correct it with new content.
type Action struct {
	Op       string   `json:"op"` // "retire" or "correct"
	ID       string   `json:"id"`
	Content  string   `json:"content,omitempty"`
	Keywords []string `json:"keywords,omitempty"`
	By       string   `json:"by,omitempty"`   // retire: the fact of the group that covers or outdates this one
	Kind     string   `json:"kind,omitempty"` // retire: "duplicate" or "outdated"
	Reason   string   `json:"reason"`
}

// DreamResult is the model's answer over groups of similar facts.
type DreamResult struct {
	Actions []Action `json:"actions"`
	CostUSD float64
	Tokens  Tokens
}

//go:embed dream_prompt.md
var dreamPrompt string

const dreamSchema = `{"type":"object","additionalProperties":false,"required":["actions"],"properties":{
"actions":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["op","id","reason"],
"properties":{"op":{"type":"string","enum":["retire","correct"]},"id":{"type":"string"},"content":{"type":"string"},
"keywords":{"type":"array","items":{"type":"string"}},"by":{"type":"string"},
"kind":{"type":"string","enum":["duplicate","outdated"]},"reason":{"type":"string"}}}}}}`

const dreamShape = "\n\nОтвет — только JSON-объект, без пояснений и без markdown: " +
	`{"actions": [{"op": "retire|correct", "id": "…", "content": "…", "keywords": ["…"], "by": "…", "kind": "duplicate|outdated", "reason": "…"}]}` + "\n"

// Dream asks the model which facts of the groups to retire or correct. groups is the rendered input; the caller
// checks the actions against what it sent.
func Dream(ctx context.Context, groups string) (DreamResult, error) {
	rp, err := call(ctx, dreamPrompt, dreamSchema, dreamShape, groups)
	if err != nil {
		return DreamResult{}, err
	}
	var r DreamResult
	if err := json.Unmarshal(rp.payload, &r); err != nil {
		return DreamResult{}, fmt.Errorf("actions: %w", err)
	}
	r.CostUSD, r.Tokens = rp.cost, rp.tokens
	for i := range r.Actions {
		r.Actions[i].Content = strings.TrimSpace(r.Actions[i].Content)
	}
	return r, nil
}
