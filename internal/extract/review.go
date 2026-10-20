package extract

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

// Judge is where a candidate review runs. An empty field falls back to the extraction's own endpoint, key and model,
// so the judge runs on the extraction model by default; setting Judge.Model is how a cheaper model reviews what the
// main one extracted.
var Judge struct {
	URL   string
	Model string
	Key   string
}

// Verdicts of a review, as the model names them.
const (
	KeepVerdict   = "keep"
	DropVerdict   = "drop"
	UnsureVerdict = "unsure"
)

// ReviewDecision is the verdict for one candidate.
type ReviewDecision struct {
	ID      string `json:"id"`
	Verdict string `json:"verdict"` // keep, drop, unsure; any other word is read as unsure
	Reason  string `json:"reason"`
	Expires string `json:"expires,omitempty"` // YYYY-MM-DD, only when the fact is worth keeping until then
}

// ReviewResult is the model's answer over a batch of candidates.
type ReviewResult struct {
	Decisions []ReviewDecision `json:"decisions"`
	CostUSD   float64
	Tokens    Tokens
}

//go:embed review_prompt.md
var reviewPrompt string

const reviewSchema = `{"type":"object","additionalProperties":false,"required":["decisions"],"properties":{
"decisions":{"type":"array","items":{"type":"object","additionalProperties":false,
"required":["id","verdict","reason"],"properties":{"id":{"type":"string"},
"verdict":{"type":"string","enum":["keep","drop","unsure"]},"reason":{"type":"string"},
"expires":{"type":["string","null"]}}}}}}`

const reviewShape = "\n\nОтвет — только JSON-объект, без пояснений и без markdown: " +
	`{"decisions": [{"id": "…", "verdict": "keep|drop|unsure", "reason": "…", "expires": null}]}` + "\n"

// reviewEndpoint is where a review goes: Judge overrides the extraction endpoint field by field.
func reviewEndpoint() Endpoint {
	ep := Endpoint{URL: API.URL, Model: Model, Key: API.Key}
	if Judge.URL != "" {
		ep.URL = Judge.URL
	}
	if Judge.Model != "" {
		ep.Model = Judge.Model
	}
	if Judge.Key != "" {
		ep.Key = Judge.Key
	}
	return ep
}

// ReviewOn says whether a review can run without `claude -p`: an endpoint is configured, the review's own or the
// extraction's. A hook leaves the subscription alone; a by-hand `memcue vet` may still use `claude -p`.
func ReviewOn() bool { return reviewEndpoint().URL != "" }

// Review asks the model whether the candidates in the rendered input belong in the base. The answer is not checked
// against anything here: a caller decides which ids it sent, and acts only on those it gets back.
func Review(ctx context.Context, input string) (ReviewResult, error) {
	rp, err := callModel(ctx, reviewEndpoint(), reviewPrompt, reviewSchema, reviewShape, input)
	if err != nil {
		return ReviewResult{}, err
	}
	var r ReviewResult
	if err := json.Unmarshal(rp.payload, &r); err != nil {
		return ReviewResult{}, fmt.Errorf("review: %w", err)
	}
	r.CostUSD, r.Tokens = rp.cost, rp.tokens
	for i := range r.Decisions {
		r.Decisions[i].ID = strings.TrimSpace(r.Decisions[i].ID)
		r.Decisions[i].Verdict = strings.ToLower(strings.TrimSpace(r.Decisions[i].Verdict))
		r.Decisions[i].Expires = strings.TrimSpace(r.Decisions[i].Expires)
	}
	return r, nil
}
