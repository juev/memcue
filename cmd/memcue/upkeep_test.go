package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/juev/memcue/internal/store"
)

// sessionCardContext runs session-card and returns what it hands the host, "" when it prints nothing.
func sessionCardContext(t *testing.T, cwd string) string {
	t.Helper()
	in, _ := json.Marshal(map[string]string{"session_id": "s-upkeep", "source": "startup", "cwd": cwd})
	var out bytes.Buffer
	if err := runSessionCard(bytes.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	if out.Len() == 0 {
		return ""
	}
	var resp struct {
		HookSpecificOutput struct {
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("output %q: %v", out.String(), err)
	}
	return resp.HookSpecificOutput.AdditionalContext
}

// Facts written under the directory name of the session's repository before #343 are named on the card of both
// hosts with the exact pair that moves them, and the line goes once they are moved (#396).
func TestCardNamesTheOldNameOfTheSessionRepository(t *testing.T) {
	dir := env(t)
	repo := filepath.Join(dir, "shop")
	key := gitRepo(t, repo)
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddWith("Магазин собирается через make.", nil, "shop 2026-10-01", "", store.Meta{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddWith("Правило: в магазине без force-push.", nil, "x 2026-10-02", "",
		store.Meta{Kind: "rule", Scope: "repo:shop", Source: "user"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddWith("Другой проект, другое имя.", nil, "garden 2026-10-03", "", store.Meta{}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	pair := "-apply shop=" + key
	if got := sessionStartContext(t, repo, "startup", "s-old"); !strings.Contains(got, "2 facts still carry \"shop\"") ||
		!strings.Contains(got, pair) {
		t.Errorf("session start does not name the old name:\n%s", got)
	}
	if got := sessionCardContext(t, repo); !strings.Contains(got, pair) {
		t.Errorf("session card does not name the old name:\n%s", got)
	}
	if got := sessionStartContext(t, dir, "startup", "s-outside"); strings.Contains(got, "old name") {
		t.Errorf("a session outside the repository is told about it:\n%s", got)
	}

	if err := runMigrateRepos(nil, []string{"shop=" + key}, true, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if got := sessionStartContext(t, repo, "startup", "s-moved"); strings.Contains(got, "old name") {
		t.Errorf("the line stays after the facts moved:\n%s", got)
	}
}

// A base in a git repository without the merge driver is named on the card until memcue install sets it up; an
// ignored base, which git never merges, is not (#396).
func TestCardAsksForInstallUntilTheDriverIsSetUp(t *testing.T) {
	env(t)
	dir := t.TempDir()
	for _, kv := range gitEnv(dir) {
		k, v, _ := strings.Cut(kv, "=")
		t.Setenv(k, v)
	}
	repo := filepath.Join(dir, "claude")
	run(t, dir, nil, "git", "init", "-q", "-b", "main", repo)
	t.Setenv("MEMCUE_FACTS", filepath.Join(repo, "memory", "facts.db"))
	seedUpkeepBase(t)

	if got := sessionCardContext(t, ""); !strings.Contains(got, "`memcue install`") {
		t.Errorf("no install line for a base without the driver:\n%s", got)
	}
	if err := runInstall(nil, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if got := sessionStartContext(t, "", "startup", "s-installed"); strings.Contains(got, "memcue install") {
		t.Errorf("the install line stays after install:\n%s", got)
	}

	if err := os.WriteFile(filepath.Join(repo, ".git", "config"), []byte("[core]\n\tbare = false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("memory/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := sessionCardContext(t, ""); strings.Contains(got, "memcue install") {
		t.Errorf("an ignored base is asked to install the driver:\n%s", got)
	}
}

// A base of the old format synced through git is asked on the card to move to text; the line goes with the move,
// which also routes the fact files to the driver (#530).
func TestCardAsksToMoveASyncedBaseToText(t *testing.T) {
	env(t)
	dir := t.TempDir()
	for _, kv := range gitEnv(dir) {
		k, v, _ := strings.Cut(kv, "=")
		t.Setenv(k, v)
	}
	repo := filepath.Join(dir, "claude")
	run(t, dir, nil, "git", "init", "-q", "-b", "main", repo)
	t.Setenv("MEMCUE_FACTS", filepath.Join(repo, "memory", "facts.db"))
	seedUpkeepBase(t)
	if got := sessionCardContext(t, ""); strings.Contains(got, "migrate -text") {
		t.Errorf("a base without the driver is asked to move before it is asked to install:\n%s", got)
	}
	if err := runInstall(nil, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if got := sessionCardContext(t, ""); !strings.Contains(got, "`memcue migrate -text`") {
		t.Errorf("no move line for a synced base of the old format:\n%s", got)
	}
	if err := runMigrateText(&bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if got := sessionCardContext(t, ""); strings.Contains(got, "migrate -text") || strings.Contains(got, "memcue install") {
		t.Errorf("the line stays after the move:\n%s", got)
	}
}

func seedUpkeepBase(t *testing.T) {
	t.Helper()
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddWith("Факт для проверки драйвера.", nil, "x 2026-10-01", "", store.Meta{}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}
