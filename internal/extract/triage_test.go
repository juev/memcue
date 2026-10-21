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

func triageEndpoint(t *testing.T, status int, answer string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/decisions" || r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("path %q auth %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		b, _ := io.ReadAll(r.Body)
		var req struct {
			Model     string            `json:"model"`
			State     map[string]string `json:"state"`
			Questions map[string]struct {
				Type     string            `json:"type"`
				Criteria map[string]string `json:"criteria"`
			} `json:"questions"`
		}
		if err := json.Unmarshal(b, &req); err != nil || req.Model != "jev" || req.Questions["q"].Type != "choice" ||
			len(req.Questions["q"].Criteria) != 3 || req.State["facts"] != "1) первый факт\n2) второй факт" {
			t.Errorf("request %s", b)
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, answer)
	}))
	t.Cleanup(srv.Close)
	old := JevJudge
	JevJudge.URL, JevJudge.Model, JevJudge.Key = srv.URL+"/api/v1/decisions", "jev", "k"
	t.Cleanup(func() { JevJudge = old })
}

func TestTriageGroupSumsActionProbabilities(t *testing.T) {
	triageEndpoint(t, 200, `{"answers":{"q":{"type":"choice","choice":"none",
		"probabilities":{"duplicate":0.15,"outdated":0.1,"none":0.75}}},"usage":{"input_tokens":356}}`)
	r, err := TriageGroup(context.Background(), []string{"первый  факт", "второй факт"})
	if err != nil {
		t.Fatal(err)
	}
	if r.PAct < 0.249 || r.PAct > 0.251 || r.Input != 356 {
		t.Errorf("got %+v", r)
	}
}

func TestTriageGroupErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		answer string
		want   string
	}{
		"status":    {502, `bad gateway`, "502"},
		"not json":  {200, `<html>`, "decisions output"},
		"no choice": {200, `{"answers":{}}`, "no answers"},
	} {
		t.Run(name, func(t *testing.T) {
			triageEndpoint(t, tc.status, tc.answer)
			if _, err := TriageGroup(context.Background(), []string{"первый факт", "второй факт"}); err == nil ||
				!strings.Contains(err.Error(), tc.want) {
				t.Errorf("err %v, want %q", err, tc.want)
			}
		})
	}
}
