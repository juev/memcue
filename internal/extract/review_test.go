package extract

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Review uses the main model: the extraction endpoint, or `claude -p` without one; there is no separate review
// endpoint.
func TestReviewUsesTheExtractionEndpoint(t *testing.T) {
	oldAPI, oldModel := API, Model
	t.Cleanup(func() { API, Model = oldAPI, oldModel })
	t.Setenv("PATH", t.TempDir())
	API.URL, API.Key, Model = "https://api.example", "k1", "main-model"
	if !ReviewOn() {
		t.Error("an endpoint is configured but ReviewOn is false")
	}
	API.URL, API.Key = "", ""
	if ReviewOn() {
		t.Error("no endpoint and no claude, but ReviewOn is true")
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	if !ReviewOn() {
		t.Error("claude is on PATH, but ReviewOn is false")
	}
}

// A contradiction with an approved fact is a question for a human, not an automatic drop: the approved fact can
// itself be false or stale.
func TestReviewPromptSendsContradictionsToUnsure(t *testing.T) {
	if !strings.Contains(reviewPrompt, "противоречие") || !strings.Contains(reviewPrompt, "`unsure`") {
		t.Error("review prompt must send a contradiction to unsure")
	}
	if strings.Contains(reviewPrompt, "противоречие одобренному факту — тоже\n  `drop`") {
		t.Error("review prompt still drops a contradiction")
	}
}

// Review reads the verdicts it knows and folds the rest to unsure: a word the code cannot place never approves a fact.
func TestReviewReadsDecisions(t *testing.T) {
	oldAPI, oldModel := API, Model
	t.Cleanup(func() { API, Model = oldAPI, oldModel })
	answer, _ := json.Marshal("```json\n{\"decisions\":[" +
		"{\"id\":\"01a\",\"verdict\":\" KEEP \",\"reason\":\"rule with a cause\"}," +
		"{\"id\":\"01b\",\"verdict\":\"MAYBE\",\"reason\":\"?\"}," +
		"{\"id\":\"01c\",\"verdict\":\"drop\",\"reason\":\"journal\",\"expires\":\"2026-11-01\"}," +
		"{\"id\":\"01d\",\"verdict\":\"split\",\"reason\":\"two claims\",\"parts\":[\" Port 3000. \",\" \",\"Path /srv.\"]}]}\n```")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var req struct {
			Model    string `json:"model"`
			Messages []struct{ Role, Content string }
		}
		if err := json.Unmarshal(b, &req); err != nil || req.Model != "cheap" ||
			len(req.Messages) != 2 || req.Messages[0].Role != "system" {
			t.Errorf("request %s", b)
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":`+string(answer)+`},"finish_reason":"stop"}],`+
			`"usage":{"prompt_tokens":700,"completion_tokens":40}}`)
	}))
	t.Cleanup(srv.Close)
	API.URL, API.Key, Model = srv.URL, "k", "cheap"

	r, err := Review(context.Background(), `{"candidates":[{"id":"01a","text":"x"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Decisions) != 4 {
		t.Fatalf("decisions %+v", r.Decisions)
	}
	if d := r.Decisions[0]; d.Verdict != KeepVerdict || d.Reason != "rule with a cause" {
		t.Errorf("keep %+v", d)
	}
	if d := r.Decisions[1]; d.Verdict != "maybe" {
		t.Errorf("an unknown verdict must stay unknown for the caller to read as unsure: %+v", d)
	}
	if d := r.Decisions[2]; d.Verdict != DropVerdict || d.Expires != "2026-11-01" {
		t.Errorf("drop %+v", d)
	}
	if d := r.Decisions[3]; d.Verdict != SplitVerdict || len(d.Parts) != 2 || d.Parts[0] != "Port 3000." ||
		d.Parts[1] != "Path /srv." {
		t.Errorf("split must keep the non-empty parts, trimmed: %+v", d)
	}
	if r.Tokens != (Tokens{Input: 700, Output: 40}) {
		t.Errorf("tokens %+v", r.Tokens)
	}
}
