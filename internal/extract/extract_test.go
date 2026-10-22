package extract

import (
	"strings"
	"testing"
)

func TestParseStructuredOutput(t *testing.T) {
	r, err := parse([]byte(`{"is_error":false,"result":"","total_cost_usd":0.14,
		"structured_output":{"facts":[{"content":"  wrapper ~/bin/glab ставит GODEBUG=tlsmlkem=0 ","keywords":["glab","EOF"]}],"dropped":""}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Facts) != 1 || r.Facts[0].Content != "wrapper ~/bin/glab ставит GODEBUG=tlsmlkem=0" || r.CostUSD != 0.14 {
		t.Errorf("got %+v", r)
	}
}

func TestParseFallsBackToResultText(t *testing.T) {
	r, err := parse([]byte(`{"is_error":false,"result":"{\"facts\":[],\"dropped\":\"только история\"}"}`))
	if err != nil || r.Dropped != "только история" {
		t.Errorf("got %+v %v", r, err)
	}
}

func TestParseReportsModelError(t *testing.T) {
	if _, err := parse([]byte(`{"is_error":true,"result":"rate limited"}`)); err == nil {
		t.Error("want error")
	}
}

func TestParseRejectsEmptyAnswer(t *testing.T) {
	if _, err := parse([]byte(`{"is_error":false,"structured_output":{"facts":[],"dropped":""}}`)); err == nil {
		t.Error("want error")
	}
}

func TestSessionFactCarriesKindSourceEvidenceScope(t *testing.T) {
	r, err := decode(reply{payload: []byte(`{"facts":[{"content":" Порт db1 5432. ","keywords":["порт"],` +
		`"kind":"environment","scope":"host:db1","source":"tool","evidence":"port 5432"}],"dropped":""}`)})
	if err != nil {
		t.Fatal(err)
	}
	want := Fact{Content: "Порт db1 5432.", Keywords: []string{"порт"}, Kind: "environment", Scope: "host:db1",
		Source: "tool", Evidence: "port 5432"}
	if len(r.Facts) != 1 || r.Facts[0].Content != want.Content || r.Facts[0].Kind != want.Kind ||
		r.Facts[0].Scope != want.Scope || r.Facts[0].Source != want.Source || r.Facts[0].Evidence != want.Evidence {
		t.Errorf("got %+v", r.Facts)
	}
	if !strings.Contains(sessionSchema, `"required":["content","keywords","kind","scope","source","evidence"]`) {
		t.Error("the session schema does not require the fields the policy reads")
	}
}

// The search queries of a session fact are kept, trimmed, without empty ones, repeats or commas inside one.
func TestSessionFactCarriesQueries(t *testing.T) {
	r, err := decode(reply{payload: []byte(`{"facts":[{"content":"Порт db1 5432.","keywords":["порт"],` +
		`"queries":[" обратный, прокси ","","обратный прокси","какой порт"]}],"dropped":""}`)})
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Facts[0].Queries; len(got) != 2 || got[0] != "обратный прокси" || got[1] != "какой порт" {
		t.Errorf("queries %q", got)
	}
	if !strings.Contains(sessionSchema, `"queries":{"type":"array"`) || !strings.Contains(sessionAnswer, `"queries"`) {
		t.Error("the session schema and answer shape must name queries")
	}
}
