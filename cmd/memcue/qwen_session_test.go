package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// While Qwen Code's built-in memory is on, the session start of a Qwen session (MEMCUE_AGENT=qwen) says so and how
// to turn it off; the user settings turn the line off. The Claude settings and the Claude environment do not govern
// the Qwen branch.
func TestSessionStartWarnsWhileQwenAutoMemoryIsOn(t *testing.T) {
	dir := env(t)
	t.Setenv("MEMCUE_AGENT", "qwen")
	const warning = "Qwen Code's built-in memory is on"
	if ctx := sessionStartContext(t, dir, "startup", "s1"); !strings.Contains(ctx, warning) {
		t.Errorf("no settings: the warning is missing:\n%s", ctx)
	}
	settings := filepath.Join(qwenDir(), "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		json string
		warn bool
	}{
		{`{"memory": {"enableManagedAutoMemory": true}}`, true},
		{`{"memory": {}}`, true},
		{`{"theme": "dark"}`, true},
		{`{"memory": {"enableManagedAutoMemory": false}}`, false},
	} {
		if err := os.WriteFile(settings, []byte(c.json), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(sessionStartContext(t, dir, "startup", "s1"), warning); got != c.warn {
			t.Errorf("%s: warning %v, want %v", c.json, got, c.warn)
		}
	}
	if err := os.WriteFile(settings, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := sessionStartContext(t, dir, "startup", "s1")
	if !strings.Contains(ctx, settings) || !strings.Contains(ctx, "memory.enableManagedAutoMemory") ||
		!strings.Contains(ctx, "memory.enableManagedAutoDream") {
		t.Errorf("the warning does not name the Qwen settings and both keys:\n%s", ctx)
	}
	if err := os.MkdirAll(claudeDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir(), "settings.json"), []byte(`{"autoMemoryEnabled": false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CODE_DISABLE_AUTO_MEMORY", "1")
	if !strings.Contains(sessionStartContext(t, dir, "startup", "s1"), warning) {
		t.Error("the Claude settings and environment silenced the Qwen warning")
	}
}

// Without MEMCUE_AGENT=qwen the Claude branch stays: the Qwen settings do not silence the Claude warning.
func TestSessionStartClaudeBranchIgnoresQwenSettings(t *testing.T) {
	dir := env(t)
	t.Setenv("CLAUDE_CODE_DISABLE_AUTO_MEMORY", "")
	settings := filepath.Join(qwenDir(), "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte(`{"memory": {"enableManagedAutoMemory": false}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if ctx := sessionStartContext(t, dir, "startup", "s1"); !strings.Contains(ctx, "Claude Code's built-in memory is on") {
		t.Errorf("the Qwen settings silenced the Claude warning:\n%s", ctx)
	}
}

// The no-repository snapshot separates the header from the line, as the repository header does.
func TestSessionPitfallsNoRepositoryHeaderSeparator(t *testing.T) {
	dir := env(t)
	in, _ := json.Marshal(map[string]string{"session_id": "s1", "cwd": dir, "hook_event_name": "UserPromptSubmit"})
	var out bytes.Buffer
	if err := runSessionPitfalls(bytes.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	var resp struct {
		HookSpecificOutput struct {
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	ctx := resp.HookSpecificOutput.AdditionalContext
	if strings.Contains(ctx, "snapshots.No active") || !strings.Contains(ctx, "snapshots.\nNo active repository pitfalls.") {
		t.Errorf("the header and the line are not separated: %q", ctx)
	}
}
