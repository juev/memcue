// Package extract turns text (a memory note or a session transcript) into atomic facts with the main model, through
// `claude -p` with structured output, or through an OpenAI-compatible endpoint when one is configured (API). Hooks are
// switched off for the `claude -p` call, so the model sees no stored memory and cannot copy facts from it.
package extract

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Fact is one fact the model extracted.
type Fact struct {
	Content  string   `json:"content"`
	Keywords []string `json:"keywords"`
}

// Result is the model's answer.
type Result struct {
	Facts   []Fact `json:"facts"`
	Dropped string `json:"dropped"` // what the source held that no fact kept, and why
	// Outdated numbers (from 1) the facts of earlier parts of a session this part disproves or replaces.
	Outdated []int   `json:"outdated"`
	CostUSD  float64 // as `claude -p` reports it; an endpoint reports Tokens instead
	Tokens   Tokens
}

//go:embed note_prompt.md
var notePrompt string

const schema = `{"type":"object","additionalProperties":false,"required":["facts","dropped"],"properties":{
"facts":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["content","keywords"],
"properties":{"content":{"type":"string"},"keywords":{"type":"array","items":{"type":"string"}}}}},
"dropped":{"type":"string"},"outdated":{"type":"array","items":{"type":"integer"}}}}`

var errEmpty = errors.New("model returned neither facts nor a reason")

// Model is the model the extraction runs on (the main one, by the user's decision).
var Model = "claude-opus-5-5"

// Note extracts the facts of a memory note. name is the note's path, for the model's orientation only.
func Note(ctx context.Context, name, content string) (Result, error) {
	return run(ctx, notePrompt, fmt.Sprintf("Заметка `%s`:\n\n%s", name, content))
}

//go:embed session_prompt.md
var sessionPrompt string

// Session extracts the facts of a rendered session transcript. where names the repository and date of the session,
// for the model's orientation only. For a later part of a long session, prior holds the facts of the earlier parts:
// the model returns only what is new and names the prior facts that are outdated, and the result holds the session's
// whole list. Prior facts stay unless named, so a model that forgets to repeat them loses nothing.
func Session(ctx context.Context, where, text string, prior []Fact) (Result, error) {
	input := fmt.Sprintf("Сессия (%s):\n\n%s", where, text)
	if len(prior) > 0 {
		var b strings.Builder
		b.WriteString("Факты из предыдущих частей сессии:\n")
		for i, f := range prior {
			fmt.Fprintf(&b, "%d. %s\n", i+1, f.Content)
		}
		input = b.String() + "\nСледующая часть. " + input
	}
	r, err := run(ctx, sessionPrompt, input)
	if errors.Is(err, errEmpty) {
		err = nil // a part that teaches nothing is the common case
	}
	if err != nil {
		return r, err
	}
	r.Facts = mergeParts(prior, r.Facts, r.Outdated)
	return r, nil
}

// mergeParts keeps the prior facts not named outdated (numbers from 1) and appends the new ones.
func mergeParts(prior, fresh []Fact, outdated []int) []Fact {
	drop := map[int]bool{}
	for _, n := range outdated {
		drop[n] = true
	}
	var out []Fact
	for i, f := range prior {
		if !drop[i+1] {
			out = append(out, f)
		}
	}
	return append(out, fresh...)
}

func run(ctx context.Context, system, input string) (Result, error) {
	if API.URL != "" {
		return runAPI(ctx, system, input)
	}
	dir, err := os.MkdirTemp("", "memcue-extract-")
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	cmd := exec.CommandContext(ctx, "claude", "-p", "--model", Model, "--tools", "", "--no-session-persistence",
		"--settings", `{"disableAllHooks": true}`, "--system-prompt", system, "--json-schema", schema,
		"--output-format", "json")
	cmd.Dir = dir // no project CLAUDE.md, no repository
	cmd.Stdin = strings.NewReader(input)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		return Result{}, fmt.Errorf("claude: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return parse(out.Bytes())
}

func parse(b []byte) (Result, error) {
	var env struct {
		IsError          bool            `json:"is_error"`
		Result           string          `json:"result"`
		StructuredOutput json.RawMessage `json:"structured_output"`
		TotalCostUSD     float64         `json:"total_cost_usd"`
	}
	if err := json.Unmarshal(b, &env); err != nil {
		return Result{}, fmt.Errorf("claude output: %w", err)
	}
	if env.IsError {
		return Result{}, fmt.Errorf("claude: %s", env.Result)
	}
	payload := env.StructuredOutput
	if len(payload) == 0 || string(payload) == "null" {
		payload = json.RawMessage(strings.TrimSpace(env.Result))
	}
	var r Result
	if err := json.Unmarshal(payload, &r); err != nil {
		return Result{}, fmt.Errorf("facts: %w", err)
	}
	r.CostUSD = env.TotalCostUSD
	for i := range r.Facts {
		r.Facts[i].Content = strings.TrimSpace(r.Facts[i].Content)
	}
	if len(r.Facts) == 0 && r.Dropped == "" {
		return r, errEmpty
	}
	return r, nil
}
