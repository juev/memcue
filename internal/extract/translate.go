package extract

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

// TranslationPrefix marks the one search query that holds the whole fact in the other language, Russian or English
// (#395). A question in the other language than the fact's shares its tool names and nothing else; the translation
// gives it the prose words too (22 of 30 Russian facts found by an English question, 30 of 30 with the translation,
// #392). The lexer drops "=", so the mark itself is never a term.
const TranslationPrefix = "= "

// Translation returns the text of the translation query, if the queries hold one.
func Translation(queries []string) (string, bool) {
	for _, q := range queries {
		if t, ok := strings.CutPrefix(q, TranslationPrefix); ok {
			return t, true
		}
	}
	return "", false
}

// SearchQueries are the queries without the translation: the short phrases the dream writes for a fact that has none.
func SearchQueries(queries []string) []string {
	var out []string
	for _, q := range queries {
		if !strings.HasPrefix(q, TranslationPrefix) {
			out = append(out, q)
		}
	}
	return out
}

// WithTranslation returns the queries with t as their translation, replacing an older one; an empty t leaves the
// queries without one.
func WithTranslation(queries []string, t string) []string {
	out := SearchQueries(queries)
	if t = strings.Join(strings.Fields(strings.ReplaceAll(t, ",", " ")), " "); t != "" {
		out = append(out, TranslationPrefix+t)
	}
	return out
}

// Translated is the translation the model wrote for one fact.
type Translated struct {
	ID          string `json:"id"`
	Translation string `json:"translation"`
}

// TranslateResult is the model's answer over a batch of facts.
type TranslateResult struct {
	Facts   []Translated `json:"facts"`
	CostUSD float64
	Tokens  Tokens
}

//go:embed translate_prompt.md
var translatePrompt string

const translateSchema = `{"type":"object","additionalProperties":false,"required":["facts"],"properties":{
"facts":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["id","translation"],
"properties":{"id":{"type":"string"},"translation":{"type":"string"}}}}}}`

const translateShape = "\n\nОтвет — только JSON-объект, без пояснений и без markdown: " +
	`{"facts": [{"id": "…", "translation": "…"}]}` + "\n"

// TranslateFacts asks the main model, without reasoning, for each fact of the rendered input in the other language.
// The caller checks the ids against what it sent.
func TranslateFacts(ctx context.Context, input string) (TranslateResult, error) {
	rp, err := callModel(ctx, Endpoint{URL: API.URL, Model: Model, Key: API.Key, Quick: true}, translatePrompt,
		translateSchema, translateShape, input)
	if err != nil {
		return TranslateResult{}, err
	}
	var r TranslateResult
	if err := json.Unmarshal(rp.payload, &r); err != nil {
		return TranslateResult{}, fmt.Errorf("translation: %w", err)
	}
	r.CostUSD, r.Tokens = rp.cost, rp.tokens
	for i := range r.Facts {
		r.Facts[i].ID = strings.TrimSpace(r.Facts[i].ID)
		r.Facts[i].Translation = strings.TrimSpace(r.Facts[i].Translation)
	}
	return r, nil
}
