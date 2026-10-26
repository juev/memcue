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
)

func endpoint(t *testing.T, status int, answer string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("path %q auth %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		b, _ := io.ReadAll(r.Body)
		var req struct {
			Model    string `json:"model"`
			Messages []struct{ Role, Content string }
		}
		if err := json.Unmarshal(b, &req); err != nil || req.Model != "m" || len(req.Messages) != 2 ||
			!strings.Contains(req.Messages[0].Content, `"facts"`) || !strings.Contains(req.Messages[1].Content, "сессия") {
			t.Errorf("request %s", b)
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, answer)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func useEndpoint(t *testing.T, url string) {
	t.Helper()
	old, oldModel := API, Model
	API.URL, API.Key, Model = url, "k", "m"
	t.Cleanup(func() { API, Model = old, oldModel })
}

func TestEndpointAnswerBecomesFacts(t *testing.T) {
	content, _ := json.Marshal("```json\n{\"facts\":[{\"content\":\" порт 55432 \",\"keywords\":[\"порт\"]}],\"dropped\":\"\"}\n```")
	srv := endpoint(t, 200, `{"choices":[{"message":{"content":`+string(content)+`},"finish_reason":"stop"}],
		"usage":{"prompt_tokens":1000,"completion_tokens":50,"prompt_cache_hit_tokens":600}}`)
	useEndpoint(t, srv.URL+"/")
	r, err := run(context.Background(), "system", "сессия")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Facts) != 1 || r.Facts[0].Content != "порт 55432" || r.Tokens != (Tokens{Input: 1000, Cached: 600, Output: 50}) {
		t.Errorf("got %+v", r)
	}
}

func TestEndpointErrorsAreReported(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		answer string
		want   string
	}{
		"status":    {401, `{"error":"bad key"}`, "401"},
		"cut":       {200, `{"choices":[{"message":{"content":"{"},"finish_reason":"length"}]}`, "token limit"},
		"not json":  {200, `{"choices":[{"message":{"content":"no facts here"},"finish_reason":"stop"}]}`, "facts:"},
		"no choice": {200, `{"choices":[]}`, "no choices"},
	} {
		t.Run(name, func(t *testing.T) {
			useEndpoint(t, endpoint(t, tc.status, tc.answer).URL)
			if _, err := run(context.Background(), "system", "сессия"); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err %v, want %q", err, tc.want)
			}
		})
	}
}

func TestEndpointEmptyAnswerIsEmptySession(t *testing.T) {
	srv := endpoint(t, 200, `{"choices":[{"message":{"content":"{\"facts\":[],\"dropped\":\"\"}"},"finish_reason":"stop"}]}`)
	useEndpoint(t, srv.URL)
	if _, err := run(context.Background(), "system", "сессия"); !errors.Is(err, errEmpty) {
		t.Errorf("err %v", err)
	}
	if r, err := Session(context.Background(), "x", "сессия", nil, ""); err != nil || len(r.Facts) != 0 {
		t.Errorf("session: %+v %v", r, err)
	}
}

func TestLaterPartKeepsPriorFactsUnlessOutdated(t *testing.T) {
	answer, _ := json.Marshal(`{"facts":[{"content":"rsync поставлен через mise","keywords":["rsync"]}],"dropped":"","outdated":[2]}`)
	var input string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var req struct{ Messages []struct{ Content string } }
		_ = json.Unmarshal(b, &req)
		input = req.Messages[1].Content
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":`+string(answer)+`},"finish_reason":"stop"}]}`)
	}))
	t.Cleanup(srv.Close)
	useEndpoint(t, srv.URL)
	prior := []Fact{{Content: "порт 55432"}, {Content: "в контейнере нет rsync"}, {Content: "модель deepseek-flash"}}
	r, err := Session(context.Background(), "x", "сессия", prior, "")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range r.Facts {
		got = append(got, f.Content)
	}
	if strings.Join(got, "|") != "порт 55432|модель deepseek-flash|rsync поставлен через mise" {
		t.Errorf("facts %q", got)
	}
	if !strings.Contains(input, "2. в контейнере нет rsync") {
		t.Errorf("prior facts not numbered in the input: %q", input)
	}
}

func TestMergePartsIgnoresUnknownNumbers(t *testing.T) {
	got := mergeParts([]Fact{{Content: "a"}}, []Fact{{Content: "b"}}, []int{0, 5})
	if len(got) != 2 {
		t.Errorf("got %+v", got)
	}
}

// The review and suspect calls judge text that is already written, so they ask the endpoint not to sample; the
// extraction call writes new text and keeps the endpoint default (#296).
func TestQuickCallAsksForNoSampling(t *testing.T) {
	for _, tc := range []struct {
		quick bool
		want  bool
	}{{true, true}, {false, false}} {
		var fields map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(b, &fields); err != nil {
				t.Errorf("body %s", b)
			}
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"{\"decisions\":[]}"},"finish_reason":"stop"}],
				"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
		}))
		ep := Endpoint{URL: srv.URL, Model: "m", Key: "k", Quick: tc.quick}
		if _, err := runAPI(context.Background(), ep, "system", "input"); err != nil {
			srv.Close()
			t.Fatalf("quick=%v: %v", tc.quick, err)
		}
		srv.Close()
		temperature, ok := fields["temperature"]
		if ok != tc.want {
			t.Fatalf("quick=%v: temperature present %v, want %v (body %v)", tc.quick, ok, tc.want, fields)
		}
		if ok && temperature != float64(0) {
			t.Errorf("quick=%v: temperature = %v, want 0", tc.quick, temperature)
		}
	}
}

// The built-in memory notes go to the model before the session, and the prompt tells it to leave out what they say:
// a paraphrase of a note would be a second copy of it on the card (#456). No notes add nothing.
func TestSessionShowsTheMemoryNotes(t *testing.T) {
	var inputs []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var req struct{ Messages []struct{ Content string } }
		_ = json.Unmarshal(b, &req)
		inputs = append(inputs, req.Messages[1].Content)
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"{\"facts\":[],\"dropped\":\"\"}"},"finish_reason":"stop"}]}`)
	}))
	t.Cleanup(srv.Close)
	useEndpoint(t, srv.URL)
	note := "Навыки держат только процедуры; состояние среды живёт в памяти."
	if _, err := Session(context.Background(), "x", "сессия", nil, note); err != nil {
		t.Fatal(err)
	}
	if _, err := Session(context.Background(), "x", "сессия", nil, "  "); err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 2 || !strings.HasPrefix(inputs[0], "Заметки встроенной памяти") ||
		strings.Index(inputs[0], note) > strings.Index(inputs[0], "Сессия (x)") {
		t.Errorf("notes not before the session: %q", inputs)
	}
	if strings.Contains(inputs[1], "Заметки") {
		t.Errorf("empty notes reached the model: %q", inputs[1])
	}
	if !strings.Contains(sessionPrompt, "заметках встроенной памяти") {
		t.Error("the session prompt does not say what to do with the notes")
	}
}
