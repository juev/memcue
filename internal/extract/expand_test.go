package extract

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// roundTrip answers every request with one chat completion and keeps the last request body.
type roundTrip struct {
	content string
	body    []byte
	host    string
}

func (rt *roundTrip) RoundTrip(r *http.Request) (*http.Response, error) {
	rt.body, _ = io.ReadAll(r.Body)
	rt.host = r.URL.Hostname()
	answer, _ := json.Marshal(rt.content)
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Request: r,
		Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":` + string(answer) +
			`},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":20}}`))}, nil
}

func useTransport(t *testing.T, rt http.RoundTripper) {
	t.Helper()
	old := http.DefaultTransport
	http.DefaultTransport = rt
	t.Cleanup(func() { http.DefaultTransport = old })
}

// ExpandFacts reads the queries of each fact and asks DeepSeek not to reason; another endpoint gets the plain request.
func TestExpandFactsAsksWithoutReasoning(t *testing.T) {
	oldAPI, oldModel := API, Model
	t.Cleanup(func() { API, Model = oldAPI, oldModel })
	rt := &roundTrip{content: `{"facts":[{"id":" 01a ","queries":[" прокси ","","reverse proxy"]}]}`}
	useTransport(t, rt)

	API.URL, API.Key, Model = "https://api.deepseek.com", "k", "deepseek-flash"
	r, err := ExpandFacts(context.Background(), `{"facts":[{"id":"01a","text":"Caddy-private монтирует файл."}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Facts) != 1 || r.Facts[0].ID != "01a" || len(r.Facts[0].Queries) != 2 || r.Facts[0].Queries[0] != "прокси" {
		t.Fatalf("facts %+v", r.Facts)
	}
	var req map[string]any
	if err := json.Unmarshal(rt.body, &req); err != nil {
		t.Fatal(err)
	}
	if rt.host != "api.deepseek.com" || req["thinking"] == nil {
		t.Errorf("DeepSeek request without the thinking switch: %s", rt.body)
	}

	API.URL = "https://openrouter.ai/api/v1"
	if _, err := ExpandFacts(context.Background(), `{"facts":[]}`); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rt.body), `"thinking"`) {
		t.Errorf("another endpoint got the DeepSeek switch: %s", rt.body)
	}
}
