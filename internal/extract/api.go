package extract

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"strings"
)

// API is an OpenAI-compatible chat completions endpoint. When URL is set the model calls go to it with Model instead
// of running `claude -p`: for hosts without Claude Code, such as Hermes Agent.
var API struct {
	URL string // base URL, e.g. https://api.deepseek.com; /chat/completions is appended
	Key string
}

// jsonAnswer replaces the structured output that `claude -p` enforces with a schema: chat completions only promise
// some JSON object, so the prompt names the shape.
const jsonAnswer = "\n\nОтвет — только JSON-объект, без пояснений и без markdown: " +
	`{"facts": [{"content": "…", "keywords": ["…"]}], "dropped": "…", "outdated": []}` + "\n"

// Tokens is the usage a call reports: an endpoint has no price, so its cost is counted from tokens; `claude -p` reports
// both (#471).
type Tokens struct {
	Input    int `json:"input"`
	Cached   int `json:"cached,omitempty"` // part of Input served from the provider's prompt cache
	Output   int `json:"output"`
	Thinking int `json:"thinking,omitempty"` // part of Output the model spent reasoning, as `claude -p` reports it
}

// Add sums usage over calls.
func (t *Tokens) Add(o Tokens) {
	t.Input += o.Input
	t.Cached += o.Cached
	t.Output += o.Output
	t.Thinking += o.Thinking
}

// Endpoint is where a model call goes: an OpenAI-compatible base URL with its key and model, or, with no URL, the
// `claude -p` on this machine, where only the model matters.
type Endpoint struct {
	URL   string // base URL, e.g. https://api.deepseek.com; /chat/completions is appended
	Model string
	Key   string
	// Quick asks for an answer without reasoning where the endpoint is known to take that request: DeepSeek reasons by
	// default, and on a split call 97–99 % of the output tokens were reasoning (#232). Other endpoints get the plain
	// request, since an unknown field may be refused.
	Quick bool
}

func runAPI(ctx context.Context, ep Endpoint, system, input string) (reply, error) {
	request := map[string]any{
		"model": ep.Model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": input},
		},
		"response_format": map[string]string{"type": "json_object"},
	}
	if ep.Quick {
		// The review and the suspect calls judge an answer that is already written, so they must give the same verdict
		// on the same input: without a temperature the endpoint samples, and a wrong retire sticks (#296).
		request["temperature"] = 0
		if deepSeek(ep.URL) {
			request["thinking"] = map[string]string{"type": "disabled"}
		}
	}
	body, err := json.Marshal(request)
	if err != nil {
		return reply{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(ep.URL, "/")+"/chat/completions",
		bytes.NewReader(body))
	if err != nil {
		return reply{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if ep.Key != "" {
		req.Header.Set("Authorization", "Bearer "+ep.Key)
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

// deepSeek reports whether url is the DeepSeek API, which takes the thinking switch.
func deepSeek(url string) bool {
	u, err := neturl.Parse(url)
	return err == nil && strings.EqualFold(u.Hostname(), "api.deepseek.com")
}
