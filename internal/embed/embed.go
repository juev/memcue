// Package embed turns text into vectors through an OpenAI-compatible embeddings endpoint.
//
// The feature is off unless an endpoint and a model are named in the environment: a long-lived server may point it at
// a local Ollama or Text Embeddings Inference, a laptop at a hosted router (OpenRouter, RouterAI, OpenAI itself).
// Nothing here is required for memcue to work: recall falls back to the words on any error, and the endpoint is then
// left alone for a while (60 s, doubling to 15 min) so a dead server does not cost a timeout on every turn.
package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// PauseFirst and PauseMax bound the time a failed endpoint is left alone.
	PauseFirst = 60 * time.Second
	PauseMax   = 15 * time.Minute
	// DefaultTimeout keeps the call inside a hook that today answers in milliseconds.
	DefaultTimeout = 2 * time.Second
)

// ErrPaused is returned, without a network call, while the endpoint is left alone after a failure.
var ErrPaused = errors.New("embeddings endpoint is paused after a failure")

// Settings describe the endpoint. A nil *Settings means the feature is off.
type Settings struct {
	URL       string        // base address, e.g. https://openrouter.ai/api/v1
	Model     string        // embedding model name
	KeyEnv    string        // name of the environment variable holding the API key
	Space     string        // optional label of the vector space, for a server that changes model under one name
	Timeout   time.Duration // one request
	StateFile string        // where the pause is remembered between processes; empty keeps it in memory
}

// FromEnv reads MEMCUE_EMBED_URL, MEMCUE_EMBED_MODEL, MEMCUE_EMBED_KEY_ENV, MEMCUE_EMBED_SPACE and
// MEMCUE_EMBED_TIMEOUT. It returns nil when no endpoint or no model is named, which is the default.
func FromEnv() *Settings {
	raw, model := strings.TrimSpace(os.Getenv("MEMCUE_EMBED_URL")), strings.TrimSpace(os.Getenv("MEMCUE_EMBED_MODEL"))
	if raw == "" || model == "" {
		return nil
	}
	s := &Settings{
		URL:     strings.TrimRight(raw, "/"),
		Model:   model,
		KeyEnv:  envOr("MEMCUE_EMBED_KEY_ENV", "OPENAI_API_KEY"),
		Space:   strings.TrimSpace(os.Getenv("MEMCUE_EMBED_SPACE")),
		Timeout: DefaultTimeout,
	}
	if v := os.Getenv("MEMCUE_EMBED_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d >= 200*time.Millisecond && d <= 10*time.Second {
			s.Timeout = d
		}
	}
	return s
}

// Tag names the vector space: two vectors may be compared only when their tags are equal. Changing the model, the
// address or the space makes every stored vector foreign, and they are made again by the embed command.
func (s *Settings) Tag() string {
	where := s.URL
	if parts, err := url.Parse(s.URL); err == nil && parts.Host != "" {
		where = strings.ToLower(parts.Scheme) + "://" + strings.ToLower(parts.Host) + strings.TrimRight(parts.Path, "/")
		if parts.RawQuery != "" {
			where += "?" + hashLabel(parts.RawQuery)
		}
	}
	tag := where + "/" + s.Model
	if s.Space != "" {
		tag += "#" + s.Space
	}
	return tag
}

// Client talks to one endpoint. It is safe for concurrent use, though a memcue process is usually short-lived.
type Client struct {
	set  *Settings
	http *http.Client
	key  string

	mu       sync.Mutex
	pausedTo time.Time
	pause    time.Duration
}

// New builds a client for the settings; the key is read once from the named environment variable.
func New(s *Settings) *Client {
	return &Client{
		set:  s,
		http: &http.Client{Timeout: s.Timeout},
		key:  strings.TrimSpace(os.Getenv(s.KeyEnv)),
	}
}

// Tag names the vector space of the client.
func (c *Client) Tag() string { return c.set.Tag() }

// Model names the embedding model of the client.
func (c *Client) Model() string { return c.set.Model }

// Paused reports whether the endpoint is being left alone, and for how much longer.
func (c *Client) Paused() (bool, time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.paused()
}

// paused must be called with the lock held.
func (c *Client) paused() (bool, time.Duration) {
	left := c.left()
	if left <= 0 {
		c.pausedTo = time.Time{}
		return false, 0
	}
	return true, left
}

// left is the remaining pause, from memory and from the state file a previous process may have written.
func (c *Client) left() time.Duration {
	until := c.pausedTo
	if c.set.StateFile != "" {
		if b, err := os.ReadFile(c.set.StateFile); err == nil {
			if t, err := time.Parse(time.RFC3339, strings.TrimSpace(string(b))); err == nil && t.After(until) {
				until = t
			}
		}
	}
	return time.Until(until)
}

