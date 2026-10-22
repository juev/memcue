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

// SplitFacts sends the split prompt and reads the compound facts, with their parts trimmed and empty ones dropped.
func TestSplitFactsReadsTheAnswer(t *testing.T) {
	oldAPI, oldModel := API, Model
	t.Cleanup(func() { API, Model = oldAPI, oldModel })
	answer, _ := json.Marshal(`{"splits":[{"id":" 01a ","parts":[" Port 3000. ","","Path /srv."],"reason":" two claims "}]}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var req struct {
			Messages []struct{ Role, Content string }
		}
		if err := json.Unmarshal(b, &req); err != nil || len(req.Messages) != 2 ||
			!strings.HasPrefix(req.Messages[0].Content, splitPrompt) {
			t.Errorf("request %s", b)
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":`+string(answer)+`},"finish_reason":"stop"}],`+
			`"usage":{"prompt_tokens":300,"completion_tokens":20}}`)
	}))
	t.Cleanup(srv.Close)
	API.URL, API.Key, Model = srv.URL, "k", "cheap"

	r, err := SplitFacts(context.Background(), `{"facts":[{"id":"01a","text":"Port 3000; path /srv."}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Splits) != 1 {
		t.Fatalf("splits %+v", r.Splits)
	}
	if s := r.Splits[0]; s.ID != "01a" || s.Reason != "two claims" || len(s.Parts) != 2 || s.Parts[0] != "Port 3000." ||
		s.Parts[1] != "Path /srv." {
		t.Errorf("split %+v", s)
	}
	if r.Tokens != (Tokens{Input: 300, Output: 20}) {
		t.Errorf("tokens %+v", r.Tokens)
	}
}
