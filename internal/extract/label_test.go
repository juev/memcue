package extract

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// LabelFacts keeps the labels the base can hold and clears the rest, so a wrong word never lands in a fact.
func TestLabelFactsClearsWhatTheBaseCannotHold(t *testing.T) {
	oldAPI, oldModel := API, Model
	t.Cleanup(func() { API, Model = oldAPI, oldModel })
	answer, _ := json.Marshal(`{"labels":[` +
		`{"id":" 01a ","kind":" Rule ","scope":"global","expires":null},` +
		`{"id":"01b","kind":"environment","scope":"host:loffe","expires":"2026-11-01"},` +
		`{"id":"01c","kind":"fact","scope":"the whole team","expires":"soon"},` +
		`{"id":"01d","kind":"event","scope":"repo:memcue"}]}`)
	var system string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var req struct {
			Messages []struct{ Role, Content string }
		}
		if err := json.Unmarshal(b, &req); err != nil || len(req.Messages) != 2 {
			t.Errorf("request %s", b)
		} else {
			system = req.Messages[0].Content
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":`+string(answer)+`},"finish_reason":"stop"}],`+
			`"usage":{"prompt_tokens":900,"completion_tokens":60}}`)
	}))
	t.Cleanup(srv.Close)
	API.URL, API.Key, Model = srv.URL, "k", "cheap"

	r, err := LabelFacts(context.Background(), `{"host":"loffe","facts":[{"id":"01a","text":"x"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(system, "размечаешь") || !strings.Contains(system, `"labels"`) {
		t.Errorf("system prompt lacks the label prompt or its shape: %.200q", system)
	}
	want := []Label{
		{ID: "01a", Kind: "rule", Scope: "global"},
		{ID: "01b", Kind: "environment", Scope: "host:loffe", Expires: "2026-11-01"},
		{ID: "01c"},
		{ID: "01d", Kind: "event", Scope: "repo:memcue"},
	}
	if len(r.Labels) != len(want) {
		t.Fatalf("labels %+v", r.Labels)
	}
	for i := range want {
		if r.Labels[i] != want[i] {
			t.Errorf("label %d = %+v, want %+v", i, r.Labels[i], want[i])
		}
	}
	if r.Tokens != (Tokens{Input: 900, Output: 60}) {
		t.Errorf("tokens %+v", r.Tokens)
	}
}
