package main

import (
	"bytes"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/juev/memcue/internal/store"
)

// fakeBinary writes a script that records its arguments and base into marker, standing in for the memcue binary.
func fakeBinary(t *testing.T) (exe, marker string) {
	t.Helper()
	dir := t.TempDir()
	marker = filepath.Join(dir, "ran")
	exe = filepath.Join(dir, "memcue")
	script := "#!/bin/sh\necho \"$* $MEMCUE_FACTS\" > " + marker + "\n"
	if err := os.WriteFile(exe, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return exe, marker
}

func waitFile(path string, d time.Duration) ([]byte, bool) {
	for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
			return b, true
		}
	}
	return nil, false
}

func TestStartEmbedRunsEmbedOnTheSameBase(t *testing.T) {
	env(t)
	t.Setenv("MEMCUE_EMBED_URL", "http://127.0.0.1:1/v1")
	t.Setenv("MEMCUE_EMBED_MODEL", "test-model")
	exe, marker := fakeBinary(t)
	startEmbed(exe)
	b, ok := waitFile(marker, 5*time.Second)
	if !ok {
		t.Fatal("the embed process did not run")
	}
	if got, want := strings.TrimSpace(string(b)), "embed "+factsPath(); got != want {
		t.Errorf("ran %q, want %q", got, want)
	}
}

func TestStartEmbedWithoutAnEndpointDoesNothing(t *testing.T) {
	env(t)
	t.Setenv("MEMCUE_EMBED_URL", "")
	t.Setenv("MEMCUE_EMBED_MODEL", "")
	exe, marker := fakeBinary(t)
	startEmbed(exe)
	if _, ok := waitFile(marker, 300*time.Millisecond); ok {
		t.Error("an embed ran without MEMCUE_EMBED_*")
	}
}

// The commands that can leave an active fact without a vector start an embed; reads, retires and the merge driver
// do not.
func TestEmbedWritesAreTheCommandsThatActivateFacts(t *testing.T) {
	for _, cmd := range []string{"add", "correct", "approve", "vet", "dream", "extract-session", "extract-messages"} {
		if !embedWrites[cmd] {
			t.Errorf("%s does not start an embed", cmd)
		}
	}
	for _, cmd := range []string{"hook", "recall", "search", "retire", "merge", "embed", "facts", "candidates"} {
		if embedWrites[cmd] {
			t.Errorf("%s starts an embed", cmd)
		}
	}
}

// A second embed waits for the one running, so the two do not pay for the same facts.
func TestEmbedWaitsForARunningEmbed(t *testing.T) {
	env(t)
	seedFacts(t, 2)
	srv := vectorServer(t, nil)
	defer srv.Close()
	t.Setenv("MEMCUE_EMBED_URL", srv.URL)
	t.Setenv("MEMCUE_EMBED_MODEL", "test-model")

	unlock, err := lockEmbed()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	var out bytes.Buffer
	go func() { done <- runEmbed(nil, &out, &bytes.Buffer{}) }()
	select {
	case err := <-done:
		unlock()
		t.Fatalf("embed did not wait for the lock: %v %q", err, out.String())
	case <-time.After(200 * time.Millisecond):
	}
	unlock()
	select {
	case err := <-done:
		if err != nil || !strings.Contains(out.String(), "embedded 2 facts") {
			t.Errorf("after the lock: %v %q", err, out.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("embed did not finish after the lock was released")
	}
}

// Approving on the review page fills the vectors of the approved facts; rejecting does not.
func TestReviewApprovalStartsAnEmbed(t *testing.T) {
	dir := env(t)
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	a := reviewCandidate(t, s, "Кандидат A про порт 1000.", "environment", "tool")
	b := reviewCandidate(t, s, "Кандидат B про правило.", "rule", "user")
	rs, err := newReviewServer(s, filepath.Join(dir, "review.log"))
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	rs.afterApprove = func() { calls++ }

	if rec := reviewPost(t, rs, "/reject", url.Values{"token": {rs.token}, "id": {b.ID}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("reject: %d", rec.Code)
	}
	if calls != 0 {
		t.Errorf("a rejection started %d embeds", calls)
	}
	if rec := reviewPost(t, rs, "/approve", url.Values{"token": {rs.token}, "id": {a.ID}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("approve: %d", rec.Code)
	}
	if calls != 1 {
		t.Errorf("an approval started %d embeds, want 1", calls)
	}
}
