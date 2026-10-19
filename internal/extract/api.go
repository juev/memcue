package extract

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// API is an OpenAI-compatible chat completions endpoint. When URL is set the extraction calls it with Model instead
// of running `claude -p`: for hosts without Claude Code, such as Hermes Agent.
var API struct {
	URL string // base URL, e.g. https://api.deepseek.com; /chat/completions is appended
	Key string
}

// jsonAnswer replaces the structured output that `claude -p` enforces with a schema: chat completions only promise
// some JSON object, so the prompt names the shape.
const jsonAnswer = "\n\nОтвет — только JSON-объект, без пояснений и без markdown: " +
	`{"facts": [{"content": "…", "keywords": ["…"]}], "dropped": "…", "outdated": []}` + "\n"

// Tokens is the usage an endpoint reports; it has no price, so the cost is counted from tokens.
type Tokens struct {
	Input  int `json:"input"`
	Cached int `json:"cached,omitempty"` // part of Input served from the provider's prompt cache
	Output int `json:"output"`
}

// Add sums usage over calls.
func (t *Tokens) Add(o Tokens) {
	t.Input += o.Input
	t.Cached += o.Cached
	t.Output += o.Output
}

func runAPI(ctx context.Context, system, input string) (Result, error) {
	body, err := json.Marshal(map[string]any{
		"model": Model,
		"messages": []map[string]string{
			{"role": "system", "content": system + jsonAnswer},
			{"role": "user", "content": input},
		},
		"response_format": map[string]string{"type": "json_object"},
	})
	if err != nil {
		return Result{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(API.URL, "/")+"/chat/completions",
		bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if API.Key != "" {
		req.Header.Set("Authorization", "Bearer "+API.Key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return Result{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(raw[:min(len(raw), 500)])))
	}
	return parseAPI(raw)
}

func parseAPI(raw []byte) (Result, error) {
	var env struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			CacheHit         int `json:"prompt_cache_hit_tokens"` // DeepSeek
			Details          struct {
				Cached int `json:"cached_tokens"` // OpenAI
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return Result{}, fmt.Errorf("endpoint output: %w", err)
	}
	if len(env.Choices) == 0 {
		return Result{}, fmt.Errorf("endpoint returned no choices")
	}
	if fr := env.Choices[0].FinishReason; fr == "length" {
		return Result{}, fmt.Errorf("answer cut at the token limit")
	}
	var r Result
	if err := json.Unmarshal([]byte(stripFence(env.Choices[0].Message.Content)), &r); err != nil {
		return Result{}, fmt.Errorf("facts: %w", err)
	}
	r.Tokens = Tokens{Input: env.Usage.PromptTokens, Cached: max(env.Usage.CacheHit, env.Usage.Details.Cached),
		Output: env.Usage.CompletionTokens}
	for i := range r.Facts {
		r.Facts[i].Content = strings.TrimSpace(r.Facts[i].Content)
	}
	if len(r.Facts) == 0 && r.Dropped == "" {
		return r, errEmpty
	}
	return r, nil
}

// stripFence removes a ```json fence some models put around the object despite the instruction.
func stripFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimPrefix(s, "json")
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "```"))
}
