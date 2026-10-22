package decisions

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// endpoint serves one answer and checks what the client sent.
func endpoint(t *testing.T, status int, answer string, check func(req map[string]any, header http.Header)) Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		if err := json.Unmarshal(raw, &req); err != nil {
			t.Errorf("request is not JSON: %s", raw)
		}
		if r.Header.Get("Authorization") != "Bearer k" || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("headers %v", r.Header)
		}
		if check != nil {
			check(req, r.Header)
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, answer)
	}))
	t.Cleanup(srv.Close)
	return Client{URL: srv.URL, Model: "typesafe/jev-1.14", Key: "k"}
}

func TestAskParsesNoulAndUsage(t *testing.T) {
	var got map[string]any
	c := endpoint(t, 200, `{"model":"typesafe/jev-1.14-20261014","answers":{"a1":{"type":"noul","noul":0.96},
		"a2":{"type":"noul","noul":0.04}},"usage":{"input_tokens":41384,"cost":0.001738}}`,
		func(req map[string]any, _ http.Header) { got = req })
	res, err := c.Ask(context.Background(),
		map[string]string{"candidate": "текст", "approved": "[1] другой"},
		map[string]Question{"a1": {Type: "noul", Instructions: "same?"}, "a2": {Type: "noul", Instructions: "same?"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Model != "typesafe/jev-1.14-20261014" || res.Input != 41384 || res.Cost != 0.001738 {
		t.Errorf("result %+v", res)
	}
	if res.Answers["a1"].Noul != 0.96 || res.Answers["a2"].Noul != 0.04 || res.Answers["a1"].Type != "noul" {
		t.Errorf("answers %+v", res.Answers)
	}
	if got["model"] != "typesafe/jev-1.14" {
		t.Errorf("model %v", got["model"])
	}
	state, ok := got["state"].(map[string]any)
	if !ok || state["candidate"] != "текст" {
		t.Errorf("state %v", got["state"])
	}
	questions, ok := got["questions"].(map[string]any)
	if !ok || len(questions) != 2 {
		t.Errorf("questions %v", got["questions"])
	}
}

func TestAskChoicesAndConfidence(t *testing.T) {
	c := endpoint(t, 200, `{"answers":{"q":{"type":"choice","choice":"outdated","confidence":0.72,
		"probabilities":{"duplicate":0.1,"outdated":0.7,"none":0.2}}},"usage":{"input_tokens":356}}`, nil)
	res, err := c.Ask(context.Background(), "state", map[string]Question{"q": {Type: "choice", Instructions: "?",
		Criteria: map[string]string{"duplicate": "a", "outdated": "b", "none": "c"}}})
	if err != nil {
		t.Fatal(err)
	}
	a := res.Answers["q"]
	if a.Choice != "outdated" || a.Confidence != 0.72 || a.Probabilities["outdated"] != 0.7 || res.Cost != 0 {
		t.Errorf("answer %+v cost %v", a, res.Cost)
	}
}

func TestAskErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		answer string
		want   string
	}{
		"status":    {422, `{"detail":"Dictionary should have at most 255 items"}`, "255 items"},
		"not json":  {200, `<html>`, "decisions output"},
		"no answer": {200, `{"answers":{}}`, "no answers"},
	} {
		t.Run(name, func(t *testing.T) {
			c := endpoint(t, tc.status, tc.answer, nil)
			_, err := c.Ask(context.Background(), "s", map[string]Question{"a1": {Type: "noul", Instructions: "?"}})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err %v, want %q", err, tc.want)
			}
		})
	}
}

func TestAskNeedsConfigurationAndQuestions(t *testing.T) {
	if _, err := (Client{URL: "u", Model: "m"}).Ask(context.Background(), "s",
		map[string]Question{"a1": {Type: "noul"}}); err == nil {
		t.Error("sent without a key")
	}
	c := Client{URL: "u", Model: "m", Key: "k"}
	if !c.On() {
		t.Error("off with all three")
	}
	if _, err := c.Ask(context.Background(), "s", nil); err == nil {
		t.Error("sent without questions")
	}
}
