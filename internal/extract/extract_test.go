package extract

import "testing"

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