// Embed asks the endpoint for the vectors of texts, in the same order. An error means the caller must stay with the
// words; nothing about it is fatal.
func (c *Client) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	c.mu.Lock()
	if paused, left := c.paused(); paused {
		c.mu.Unlock()
		return nil, fmt.Errorf("%w: %s left", ErrPaused, left.Round(time.Second))
	}
	c.mu.Unlock()

	body, err := json.Marshal(request{Model: c.set.Model, Input: texts})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.set.URL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.key != "" {
		req.Header.Set("Authorization", "Bearer "+c.key)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, c.fail(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var answer response
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(&answer); err != nil && resp.StatusCode == http.StatusOK {
		return nil, c.fail(fmt.Errorf("decode answer: %w", err))
	}
	if resp.StatusCode != http.StatusOK {
		msg := resp.Status
		if answer.Error != nil && answer.Error.Message != "" {
			msg = answer.Error.Message
		}
		return nil, c.fail(fmt.Errorf("endpoint answered %s", msg))
	}
	if answer.Error != nil && answer.Error.Message != "" {
		return nil, c.fail(errors.New(answer.Error.Message))
	}
	if len(answer.Data) != len(texts) {
		return nil, c.fail(fmt.Errorf("endpoint returned %d vectors for %d texts", len(answer.Data), len(texts)))
	}
	out := make([][]float32, len(texts))
	for i, d := range answer.Data {
		at := d.Index
		if at < 0 || at >= len(texts) {
			at = i
		}
		if len(d.Embedding) == 0 {
			return nil, c.fail(fmt.Errorf("endpoint returned an empty vector at %d", at))
		}
		out[at] = Normalize(d.Embedding)
	}
	for i, v := range out {
		if len(v) != len(out[0]) {
			return nil, c.fail(fmt.Errorf("vectors of different size in one answer (%d and %d)", len(out[0]), len(out[i])))
		}
	}
	c.ok()
	return out, nil
}

// fail records a failure: the endpoint is left alone for a while, doubling up to PauseMax.
func (c *Client) fail(err error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pause < PauseFirst {
		c.pause = PauseFirst
	} else if c.pause *= 2; c.pause > PauseMax {
		c.pause = PauseMax
	}
	c.pausedTo = time.Now().Add(c.pause)
	if c.set.StateFile != "" {
		if err := os.MkdirAll(filepath.Dir(c.set.StateFile), 0o700); err == nil {
			_ = os.WriteFile(c.set.StateFile, []byte(c.pausedTo.UTC().Format(time.RFC3339)), 0o600)
		}
	}
	return err
}

// ok clears the pause: a working endpoint is not held back by an old failure.
func (c *Client) ok() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pause, c.pausedTo = 0, time.Time{}
	if c.set.StateFile != "" {
		_ = os.Remove(c.set.StateFile)
	}
}

// Normalize scales a vector to unit length so that a similarity is a plain dot product.
func Normalize(values []float32) []float32 {
	var sum float64
	for _, v := range values {
		sum += float64(v) * float64(v)
	}
	if sum == 0 || math.IsInf(sum, 0) || math.IsNaN(sum) {
		return values
	}
	norm := math.Sqrt(sum)
	out := make([]float32, len(values))
	for i, v := range values {
		out[i] = float32(float64(v) / norm)
	}
	return out
}

type request struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type response struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func envOr(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}

// hashLabel keeps a query string (which may hold a deployment or a key) out of the tag as written.
func hashLabel(s string) string {
	const hex = "0123456789abcdef"
	sum := uint64(1469598103934665603)
	for i := 0; i < len(s); i++ {
		sum ^= uint64(s[i])
		sum *= 1099511628211
	}
	b := make([]byte, 8)
	for i := range b {
		b[i] = hex[(sum>>(uint(i)*8))&0xf]
	}
	return string(b)
}

// ParseMargin reads a positive float from the environment, keeping the fallback when it is missing or malformed.
func ParseMargin(name string, fallback float64) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(os.Getenv(name)), 64)
	if err != nil || v <= 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return fallback
	}
	return v
}

// ParseScore reads an optional absolute similarity floor: 0, the default, leaves the decision to the margin alone.
func ParseScore(name string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(os.Getenv(name)), 64)
	if err != nil || v <= 0 || v > 1 || math.IsNaN(v) {
		return 0
	}
	return v
}

// ParseCount reads a positive integer from the environment; 0, the default, means the caller's own limit.
func ParseCount(name string) int {
	v, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if err != nil || v <= 0 {
		return 0
	}
	return v
}
