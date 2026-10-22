package extract

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

// Verdicts about a suspect fact, as the model names them.
const (
	RetireSuspect = "retire"
	KeepSuspect   = "keep"
)

// SuspectDecision is the verdict about one approved fact the binary found suspect.
type SuspectDecision struct {
	ID      string `json:"id"`
	Verdict string `json:"verdict"` // retire or keep; any other word is read as keep
	Reason  string `json:"reason"`
}

// SuspectResult is the model's answer over a batch of suspects.
type SuspectResult struct {
	Decisions []SuspectDecision `json:"decisions"`
	CostUSD   float64
	Tokens    Tokens
}

//go:embed suspect_prompt.md
var suspectPrompt string

const suspectSchema = `{"type":"object","additionalProperties":false,"required":["decisions"],"properties":{
"decisions":{"type":"array","items":{"type":"object","additionalProperties":false,
"required":["id","verdict","reason"],"properties":{"id":{"type":"string"},
"verdict":{"type":"string","enum":["retire","keep"]},"reason":{"type":"string"}}}}}}`

const suspectShape = "\n\nОтвет — только JSON-объект, без пояснений и без markdown: " +
	`{"decisions": [{"id": "…", "verdict": "retire|keep", "reason": "…"}]}` + "\n"

// ReviewSuspects asks the main model, without reasoning, whether each approved fact of the rendered input describes a
// past state as current (#289). The caller acts only on the ids it sent.
func ReviewSuspects(ctx context.Context, input string) (SuspectResult, error) {
	rp, err := callModel(ctx, Endpoint{URL: API.URL, Model: Model, Key: API.Key, Quick: true}, suspectPrompt,
		suspectSchema, suspectShape, input)
	if err != nil {
		return SuspectResult{}, err
	}
	var r SuspectResult
	if err := json.Unmarshal(rp.payload, &r); err != nil {
		return SuspectResult{}, fmt.Errorf("suspects: %w", err)
	}
	r.CostUSD, r.Tokens = rp.cost, rp.tokens
	for i := range r.Decisions {
		r.Decisions[i].ID = strings.TrimSpace(r.Decisions[i].ID)
		r.Decisions[i].Verdict = strings.ToLower(strings.TrimSpace(r.Decisions[i].Verdict))
	}
	return r, nil
}
