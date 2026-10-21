package extract

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The judge runs on the extraction endpoint and model unless it is given its own: the default is one model for both,
// and a cheaper one is a setting away.
func TestReviewEndpointFallsBackToTheExtraction(t *testing.T) {
	oldAPI, oldModel, oldJudge := API, Model, Judge
	t.Cleanup(func() { API, Model, Judge = oldAPI, oldModel, oldJudge })
	API.URL, API.Key, Model = "https://api.example", "k1", "main-model"
	Judge = struct {
		URL   string
		Model string
		Key   string
	}{}
	if ep := reviewEndpoint(); ep.URL != "https://api.example" || ep.Model != "main-model" || ep.Key != "k1" {
		t.Errorf("default endpoint %+v", ep)
	}
	if !ReviewOn() {
		t.Error("an endpoint is configured but ReviewOn is false")
	}
	Judge.URL, Judge.Model, Judge.Key = "https://judge.example", "cheap", "k2"
	if ep := reviewEndpoint(); ep.URL != "https://judge.example" || ep.Model != "cheap" || ep.Key != "k2" {
		t.Errorf("own endpoint %+v", ep)
	}

	API.URL, API.Key = "", ""
	Judge = struct {
		URL   string
		Model string
		Key   string
	}{}
	if ReviewOn() {
		t.Error("no endpoint, but ReviewOn is true")
	}
}

// A review endpoint on another host must not receive the extraction key just because Judge.Key is empty.
func TestReviewEndpointDoesNotLeakTheExtractionKeyToAnotherHost(t *testing.T) {
	oldAPI, oldModel, oldJudge := API, Model, Judge
	t.Cleanup(func() { API, Model, Judge = oldAPI, oldModel, oldJudge })
	API.URL, API.Key, Model = "https://api.example/v1", "k1", "main-model"
	Judge.URL, Judge.Model, Judge.Key = "https://judge.example/v1", "cheap", ""
	if ep := reviewEndpoint(); ep.Key != "" {
		t.Errorf("another host received the extraction key: %+v", ep)
	}
	Judge.URL = "https://api.example/v2"
	if ep := reviewEndpoint(); ep.Key != "k1" {
		t.Errorf("same host should keep the extraction key: %+v", ep)
	}
	Judge.Key = "k2"
	if ep := reviewEndpoint(); ep.Key != "k2" {
		t.Errorf("the review key should win: %+v", ep)
	}
}

// Review reads the verdicts it knows and folds the rest to unsure: a word the code cannot place never approves a fact.
func TestReviewReadsDecisions(t *testing.T) {
	oldAPI, oldModel, oldJudge := API, Model, Judge
	t.Cleanup(func() { API, Model, Judge = oldAPI, oldModel, oldJudge })
	answer, _ := json.Marshal("```json\n{\"decisions\":[" +
		"{\"id\":\"01a\",\"verdict\":\" KEEP \",\"reason\":\"rule with a cause\"}," +
		"{\"id\":\"01b\",\"verdict\":\"MAYBE\",\"reason\":\"?\"}," +
		"{\"id\":\"01c\",\"verdict\":\"drop\",\"reason\":\"journal\",\"expires\":\"2026-11-01\"}]}\n```")
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
	API.URL, API.Key, Model = "", "", "main-model"
	Judge.URL, Judge.Model, Judge.Key = srv.URL, "cheap", "k"

	r, err := Review(context.Background(), `{"candidates":[{"id":"01a","text":"x"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Decisions) != 3 {
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
	if r.Tokens != (Tokens{Input: 700, Output: 40}) {
		t.Errorf("tokens %+v", r.Tokens)
	}
}
