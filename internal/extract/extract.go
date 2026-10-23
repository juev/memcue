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

// Fact is one fact the model extracted. A session's facts also say what they are and what they rest on; a note's
// facts leave those empty.
type Fact struct {
	Content  string   `json:"content"`
	Keywords []string `json:"keywords"`
	Kind     string   `json:"kind,omitempty"`     // rule, decision, pitfall, environment, or another word the policy drops
	Scope    string   `json:"scope,omitempty"`    // global, host:<name>, repo:<name>
	Source   string   `json:"source,omitempty"`   // user, user_confirmed, tool, agent
	Evidence string   `json:"evidence,omitempty"` // the words of the record the fact rests on, verbatim
	Queries  []string `json:"queries,omitempty"`  // how a user would ask about the fact; recall searches them
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

// sessionSchema is schema with the fields a session's facts must carry for the policy to judge them.
const sessionSchema = `{"type":"object","additionalProperties":false,"required":["facts","dropped"],"properties":{
"facts":{"type":"array","items":{"type":"object","additionalProperties":false,
"required":["content","keywords","kind","scope","source","evidence"],
"properties":{"content":{"type":"string"},"keywords":{"type":"array","items":{"type":"string"}},
"kind":{"type":"string","enum":["rule","decision","pitfall","environment","event","procedure","repo","other"]},
"scope":{"type":"string"},"source":{"type":"string","enum":["user","user_confirmed","tool","agent"]},
"evidence":{"type":"string"},"queries":{"type":"array","items":{"type":"string"}}}}},
"dropped":{"type":"string"},"outdated":{"type":"array","items":{"type":"integer"}}}}`

const sessionAnswer = "\n\nОтвет — только JSON-объект, без пояснений и без markdown: " +
	`{"facts": [{"content": "…", "keywords": ["…"], "kind": "rule|decision|pitfall|environment|event|procedure|repo|other", ` +
	`"scope": "…", "source": "user|user_confirmed|tool|agent", "evidence": "…", "queries": ["…"]}], "dropped": "…", "outdated": []}` + "\n"

var errEmpty = errors.New("model returned neither facts nor a reason")

// Model is the model the extraction runs on (the main one, by the user's decision).
var Model = "claude-opus-5-5"

// NotePromptVersion is the version of note_prompt.md. It is 0 for the prompt that produced the migrated base;
// bump it whenever the prompt changes, so migrate -all can tell a note that predates the change.
const NotePromptVersion = 0

// Note extracts the facts of a memory note. name is the note's path, for the model's orientation only.
func Note(ctx context.Context, name, content string) (Result, error) {
	return run(ctx, notePrompt, fmt.Sprintf("Заметка `%s`:\n\n%s", name, content))
}

//go:embed session_prompt.md
var sessionPrompt string

// Session extracts the facts of a rendered session transcript. where names the repository, date, host and system of
// the session: the model uses them to name a fact's scope. For a later part of a long session, prior holds the facts of the earlier parts:
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
	rp, err := call(ctx, sessionPrompt, sessionSchema, sessionAnswer, input)
	var r Result
	if err == nil {
		r, err = decode(rp)
	}
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
	rp, err := call(ctx, system, schema, jsonAnswer, input)
	if err != nil {
		return Result{}, err
	}
	return decode(rp)
}

// reply is one answer of the model: the JSON object it gave and what the call cost.
type reply struct {
	payload []byte
	cost    float64 // as `claude -p` reports it
	tokens  Tokens  // as an endpoint reports them
}

// call runs the model once on the extraction endpoint. `claude -p` enforces schema; an endpoint only promises some
// JSON object, so shape is appended to its system prompt to name the object.
func call(ctx context.Context, system, schema, shape, input string) (reply, error) {
	return callModel(ctx, Endpoint{URL: API.URL, Model: Model, Key: API.Key}, system, schema, shape, input)
}

// callModel runs the model once on ep: the endpoint when it has a URL, `claude -p` on ep.Model otherwise.
func callModel(ctx context.Context, ep Endpoint, system, schema, shape, input string) (reply, error) {
	if ep.URL != "" {
		return runAPI(ctx, ep, system+shape, input)
	}
	dir, err := os.MkdirTemp("", "memcue-extract-")
	if err != nil {
		return reply{}, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	cmd := exec.CommandContext(ctx, "claude", "-p", "--model", ep.Model, "--tools", "", "--no-session-persistence",
		"--settings", `{"disableAllHooks": true}`, "--system-prompt", system, "--json-schema", schema,
		"--output-format", "json")
	cmd.Dir = dir // no project CLAUDE.md, no repository
	cmd.Stdin = strings.NewReader(input)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		return reply{}, fmt.Errorf("claude: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return envelope(out.Bytes())
}

// envelope takes the answer out of the JSON `claude -p` prints.
func envelope(b []byte) (reply, error) {
	var env struct {
		IsError          bool            `json:"is_error"`
		Result           string          `json:"result"`
		StructuredOutput json.RawMessage `json:"structured_output"`
		TotalCostUSD     float64         `json:"total_cost_usd"`
	}
	if err := json.Unmarshal(b, &env); err != nil {
		return reply{}, fmt.Errorf("claude output: %w", err)
	}
	if env.IsError {
		return reply{}, fmt.Errorf("claude: %s", env.Result)
	}
	payload := env.StructuredOutput
	if len(payload) == 0 || string(payload) == "null" {
		payload = json.RawMessage(strings.TrimSpace(env.Result))
	}
	return reply{payload: payload, cost: env.TotalCostUSD}, nil
}

func decode(rp reply) (Result, error) {
	var r Result
	if err := json.Unmarshal(rp.payload, &r); err != nil {
		return Result{}, fmt.Errorf("facts: %w", err)
	}
	r.CostUSD, r.Tokens = rp.cost, rp.tokens
	for i := range r.Facts {
		r.Facts[i].Content = strings.TrimSpace(r.Facts[i].Content)
		r.Facts[i].Queries = CleanQueries(r.Facts[i].Queries)
	}
	if len(r.Facts) == 0 && r.Dropped == "" {
		return r, errEmpty
	}
	return r, nil
}
