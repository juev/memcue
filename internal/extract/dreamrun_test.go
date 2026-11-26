package extract

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
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
// split need. The channel task of #625 went with the card (#656).
func TestDreamSchemaIsTheAnswer(t *testing.T) {
	var schema struct {
		Required   []string                   `json:"required"`
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal([]byte(dreamSchema), &schema); err != nil {
		t.Fatal(err)
	}
	want := []string{"packet", "splits", "actions", "scopes"}
	if strings.Join(schema.Required, ",") != strings.Join(want, ",") || len(schema.Properties) != len(want) {
		t.Errorf("schema requires %q with %d properties, want %q", schema.Required, len(schema.Properties), want)
	}
	for _, field := range []string{`"by"`, `"kind"`, `"parts"`, `"scope"`} {
		if !strings.Contains(dreamSchema, field) {
			t.Errorf("schema lacks %s", field)
		}
	}
	if strings.Contains(dreamSchema, "channel") || strings.Contains(DreamInstructions("p"), "канал") {
		t.Error("the dream still asks for channels")
	}
}

func TestDreamResolvedRetirementContract(t *testing.T) {
	for name, text := range map[string]string{
		"endpoint schema": dreamSchema,
		"packet answer":   DreamInstructions("p-802"),
	} {
		if !strings.Contains(text, "resolved") {
			t.Errorf("%s does not admit resolved retirement", name)
		}
	}
	text := strings.Join(strings.Fields(DreamInstructions("p-802")), " ")
	for _, phrase := range []string{
		`kind: "resolved"`,
		"the fact's own text carries the evidence",
		"`by` is optional",
		"another fact in the group",
		"merely because it is old",
		"unused in search",
		"Probably fixed",
		"a PR number alone",
		"a finished experiment without an explicit resolution",
		"Never guess repository state outside the packet",
	} {
		if !strings.Contains(text, phrase) {
			t.Errorf("resolved-retirement instructions lack %q", phrase)
		}
	}
}

func TestDreamNoiseRetirementContract(t *testing.T) {
	for name, text := range map[string]string{"endpoint schema": dreamSchema, "packet answer": DreamInstructions("p-805")} {
		for _, phrase := range []string{"noise", "by"} {
			if !strings.Contains(text, phrase) {
				t.Errorf("%s lacks %q for noise retirement", name, phrase)
			}
		}
	}
	text := strings.Join(strings.Fields(DreamInstructions("p-805")), " ")
	for _, phrase := range []string{
		"only facts marked as noise candidates are eligible",
		"task progress or a one-off observation",
		"transient authentication/CI/process state",
		"reusable research limitations",
		"the only group that may become empty",
	} {
		if !strings.Contains(text, phrase) {
			t.Errorf("noise-retirement instructions lack %q", phrase)
		}
	}
}

func TestDreamInstructionsEnglishContentWithoutLanguageOnlyRewrites(t *testing.T) {
	text := DreamInstructions("policy-packet")
	for _, phrase := range []string{"new and corrected facts in English", "identifiers, commands, paths, versions, exact values and verbatim quotes", "Never correct a fact solely to change its language"} {
		if !strings.Contains(text, phrase) {
			t.Errorf("missing %q", phrase)
		}
	}
	if strings.Contains(text, `"translations"`) || strings.Contains(dreamSchema, `"translations"`) {
		t.Error("dream still requests translation answers")
	}
}

func TestModelPromptsUseEnglish(t *testing.T) {
	cyrillic := regexp.MustCompile(`[А-Яа-яЁё]`)
	for name, text := range map[string]string{
		"dream packet": packetPrompt,
		"split":        splitPrompt,
		"review":       dreamPrompt,
		"scope":        scopePrompt,
		"note":         notePrompt,
		"system":       dreamRunSystem,
		"answer":       answerShape,
		"api answer":   jsonAnswer,
	} {
		if cyrillic.MatchString(text) {
			t.Errorf("%s still contains Russian instructions", name)
		}
	}
}
