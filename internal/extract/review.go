package extract

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// Verdicts of a review, as the model names them.
const (
	KeepVerdict   = "keep"
	DropVerdict   = "drop"
	UnsureVerdict = "unsure"
	SplitVerdict  = "split"
)

// ReviewDecision is the verdict for one candidate.
type ReviewDecision struct {
	ID      string   `json:"id"`
	Verdict string   `json:"verdict"` // keep, drop, unsure, split; any other word is read as unsure
	Reason  string   `json:"reason"`
	Expires string   `json:"expires,omitempty"` // YYYY-MM-DD, only when the fact is worth keeping until then
	Parts   []string `json:"parts,omitempty"`   // with split: the simple facts the candidate holds, each standing alone
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
"verdict":{"type":"string","enum":["keep","drop","unsure","split"]},"reason":{"type":"string"},
"expires":{"type":["string","null"]},"parts":{"type":["array","null"],"items":{"type":"string"}}}}}}}`

const reviewShape = "\n\nОтвет — только JSON-объект, без пояснений и без markdown: " +
	`{"decisions": [{"id": "…", "verdict": "keep|drop|unsure|split", "reason": "…", "expires": null, "parts": null}]}` + "\n"

// ReviewOn says whether a review can run: the extraction endpoint is configured, or `claude` is on PATH for the
// `claude -p` path the extraction itself takes without one. The review after an extraction is one more call of the same
// model on the same plan, a call per twelve candidates.
func ReviewOn() bool {
	if API.URL != "" {
		return true
	}
	_, err := exec.LookPath("claude")
	return err == nil
}

// Review asks the model whether the candidates in the rendered input belong in the base. The answer is not checked
// against anything here: a caller decides which ids it sent, and acts only on those it gets back.
func Review(ctx context.Context, input string) (ReviewResult, error) {
	rp, err := callModel(ctx, Endpoint{URL: API.URL, Model: Model, Key: API.Key}, reviewPrompt, reviewSchema,
		reviewShape, input)
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
		var parts []string
		for _, p := range r.Decisions[i].Parts {
			if p = strings.TrimSpace(p); p != "" {
				parts = append(parts, p)
			}
		}
		r.Decisions[i].Parts = parts
	}
	return r, nil
}
