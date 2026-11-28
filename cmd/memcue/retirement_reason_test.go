package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCaptureRejectsSameScopedRetiredClaim(t *testing.T) {
	dir := env(t)
	const claim = "Acme requires ENABLE_HOOK before rendering."
	const reason = "The current renderer removed the hook."
	var out bytes.Buffer
	args := []string{"-scope", "repo:acme", "--", claim}
	if err := runAdd(args, &out); err != nil {
		t.Fatal(err)
	}
	id := strings.TrimSpace(out.String())
	if err := runRetire([]string{"-reason", reason, id}, io.Discard); err != nil {
		t.Fatal(err)
	}
	check := func(err error) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), reason) || !strings.Contains(err.Error(), id) || strings.Contains(err.Error(), claim) {
			t.Fatalf("expected a scoped warning with reason and no retired text; got %v", err)
		}
	}
	check(runAdd(args, io.Discard))
	out.Reset()
	if err := runAdd([]string{"-scope", "repo:acme", "--", "Acme retains a stable image cache."}, &out); err != nil {
		t.Fatal(err)
	}
	other := strings.TrimSpace(out.String())
	check(runCorrect([]string{"-expected-version", "1", other, "--", claim}, io.Discard))
	if got := storedFact(t, other); got.Version != 1 {
		t.Fatalf("rejected correction wrote a new version: %+v", got)
	}
	batch := filepath.Join(dir, "batch.json")
	if err := os.WriteFile(batch, []byte(`[{"Content":"Beta uses a dedicated renderer."},{"Content":"`+claim+`","Scope":"repo:acme"}]`), 0600); err != nil {
		t.Fatal(err)
	}
	check(runAdd([]string{"-batch", batch}, io.Discard))
	if err := runAdd([]string{"-scope", "repo:beta", "--", claim}, io.Discard); err != nil {
		t.Fatalf("different scope was blocked: %v", err)
	}
	out.Reset()
	if err := runSearch([]string{"Beta dedicated renderer"}, &out); err != nil || strings.Contains(out.String(), "dedicated renderer") {
		t.Fatalf("rejected batch partly committed: %v, %s", err, &out)
	}
}

func TestForgottenTextKeepsReasonWithoutDisclosingItOnSave(t *testing.T) {
	dir := env(t)
	text := filepath.Join(dir, "facts")
	if err := os.Mkdir(text, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MEMCUE_FACTS", text)
	const claim = "Acme archives the renderer records verbatim."
	id := addFactForTest(t, "-scope", "global", "--", claim)
	const reason = "User requested forgetting."
	if err := runRetire([]string{"-reason", reason, id}, io.Discard); err != nil {
		t.Fatal(err)
	}
	// Rebuild the SQLite projection: the reason must be in the canonical history.
	cache, err := cachePath(factsBase())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(cache); err != nil {
		t.Fatal(err)
	}
	if got := factHistoryFromBase(t, id); got != reason {
		t.Fatalf("canonical text history lost the reason: %q", got)
	}
	if err := runAdd([]string{"-scope", "global", "--", claim}, io.Discard); err == nil ||
		!strings.Contains(err.Error(), "forgotten fact") || strings.Contains(err.Error(), id) || strings.Contains(err.Error(), claim) || strings.Contains(err.Error(), reason) {
		t.Fatalf("unexpected disclosure for forgotten match: %v", err)
	}
}

func factHistoryFromBase(t *testing.T, id string) string {
	t.Helper()
	inv := newInvocation()
	defer inv.close()
	var out bytes.Buffer
	if err := inv.runHistory([]string{"-json", id}, &out); err != nil {
		t.Fatal(err)
	}
	var facts []struct {
		RetirementReason string `json:"retirement_reason"`
	}
	if err := json.Unmarshal(out.Bytes(), &facts); err != nil || len(facts) != 2 {
		t.Fatalf("unexpected history: %v %q", err, &out)
	}
	return facts[1].RetirementReason
}

func TestDreamIsNotExposed(t *testing.T) {
	if strings.Contains(usage, "\n  dream ") || strings.Contains(usage, "MEMCUE_DREAM_") || strings.Contains(usage, "-dream-tools") {
		t.Fatal("help still exposes the retired Dream interface")
	}
}

func TestRetireRecordsReasonAndRejectsBlank(t *testing.T) {
	env(t)
	var out bytes.Buffer
	if err := runAdd([]string{"The Acme renderer requires sRGB assets."}, &out); err != nil {
		t.Fatal(err)
	}
	id := strings.Fields(out.String())[0]
	if err := runRetire([]string{id}, io.Discard); err == nil {
		t.Fatal("missing reason was accepted")
	}
	if err := runRetire([]string{"-reason", "  ", id}, io.Discard); err == nil {
		t.Fatal("blank reason was accepted")
	}
	if got := storedFact(t, id); got.Status != "active" || got.Version != 1 {
		t.Fatalf("blank reason changed the fact: %+v", got)
	}
	const reason = "The renderer now converts assets to sRGB on import."
	if err := runRetire([]string{"-expected-version", "1", "-reason", reason, id}, io.Discard); err != nil {
		t.Fatalf("retiring with a reason: %v", err)
	}
	inv := newInvocation()
	defer inv.close()
	out.Reset()
	if err := inv.runHistory([]string{id}, &out); err != nil || !strings.Contains(out.String(), reason) {
		t.Fatalf("history lacks retirement reason: %q, %v", out.String(), err)
	}
	if err := runReopen([]string{id}, io.Discard); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := inv.runHistory([]string{id}, &out); err != nil || strings.Count(out.String(), reason) != 1 {
		t.Fatalf("reopen should keep reason only on retirement: %q, %v", out.String(), err)
	}
}
