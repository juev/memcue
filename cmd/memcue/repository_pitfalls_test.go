package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/juev/memcue/internal/store"
)

func pitfallFixture(t *testing.T) (string, string) {
	t.Helper()
	dir := env(t)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(dir, "global"))
	t.Setenv("GIT_CONFIG_SYSTEM", filepath.Join(dir, "system"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("MEMCUE_DREAM_BACKGROUND", "0")
	t.Setenv("MEMCUE_FACTS", filepath.Join(dir, "facts"))
	repo := filepath.Join(dir, "repo")
	for _, args := range [][]string{{"init", "-q", repo}, {"-C", repo, "remote", "add", "origin", "https://example.invalid/team/repo.git"}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git setup: %v: %s", err, out)
		}
	}
	return dir, repo
}

func putPitfall(t *testing.T, content, expires string, meta store.Meta) store.Fact {
	t.Helper()
	s, err := openBase()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	f, err := s.AddWith(content, "example.invalid/team/repo 2026-11-07", expires, meta)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func lifecycleContext(t *testing.T, cwd, source string, card bool) string {
	t.Helper()
	in, _ := json.Marshal(sessionStartInput{SessionID: "fixture", CWD: cwd, Source: source})
	var out bytes.Buffer
	var err error
	if card {
		err = runSessionCardWith(bytes.NewReader(in), &out, false)
	} else {
		err = runSessionStartPart(bytes.NewReader(in), &out, 1)
	}
	if err != nil {
		t.Fatal(err)
	}
	if out.Len() == 0 {
		return ""
	}
	var response struct {
		HookSpecificOutput struct {
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response.HookSpecificOutput.AdditionalContext
}

func TestRepositoryPitfallsExcludeExpired(t *testing.T) {
	_, repo := pitfallFixture(t)
	f := putPitfall(t, "Expired failure had a workaround.", time.Now().AddDate(0, 0, -1).Format("2006-01-02"), store.Meta{Kind: "pitfall", Scope: "repo:example.invalid/team/repo"})
	if strings.Contains(lifecycleContext(t, repo, "startup", false), f.Content) {
		t.Fatal("expired pitfall was automatically delivered")
	}
}

// Supplemental controls for selection already provided by the first implementation.
func TestRepositoryPitfallsSelectionControls(t *testing.T) {
	_, repo := pitfallFixture(t)
	for _, kind := range []string{"rule", "decision", "environment", "procedure", "event"} {
		putPitfall(t, "EXCLUDE-"+kind, "", store.Meta{Kind: kind, Scope: "repo:example.invalid/team/repo"})
	}
	for _, scope := range []string{"global", "host:fixture", "shelf:research", "repo:example.invalid/other/repo", ""} {
		putPitfall(t, "EXCLUDE-"+scope, "", store.Meta{Kind: "pitfall", Scope: scope})
	}
	var want []string
	for i := 0; i < 12; i++ {
		content := fmt.Sprintf("Pitfall %d failed because of a stale fixture; %s regenerate it.", i, strings.Repeat("full condition ", 100))
		putPitfall(t, content, "", store.Meta{Kind: "pitfall", Scope: "repo:example.invalid/team/repo"})
		want = append(want, content)
	}
	got := lifecycleContext(t, repo, "startup", false)
	if strings.Contains(got, "EXCLUDE-") {
		t.Fatal("non-repository-pitfall scope/kind leaked")
	}
	for _, content := range want {
		if !strings.Contains(got, content) {
			t.Fatal("eligible whole fact missing from unbounded delivery")
		}
	}
}

func TestRepositoryPitfallsCardLifecycle(t *testing.T) {
	_, repo := pitfallFixture(t)
	f := putPitfall(t, "Card fixture failed because its index was stale; rebuild it.", "", store.Meta{Kind: "pitfall", Scope: "repo:example.invalid/team/repo"})
	for _, source := range []string{"startup", "compact"} {
		if !strings.Contains(lifecycleContext(t, repo, source, true), f.Content) {
			t.Fatalf("%s SessionCard omitted repository pitfall", source)
		}
	}
}

// The pitfalls of a text history come from its cache (#709). Another adapter with its own state directory keeps another
// cache of the same history; a correction it appends must still reach this session's pitfalls, as #686 required.
func TestRepositoryPitfallsSeeAnotherCachesCorrection(t *testing.T) {
	dir, repo := pitfallFixture(t)
	meta := store.Meta{Kind: "pitfall", Scope: "repo:example.invalid/team/repo"}
	f := putPitfall(t, "COPPER-CONTENT: the fixture deploy needs the copper gate.", "", meta)
	if got := lifecycleContext(t, repo, "startup", false); !strings.Contains(got, "COPPER-CONTENT") {
		t.Fatal("the first cache did not deliver the pitfall")
	}
	own := os.Getenv("MEMCUE_STATE_DIR")
	t.Setenv("MEMCUE_STATE_DIR", filepath.Join(dir, "other-adapter-state"))
	s, err := openBase()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Correct(f.ID, "AMBER-CONTENT: the fixture deploy needs the amber gate."); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MEMCUE_STATE_DIR", own)
	for _, card := range []bool{false, true} {
		got := lifecycleContext(t, repo, "startup", card)
		if !strings.Contains(got, "AMBER-CONTENT") || strings.Contains(got, "COPPER-CONTENT") {
			t.Fatalf("card=%v: the other cache's correction did not reach the pitfalls:\n%s", card, got)
		}
	}
}

func TestRepositoryPitfallsSwitchSnapshot(t *testing.T) {
	dir, repo := pitfallFixture(t)
	f := putPitfall(t, "Old repository failed because its index was stale; rebuild it.", "", store.Meta{Kind: "pitfall", Scope: "repo:example.invalid/team/repo"})
	first := lifecycleContext(t, repo, "startup", true)
	if !strings.Contains(first, f.Content) {
		t.Fatal("initial delivery missing")
	}
	if !strings.Contains(first, "example.invalid/team/repo") || !strings.Contains(first, "only to this repository") {
		t.Fatal("pitfall context does not bind applicability to its repository")
	}
	if out, err := exec.Command("git", "-C", repo, "remote", "set-url", "origin", "https://example.invalid/team/other.git").CombinedOutput(); err != nil {
		t.Fatalf("switch setup: %v: %s", err, out)
	}
	next := lifecycleContext(t, repo, "startup", true)
	if strings.Contains(next, f.Content) || !strings.Contains(next, "supersedes") || !strings.Contains(next, "No active repository pitfalls") {
		t.Fatal("empty new repository snapshot did not supersede old applicability")
	}
	if out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").CombinedOutput(); err == nil {
		t.Fatalf("non-git fixture inside git: %s", out)
	}
	if got := lifecycleContext(t, dir, "startup", true); strings.Contains(got, f.Content) {
		t.Fatal("non-git directory loaded pitfalls")
	}
}

func TestRepositoryPitfallsVersionWorktreeControls(t *testing.T) {
	dir, repo := pitfallFixture(t)
	meta := store.Meta{Kind: "pitfall", Scope: "repo:example.invalid/team/repo"}
	old := putPitfall(t, "OBSOLETE-CONTENT stale workaround.", "", meta)
	retired := putPitfall(t, "RETIRED-CONTENT failed workaround.", "", meta)
	today := putPitfall(t, "Valid through today because the fixture clock uses UTC; retry today.", time.Now().UTC().Format("2006-01-02"), meta)
	s, err := openBase()
	if err != nil {
		t.Fatal(err)
	}
	latest, err := s.Correct(old.ID, "Current failure cause; use the corrected full workaround.")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Retire(retired.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(dir, "linked")
	for _, args := range [][]string{{"-C", repo, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--allow-empty", "-qm", "fixture"}, {"-C", repo, "worktree", "add", "-q", "-b", "fixture-linked", linked}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("linked setup: %v: %s", err, out)
		}
	}
	for _, cwd := range []string{repo, linked} {
		for _, source := range []string{"startup", "compact"} {
			for _, card := range []bool{false, true} {
				got := lifecycleContext(t, cwd, source, card)
				for _, f := range []store.Fact{latest, today} {
					if !strings.Contains(got, f.Content) {
						t.Fatal("latest eligible fact missing")
					}
				}
				if strings.Contains(got, "OBSOLETE-CONTENT") || strings.Contains(got, "RETIRED-CONTENT") {
					t.Fatal("obsolete version delivered")
				}
			}
		}
	}
}

func TestRepositoryPitfallsExplicitRemoteAlias(t *testing.T) {
	_, repo := pitfallFixture(t)
	if out, err := exec.Command("git", "-C", repo, "remote", "add", "upstream", "https://example.invalid/upstream/repo.git").CombinedOutput(); err != nil {
		t.Fatalf("alias setup: %v: %s", err, out)
	}
	f := putPitfall(t, "Upstream fixture failed because of a stale index; rebuild it in this clone too.", "", store.Meta{Kind: "pitfall", Scope: "repo:example.invalid/upstream/repo"})
	if !strings.Contains(lifecycleContext(t, repo, "startup", true), f.Content) {
		t.Fatal("explicit scope of a current repository remote was omitted")
	}
}

func TestRepositoryPitfallsLocalRepository(t *testing.T) {
	_, repo := pitfallFixture(t)
	if out, err := exec.Command("git", "-C", repo, "remote", "remove", "origin").CombinedOutput(); err != nil {
		t.Fatalf("local setup: %v: %s", err, out)
	}
	f := putPitfall(t, "Local repository failed because generated files were stale; regenerate them.", "", store.Meta{Kind: "pitfall", Scope: "dir:" + repo})
	if !strings.Contains(lifecycleContext(t, repo, "startup", true), f.Content) {
		t.Fatal("actual local git repository lost explicitly scoped pitfalls")
	}
}

func TestRepositoryPitfallsNativeSnapshot(t *testing.T) {
	_, repo := pitfallFixture(t)
	f := putPitfall(t, "Native snapshot failure cause; preserve full workaround.", "", store.Meta{Kind: "pitfall", Scope: "repo:example.invalid/team/repo"})
	input, _ := json.Marshal(sessionStartInput{CWD: repo})
	var out bytes.Buffer
	if err := runSessionPitfallsJSON(bytes.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	var snapshot struct {
		Facts []string `json:"facts"`
	}
	if err := json.Unmarshal(out.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Facts) != 1 || snapshot.Facts[0] != f.Content {
		t.Fatal("native function-hook snapshot omitted whole persisted repository fact")
	}
}

func TestRepositoryPitfallsSnapshotReadError(t *testing.T) {
	dir, repo := pitfallFixture(t)
	broken := filepath.Join(dir, "broken.db")
	if err := os.WriteFile(broken, []byte("synthetic invalid database"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MEMCUE_FACTS", broken)
	input, _ := json.Marshal(sessionStartInput{CWD: repo})
	var out bytes.Buffer
	if err := runSessionPitfallsJSON(bytes.NewReader(input), &out); err == nil {
		t.Fatal("snapshot read failure was reported as a successful empty transport")
	}
	if out.Len() != 0 {
		t.Fatal("failed snapshot emitted partial context")
	}
}
