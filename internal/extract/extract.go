// Package extract holds memcue's model calls: a memory note turned into atomic facts for migrate, the search queries
// of a stored fact, and the dream run by hand. They go through `claude -p` with structured output, or through an
// OpenAI-compatible endpoint when one is configured (API). Hooks are switched off for the `claude -p` call, so the
// model sees no stored memory and cannot copy facts from it. No session is read: the agent writes its facts itself
// (#494).
package extract

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Fact is one fact the model extracted. A session's facts also say what they are and what they rest on; a note's
// facts leave those empty.
type Fact struct {
	Content  string   `json:"content"`
	Keywords []string `json:"keywords"`
	Kind     string   `json:"kind,omitempty"`     // rule, decision, pitfall, environment, or another word the policy drops
	Scope    string   `json:"scope,omitempty"`    // global, host:<name>, repo (the session's project), service:<name>
	Source   string   `json:"source,omitempty"`   // user, user_confirmed, tool, agent
	Evidence string   `json:"evidence,omitempty"` // the words of the record the fact rests on, verbatim
	Queries  []string `json:"queries,omitempty"`  // how a user would ask about the fact; recall searches them
	// Translation is the fact in the other language, Russian or English; decode moves it into Queries under
	// TranslationPrefix, so the store needs no field of its own (#395).
	Translation string `json:"translation,omitempty"`
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

// Model is the model of the calls (the main one, by the user's decision).
var Model = "claude-opus-5-5"

// NotePromptVersion is the version of note_prompt.md. It is 0 for the prompt that produced the migrated base;
// bump it whenever the prompt changes, so migrate -all can tell a note that predates the change.
const NotePromptVersion = 0

// Note extracts the facts of a memory note. name is the note's path, for the model's orientation only.
func Note(ctx context.Context, name, content string) (Result, error) {
	return run(ctx, notePrompt, fmt.Sprintf("Заметка `%s`:\n\n%s", name, content))
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
	tokens  Tokens
	// api and ttft are the time `claude -p` says the API took and took to its first token; an endpoint has neither.
	api, ttft time.Duration
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
	if cmd.Env, err = claudeEnv(); err != nil {
		return reply{}, err
	}
	cmd.Stdin = strings.NewReader(input)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		return reply{}, fmt.Errorf("claude: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return envelope(out.Bytes())
}

// ClaudeTokenFile is the file of a Claude Code OAuth token (MEMCUE_CLAUDE_TOKEN_FILE) that `claude -p` runs with
// instead of its own login, which can expire on a machine nobody logs in to (#591).
var ClaudeTokenFile string

// claudeEnv is the environment of `claude -p`: memcue's own, nil, unless ClaudeTokenFile names a file, whose token
// then replaces any CLAUDE_CODE_OAUTH_TOKEN. A leading ~/ is the home directory, since the env block of
// settings.json expands nothing; a missing file is no token, since that block is shared with machines without one.
func claudeEnv() ([]string, error) {
	path := ClaudeTokenFile
	if path == "" {
		return nil, nil
	}
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("MEMCUE_CLAUDE_TOKEN_FILE: %w", err)
		}
		path = filepath.Join(home, rest)
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("MEMCUE_CLAUDE_TOKEN_FILE: %w", err)
	}
	token := strings.TrimSpace(string(b))
	if token == "" {
		return nil, fmt.Errorf("MEMCUE_CLAUDE_TOKEN_FILE: %s holds no token", path)
	}
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "CLAUDE_CODE_OAUTH_TOKEN=") {
			env = append(env, kv)
		}
	}
	return append(env, "CLAUDE_CODE_OAUTH_TOKEN="+token), nil
}

// envelope takes the answer out of the JSON `claude -p` prints.
func envelope(b []byte) (reply, error) {
	var env struct {
		IsError          bool            `json:"is_error"`
		Result           string          `json:"result"`
		StructuredOutput json.RawMessage `json:"structured_output"`
		TotalCostUSD     float64         `json:"total_cost_usd"`
		DurationAPIMS    int64           `json:"duration_api_ms"`
		TTFTMS           int64           `json:"ttft_ms"`
		Usage            struct {
			Input         int `json:"input_tokens"`
			CacheCreation int `json:"cache_creation_input_tokens"`
			CacheRead     int `json:"cache_read_input_tokens"`
			Output        int `json:"output_tokens"`
			Details       struct {
				Thinking int `json:"thinking_tokens"`
			} `json:"output_tokens_details"`
		} `json:"usage"`
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
	u := env.Usage
	return reply{payload: payload, cost: env.TotalCostUSD,
		tokens: Tokens{Input: u.Input + u.CacheCreation + u.CacheRead, Cached: u.CacheRead, Output: u.Output,
			Thinking: u.Details.Thinking},
		api: time.Duration(env.DurationAPIMS) * time.Millisecond, ttft: time.Duration(env.TTFTMS) * time.Millisecond}, nil
}

func decode(rp reply) (Result, error) {
	var r Result
	if err := json.Unmarshal(rp.payload, &r); err != nil {
		return Result{}, fmt.Errorf("facts: %w", err)
	}
	r.CostUSD, r.Tokens = rp.cost, rp.tokens
	for i := range r.Facts {
		r.Facts[i].Content = strings.TrimSpace(r.Facts[i].Content)
		r.Facts[i].Queries = WithTranslation(CleanQueries(r.Facts[i].Queries), r.Facts[i].Translation)
		r.Facts[i].Translation = ""
	}
	if len(r.Facts) == 0 && r.Dropped == "" {
		return r, errEmpty
	}
	return r, nil
}
