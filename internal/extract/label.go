package extract

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Label is the kind, scope and expiry the model gives one fact. A field the code cannot accept comes back empty.
type Label struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Scope   string `json:"scope"`
	Expires string `json:"expires,omitempty"`
}

// LabelResult is the model's answer over a batch of facts.
type LabelResult struct {
	Labels  []Label `json:"labels"`
	CostUSD float64
	Tokens  Tokens
}

//go:embed label_prompt.md
var labelPrompt string

const labelSchema = `{"type":"object","additionalProperties":false,"required":["labels"],"properties":{
"labels":{"type":"array","items":{"type":"object","additionalProperties":false,
"required":["id","kind","scope"],"properties":{"id":{"type":"string"},
"kind":{"type":"string","enum":["rule","decision","pitfall","environment","event","procedure","repo","other"]},
"scope":{"type":"string"},"expires":{"type":["string","null"]}}}}}}`

const labelShape = "\n\nОтвет — только JSON-объект, без пояснений и без markdown: " +
	`{"labels": [{"id": "…", "kind": "rule|decision|pitfall|environment|event|procedure|repo|other", ` +
	`"scope": "global|host:…|repo:…|service:…", "expires": null}]}` + "\n"

// kinds are the values of kind a label may carry: the four the extraction keeps and the ones it names but drops.
var kinds = map[string]bool{"rule": true, "decision": true, "pitfall": true, "environment": true, "event": true,
	"procedure": true, "repo": true, "other": true}

var reScope = regexp.MustCompile(`^(global|(host|repo|service):[^\s:]+)$`)

// LabelFacts asks the model for the kind, scope and expiry of the facts in the rendered input. Values outside the
// accepted forms are cleared, so a caller fills a field only with something the base can hold.
func LabelFacts(ctx context.Context, input string) (LabelResult, error) {
	rp, err := callModel(ctx, Endpoint{URL: API.URL, Model: Model, Key: API.Key}, labelPrompt, labelSchema, labelShape,
		input)
	if err != nil {
		return LabelResult{}, err
	}
	var r LabelResult
	if err := json.Unmarshal(rp.payload, &r); err != nil {
		return LabelResult{}, fmt.Errorf("label: %w", err)
	}
	r.CostUSD, r.Tokens = rp.cost, rp.tokens
	for i := range r.Labels {
		l := &r.Labels[i]
		l.ID = strings.TrimSpace(l.ID)
		if l.Kind = strings.ToLower(strings.TrimSpace(l.Kind)); !kinds[l.Kind] {
			l.Kind = ""
		}
		if l.Scope = strings.TrimSpace(l.Scope); !reScope.MatchString(l.Scope) {
			l.Scope = ""
		}
		if l.Expires = strings.TrimSpace(l.Expires); l.Expires != "" {
			if _, err := time.Parse(time.DateOnly, l.Expires); err != nil {
				l.Expires = ""
			}
		}
	}
	return r, nil
}
