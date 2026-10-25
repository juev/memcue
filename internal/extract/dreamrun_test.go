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
	answer, tokens, err := DreamAnswer(context.Background(), packet)
	if err != nil {
		t.Fatal(err)
	}
	if string(answer) != `{"packet":"p1","translations":[]}` || tokens.Input != 900 || tokens.Output != 40 {
		t.Errorf("answer %s, tokens %+v", answer, tokens)
	}
}

// Without an endpoint there is nobody to send the packet to: the session's model runs the dream instead.
func TestDreamAnswerNeedsAnEndpoint(t *testing.T) {
	useEndpoint(t, "")
	if _, _, err := DreamAnswer(context.Background(), "packet"); !errors.Is(err, ErrNoEndpoint) {
		t.Errorf("err %v, want ErrNoEndpoint", err)
	}
}
