// Package decisions speaks the typed decisions API of TypeSafe's System One models (Jev). A request carries the state
// and a set of typed questions; the answer carries probabilities instead of prose. RouterAI, OpenRouter and TypeSafe
// speak the same contract, only the URL and the model differ.
package decisions

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Question is one typed question about the state. Type is "noul", "choice", "score" or "box": a noul answers with the
// probability of yes; choice takes Criteria as an object keyed by option, score as a list of levels, and both answer
// with a distribution.
type Question struct {
	Type         string `json:"type"`
	Instructions string `json:"instructions"`
	Criteria     any    `json:"criteria,omitempty"`
}

// Answer is what the model returned for one question. Only the fields of its own type are set.
type Answer struct {
	Type          string             `json:"type"`
	Noul          float64            `json:"noul"`
	Choice        string             `json:"choice"`
	Probabilities map[string]float64 `json:"probabilities"`
	Confidence    float64            `json:"confidence"`
}

// Result is one answer set: the answers by question key, the model that answered and what the call cost.
type Result struct {
	Model   string
	Answers map[string]Answer
	Input   int     // input tokens the endpoint reports
	Cost    float64 // dollars, when the endpoint reports a cost
}

// Client is one decisions endpoint. It is on only when the URL, the model and the key are all set.
type Client struct {
	URL         string // the full endpoint, e.g. https://openrouter.ai/api/alpha/decisions
	Model       string // e.g. typesafe/jev-1.14
	Key         string // bearer token
	LongContext bool   // TypeSafe-Danger-Zone-Long-Context: 262k in one request, slower and less accurate
	HTTP        *http.Client
}

// On says whether the client can send anything.
func (c Client) On() bool { return c.URL != "" && c.Model != "" && c.Key != "" }

// Ask sends the state and every question in one request. The state goes as given: a string, an object or an array.
func (c Client) Ask(ctx context.Context, state any, questions map[string]Question) (Result, error) {
	if !c.On() {
		return Result{}, fmt.Errorf("decisions: endpoint, model or key is not set")
	}
	if len(questions) == 0 {
		return Result{}, fmt.Errorf("decisions: no questions")
	}
	body, err := json.Marshal(map[string]any{"model": c.Model, "state": state, "questions": questions})
	if err != nil {
		return Result{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL, bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Key)
	if c.LongContext {
		req.Header.Set("TypeSafe-Danger-Zone-Long-Context", "true")
	}
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Result{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("decisions %s: %s", resp.Status, head(raw, 300))
	}
	var env struct {
		Model   string            `json:"model"`
		Answers map[string]Answer `json:"answers"`
		Usage   struct {
			Input int     `json:"input_tokens"`
			Cost  float64 `json:"cost"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return Result{}, fmt.Errorf("decisions output: %w", err)
	}
	if len(env.Answers) == 0 {
		return Result{}, fmt.Errorf("decisions: no answers in %s", head(raw, 300))
	}
	return Result{Model: env.Model, Answers: env.Answers, Input: env.Usage.Input, Cost: env.Usage.Cost}, nil
}

// head is the first n bytes of a response, for an error that says what the endpoint refused.
func head(raw []byte, n int) string {
	s := strings.TrimSpace(string(raw))
	if len(s) <= n {
		return s
	}
	return s[:n]
}
