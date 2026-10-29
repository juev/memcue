package extract

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeClaude puts on PATH a `claude` that answers with the CLAUDE_CODE_OAUTH_TOKEN it was given.
func fakeClaude(t *testing.T) {
	t.Helper()
	fakeClaudeScript(t, "printf '{\"is_error\":false,\"structured_output\":{\"token\":\"%s\"}}' \"$CLAUDE_CODE_OAUTH_TOKEN\"\n")
}

// fakeClaudeScript puts on PATH a `claude` that reads its input and runs body.
func fakeClaudeScript(t *testing.T, body string) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\ncat >/dev/null\n" + body
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// tokenFile sets ClaudeTokenFile for the test.
func tokenFile(t *testing.T, path string) {
	t.Helper()
	old := ClaudeTokenFile
	ClaudeTokenFile = path
	t.Cleanup(func() { ClaudeTokenFile = old })
}

// claudeToken calls the fake `claude -p` and returns the token it saw.
func claudeToken(t *testing.T) (string, error) {
	t.Helper()
	rp, err := callModel(context.Background(), Endpoint{Model: "m"}, "system", "{}", "", "input")
	if err != nil {
		return "", err
	}
	var got struct{ Token string }
	if err := json.Unmarshal(rp.payload, &got); err != nil {
		t.Fatal(err)
	}
	return got.Token, nil
}

// The token of MEMCUE_CLAUDE_TOKEN_FILE reaches `claude -p` and replaces one of the environment (#591).
func TestClaudeTokenFileReachesClaude(t *testing.T) {
	fakeClaude(t)
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "from-env")
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tokenFile(t, path)
	got, err := claudeToken(t)
	if err != nil {
		t.Fatal(err)
	}
	if got != "from-file" {
		t.Errorf("claude -p got token %q, want from-file", got)
	}
}

// A leading ~/ is the home directory: the env block of settings.json expands nothing.
func TestClaudeTokenFileExpandsHome(t *testing.T) {
	fakeClaude(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".config"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".config", "claude-oauth-token"), []byte("at-home"), 0o600); err != nil {
		t.Fatal(err)
	}
	tokenFile(t, "~/.config/claude-oauth-token")
	got, err := claudeToken(t)
	if err != nil {
		t.Fatal(err)
	}
	if got != "at-home" {
		t.Errorf("claude -p got token %q, want at-home", got)
	}
}

// A missing file leaves `claude -p` the environment it had: the setting is shared with machines that have no token.
func TestClaudeTokenFileMissingKeepsEnvironment(t *testing.T) {
	fakeClaude(t)
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "from-env")
	tokenFile(t, filepath.Join(t.TempDir(), "absent"))
	got, err := claudeToken(t)
	if err != nil {
		t.Fatal(err)
	}
	if got != "from-env" {
		t.Errorf("claude -p got token %q, want from-env", got)
	}
}

// A failed `claude -p` names the reason it printed as the result of its JSON, not only its exit status (#590).
func TestClaudeFailureNamesTheResult(t *testing.T) {
	fakeClaudeScript(t, "printf '{\"is_error\":true,\"result\":\"Failed to authenticate: OAuth session expired\"}'\nexit 1\n")
	_, err := claudeToken(t)
	if err == nil || !strings.Contains(err.Error(), "Failed to authenticate: OAuth session expired") {
		t.Errorf("err = %v, want the result of the JSON", err)
	}
}

// Without that JSON the reason is stderr, as before.
func TestClaudeFailureWithoutJSONNamesStderr(t *testing.T) {
	fakeClaudeScript(t, "echo 'unknown option --json-schema' >&2\nexit 2\n")
	_, err := claudeToken(t)
	if err == nil || !strings.Contains(err.Error(), "unknown option --json-schema") {
		t.Errorf("err = %v, want stderr", err)
	}
}

// A file without a token is an error that names it, not a call that fails on the login.
func TestClaudeTokenFileEmptyIsError(t *testing.T) {
	fakeClaude(t)
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte(" \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tokenFile(t, path)
	_, err := claudeToken(t)
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Errorf("err = %v, want an error naming %s", err, path)
	}
}
