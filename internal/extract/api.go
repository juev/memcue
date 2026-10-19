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

func runAPI(ctx context.Context, system, input string) (reply, error) {
	body, err := json.Marshal(map[string]any{
		"model": Model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": input},
		},
		"response_format": map[string]string{"type": "json_object"},
	})
	if err != nil {
		return reply{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(API.URL, "/")+"/chat/completions",
		bytes.NewReader(body))
	if err != nil {
		return reply{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if API.Key != "" {
		req.Header.Set("Authorization", "Bearer "+API.Key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return reply{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return reply{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return reply{}, fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(raw[:min(len(raw), 500)])))
	}
	return parseAPI(raw)
}

// parseAPI takes the answer out of a chat completion.
func parseAPI(raw []byte) (reply, error) {
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
		return reply{}, fmt.Errorf("endpoint output: %w", err)
	}
	if len(env.Choices) == 0 {
		return reply{}, fmt.Errorf("endpoint returned no choices")
	}
	if fr := env.Choices[0].FinishReason; fr == "length" {
		return reply{}, fmt.Errorf("answer cut at the token limit")
	}
	return reply{payload: []byte(stripFence(env.Choices[0].Message.Content)),
		tokens: Tokens{Input: env.Usage.PromptTokens, Cached: max(env.Usage.CacheHit, env.Usage.Details.Cached),
			Output: env.Usage.CompletionTokens}}, nil
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
