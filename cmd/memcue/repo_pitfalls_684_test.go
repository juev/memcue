package main

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/juev/memcue/internal/store"
	"github.com/juev/memcue/internal/tier"
)

// One native lifecycle tracer: persisted repository pitfall -> SessionStart context.
// Keep this regression frozen until the initial delivery seam is green.
func TestSessionStartAutomaticallyDeliversRepositoryPitfall684(t *testing.T) {
	dir := env(t)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(dir, "empty-global.gitconfig"))
	t.Setenv("GIT_CONFIG_SYSTEM", filepath.Join(dir, "empty-system.gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	t.Setenv("MEMCUE_FACTS", filepath.Join(dir, "facts")) // synthetic text history, not a live base
	if out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").CombinedOutput(); err == nil {
		t.Fatalf("fixture must start outside git: %s", out)
	}
	repo := filepath.Join(dir, "checkout")
	for _, args := range [][]string{
		{"init", "--quiet", repo},
		{"-C", repo, "remote", "add", "origin", "https://example.invalid/team/pitfall-fixture.git"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git fixture setup: %v: %s", err, out)
		}
	}
	const key = "example.invalid/team/pitfall-fixture"
	if got := tier.Of(repo, claudeDir()); got != key {
		t.Fatalf("repository identity = %q, want %q", got, key)
	}
	s, err := openBase()
	if err != nil {
		t.Fatal(err)
	}
	const pitfall = "The fixture build failed because its generated schema was stale; regenerate the schema before rebuilding."
	fact, err := s.AddWith(pitfall, key+" 2026-11-07", "", store.Meta{
		Kind: "pitfall", Scope: "repo:" + key, Source: "tool",
	})
	if err != nil {
		_ = s.Close()
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	in, err := json.Marshal(sessionStartInput{SessionID: "pitfall-684", Source: "startup", CWD: repo})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runSessionStartPart(bytes.NewReader(in), &out, 1); err != nil {
		t.Fatal(err)
	}
	var response struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out.Bytes(), &response); err != nil {
		t.Fatalf("decode native lifecycle output: %v", err)
	}
	if response.HookSpecificOutput.HookEventName != "SessionStart" {
		t.Fatalf("unexpected hook event: %q", response.HookSpecificOutput.HookEventName)
	}
	if !strings.Contains(response.HookSpecificOutput.AdditionalContext, fact.Content) {
		t.Fatalf("missing automatic repository pitfall delivery: SessionStart additionalContext does not contain persisted pitfall %q", fact.Content)
	}
}
