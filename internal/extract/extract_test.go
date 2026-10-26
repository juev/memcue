package extract

import (
	"testing"
	"time"
)

// answer runs the two production steps the tests describe: the envelope of `claude -p` and the payload decode.
func answer(b []byte) (Result, error) {
	rp, err := envelope(b)
	if err != nil {
		return Result{}, err
	}
	return decode(rp)
}

func TestAnswerStructuredOutput(t *testing.T) {
	r, err := answer([]byte(`{"is_error":false,"result":"","total_cost_usd":0.14,
		"structured_output":{"facts":[{"content":"  wrapper ~/bin/glab ставит GODEBUG=tlsmlkem=0 ","keywords":["glab","EOF"]}],"dropped":""}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Facts) != 1 || r.Facts[0].Content != "wrapper ~/bin/glab ставит GODEBUG=tlsmlkem=0" || r.CostUSD != 0.14 {
		t.Errorf("got %+v", r)
	}
}

// The envelope of `claude -p` carries the usage and the time of the call; the input counts the cached part too (#471).
func TestEnvelopeReadsUsageAndTime(t *testing.T) {
	rp, err := envelope([]byte(`{"is_error":false,"structured_output":{},"total_cost_usd":0.6,
		"duration_api_ms":95000,"ttft_ms":40000,"usage":{"input_tokens":5,"cache_creation_input_tokens":30000,
		"cache_read_input_tokens":2000,"output_tokens":12000,"output_tokens_details":{"thinking_tokens":7000}}}`))
	if err != nil {
		t.Fatal(err)
	}
	want := Tokens{Input: 32005, Cached: 2000, Output: 12000, Thinking: 7000}
	if rp.tokens != want || rp.api != 95*time.Second || rp.ttft != 40*time.Second || rp.cost != 0.6 {
		t.Errorf("got tokens %+v, api %v, ttft %v, cost %v", rp.tokens, rp.api, rp.ttft, rp.cost)
	}
}

func TestAnswerFallsBackToResultText(t *testing.T) {
	r, err := answer([]byte(`{"is_error":false,"result":"{\"facts\":[],\"dropped\":\"только история\"}"}`))
	if err != nil || r.Dropped != "только история" {
		t.Errorf("got %+v %v", r, err)
	}
}

func TestAnswerReportsModelError(t *testing.T) {
	if _, err := answer([]byte(`{"is_error":true,"result":"rate limited"}`)); err == nil {
		t.Error("want error")
	}
}

func TestAnswerRejectsEmptyAnswer(t *testing.T) {
	if _, err := answer([]byte(`{"is_error":false,"structured_output":{"facts":[],"dropped":""}}`)); err == nil {
		t.Error("want error")
	}
}
