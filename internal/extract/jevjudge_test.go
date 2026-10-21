package extract

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/juev/memcue/internal/judge"
)

// jevEndpoint serves one decisions answer and records what was asked. answers maps a question key to the noul it
// should return.
func jevEndpoint(t *testing.T, status int, answers map[string]float64, asked *[]map[string]any) {
	t.Helper()
	judge.Shared.Reset()
	t.Cleanup(judge.Shared.Reset)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		if err := json.Unmarshal(raw, &req); err != nil {
			t.Errorf("request is not JSON: %s", raw)
		}
		if asked != nil {
			*asked = append(*asked, req)
		}
		w.WriteHeader(status)
		if status != http.StatusOK {
			_, _ = io.WriteString(w, `{"detail":"boom"}`)
			return
		}
		out := map[string]any{}
		for k, p := range answers {
			related := 0.08
			if p+related > 1 {
				related = 0
			}
			out[k] = map[string]any{"type": "score",
				"probabilities": map[string]float64{"0": p, "1": related, "2": 1 - p - related}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "typesafe/jev-1.14", "answers": out,
			"usage": map[string]any{"input_tokens": 41384, "cost": 0.001738}})
	}))
	t.Cleanup(srv.Close)
	old := JevJudge
	JevJudge.URL, JevJudge.Model, JevJudge.Key = srv.URL, "typesafe/jev-1.14", "k"
	JevJudge.Min, JevJudge.MaxFacts, JevJudge.LongContext = 0.9, 400, false
	t.Cleanup(func() { JevJudge = old })
}

// The strongest match wins, and every approved fact gets its own question: a choice would name one and drop the
// others, and a base that holds three copies of a fact needs all three.
func TestJudgeDuplicateTakesTheStrongestMatch(t *testing.T) {
	var asked []map[string]any
	jevEndpoint(t, 200, map[string]float64{"a1": 0.12, "a2": 0.96, "a3": 0.94}, &asked)
	approved := []JudgeFact{{ID: "f1", Text: "первый"}, {ID: "f2", Text: "второй"}, {ID: "f3", Text: "третий"}}
	v, err := JudgeDuplicate(context.Background(), JudgeFact{ID: "c", Text: "кандидат"}, approved)
	if err != nil {
		t.Fatal(err)
	}
	if v.P != 0.96 || v.HitID != "f2" || v.Related != 0.08 {
		t.Errorf("verdict %+v, want f2 0.96 with related 0.08", v)
	}
	if v.Tokens.Input != 41384 || v.Cost != 0.001738 {
		t.Errorf("usage %+v", v)
	}
	if len(asked) != 1 {
		t.Fatalf("requests %d", len(asked))
	}
	questions, _ := asked[0]["questions"].(map[string]any)
	if len(questions) != len(approved) {
		t.Fatalf("questions %d, want one per approved fact (%d)", len(questions), len(approved))
	}
	for _, k := range []string{"a1", "a2", "a3"} {
		q, ok := questions[k].(map[string]any)
		levels, _ := q["criteria"].([]any)
		if !ok || q["type"] != "score" || len(levels) != 3 {
			t.Errorf("question %s: %v", k, questions[k])
		}
	}
	state, _ := asked[0]["state"].(map[string]any)
	if state["candidate"] != "кандидат" || !strings.Contains(state["approved"].(string), "[3] третий") {
		t.Errorf("state %v", state)
	}
}

// A duplicate found at the second fact is still reported: the regression the choice shape causes.
func TestJudgeDuplicateReportsASecondFact(t *testing.T) {
	jevEndpoint(t, 200, map[string]float64{"a1": 0.02, "a2": 0.97}, nil)
	approved := []JudgeFact{{ID: "f1", Text: "другое"}, {ID: "f2", Text: "то же"}}
	v, err := JudgeDuplicate(context.Background(), JudgeFact{ID: "c", Text: "то же самое"}, approved)
	if err != nil {
		t.Fatal(err)
	}
	if v.P != 0.97 || v.HitID != "f2" {
		t.Errorf("verdict %+v", v)
	}
}

// An endpoint that fails decides nothing and says so.
func TestJudgeDuplicateErrors(t *testing.T) {
	jevEndpoint(t, 500, nil, nil)
	_, err := JudgeDuplicate(context.Background(), JudgeFact{ID: "c", Text: "x"},
		[]JudgeFact{{ID: "f1", Text: "y"}})
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("err %v", err)
	}
}

// One dead endpoint means no role pays another timeout: a failed duplicate call pauses triage too.
func TestSharedPauseStopsTriageAfterDuplicateFailure(t *testing.T) {
	jevEndpoint(t, 500, nil, nil)
	if _, err := JudgeDuplicate(context.Background(), JudgeFact{ID: "c", Text: "x"},
		[]JudgeFact{{ID: "f1", Text: "y"}}); err == nil {
		t.Fatal("duplicate call should fail")
	}
	if _, err := TriageGroup(context.Background(), []string{"факт"}); !errors.Is(err, judge.ErrPaused) {
		t.Fatalf("triage after a failed duplicate = %v, want ErrPaused", err)
	}
}

// An empty base asks nothing: there is nothing to be a duplicate of, and a request would cost money for no answer.
func TestJudgeDuplicateWithoutApprovedFacts(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	t.Cleanup(srv.Close)
	old := JevJudge
	JevJudge.URL, JevJudge.Model, JevJudge.Key = srv.URL, "m", "k"
	t.Cleanup(func() { JevJudge = old })
	v, err := JudgeDuplicate(context.Background(), JudgeFact{ID: "c", Text: "x"}, nil)
	if err != nil || v.P != 0 || called {
		t.Errorf("verdict %+v err %v called %v", v, err, called)
	}
}

func TestJevJudgeSettings(t *testing.T) {
	old := JevJudge
	t.Cleanup(func() { JevJudge = old })
	JevJudge.URL, JevJudge.Model, JevJudge.Key = "u", "m", ""
	JevJudge.Min, JevJudge.MaxFacts = 0, 0
	if JevJudgeOn() {
		t.Error("on without a key")
	}
	JevJudge.Key = "k"
	if !JevJudgeOn() {
		t.Error("off with all three")
	}
	if JevJudgeMin() != jevMin || JevJudgeMaxFacts() != jevMaxFacts {
		t.Errorf("defaults %v %v", JevJudgeMin(), JevJudgeMaxFacts())
	}
	JevJudge.Min, JevJudge.MaxFacts = 0.7, 900
	if JevJudgeMin() != 0.7 || JevJudgeMaxFacts() != 900 {
		t.Errorf("configured %v %v", JevJudgeMin(), JevJudgeMaxFacts())
	}
	if _, err := JudgeDuplicate(context.Background(), JudgeFact{ID: "c", Text: "x"}, nil); err != nil {
		t.Errorf("unexpected error %v", err)
	}
}
