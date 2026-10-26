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

// The packet goes to the endpoint whole as the user message, and the model may reason over it: the request carries no
// thinking switch and no temperature. The answer comes back without its fence, with the tokens it took (#401).
func TestDreamAnswerSendsThePacket(t *testing.T) {
	const packet = "# memcue dream\n\n## Форма ответа\n\n`packet` — ровно `p1`."
	content, _ := json.Marshal("```json\n{\"packet\":\"p1\",\"translations\":[]}\n```")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var req struct {
			Messages    []struct{ Role, Content string }
			Temperature *float64          `json:"temperature"`
			Thinking    map[string]string `json:"thinking"`
		}
		if err := json.Unmarshal(b, &req); err != nil || len(req.Messages) != 2 || req.Messages[1].Content != packet ||
			!strings.Contains(req.Messages[0].Content, "memcue dream") || req.Temperature != nil || req.Thinking != nil {
			t.Errorf("request %s", b)
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":`+string(content)+`},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":900,"completion_tokens":40}}`)
	}))
	t.Cleanup(srv.Close)
	useEndpoint(t, srv.URL)
	r, err := DreamAnswer(context.Background(), packet)
	if err != nil {
		t.Fatal(err)
	}
	if string(r.Answer) != `{"packet":"p1","translations":[]}` || r.Tokens.Input != 900 || r.Tokens.Output != 40 {
		t.Errorf("answer %s, tokens %+v", r.Answer, r.Tokens)
	}
}

// The schema `claude -p` holds a dream answer to is the answer -apply reads: every part, and the fields an action and a
// split need.
func TestDreamSchemaIsTheAnswer(t *testing.T) {
	var schema struct {
		Required   []string                   `json:"required"`
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal([]byte(dreamSchema), &schema); err != nil {
		t.Fatal(err)
	}
	want := []string{"packet", "splits", "expansions", "actions", "translations"}
	if strings.Join(schema.Required, ",") != strings.Join(want, ",") || len(schema.Properties) != len(want) {
		t.Errorf("schema requires %q with %d properties, want %q", schema.Required, len(schema.Properties), want)
	}
	for _, field := range []string{`"by"`, `"kind"`, `"parts"`, `"translation"`, `"queries"`} {
		if !strings.Contains(dreamSchema, field) {
			t.Errorf("schema lacks %s", field)
		}
	}
}
