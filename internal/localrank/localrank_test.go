package localrank

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"math"
	"os"
	"testing"

	"github.com/juev/memcue/internal/recall"
)

// goldRow is one prompt of testdata/gold.jsonl.gz: the rankfeat row of a gold prompt and the scores LightGBM gave its
// candidates when local_train.py wrote model.txt.
type goldRow struct {
	ID string `json:"id"`
	recall.PromptFeatures
	Features []recall.Features `json:"features"`
	Scores   []float64         `json:"scores"`
}

// TestScoresMatchLightGBMOnTheGoldSet pins the Go vector and the tree walk to the trainer: every gold pair, raw score
// within 1e-9 of LightGBM's. A feature out of order, a one-hot value misspelt or a model file leaves misreads fails here.
func TestScoresMatchLightGBMOnTheGoldSet(t *testing.T) {
	f, err := os.Open("testdata/gold.jsonl.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	z, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	m, err := model()
	if err != nil {
		t.Fatal(err)
	}
	sc := bufio.NewScanner(z)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	prompts, pairs := 0, 0
	for sc.Scan() {
		var row goldRow
		if err := json.Unmarshal(sc.Bytes(), &row); err != nil {
			t.Fatal(err)
		}
		for i, v := range vectors(row.PromptFeatures, row.Features) {
			if got := m.PredictSingle(v, 0); math.Abs(got-row.Scores[i]) > 1e-9 {
				t.Fatalf("%s, candidate %d: Go %v, LightGBM %v", row.ID, i, got, row.Scores[i])
			}
			pairs++
		}
		prompts++
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if prompts != 171 || pairs < 3000 {
		t.Fatalf("checked %d prompts, %d pairs; the fixture holds 171 gold prompts", prompts, pairs)
	}
}

func TestScoreFeaturesStaysInTheUnitInterval(t *testing.T) {
	fs := []recall.Features{
		{ID: "a", Rank: 0, Passed: true, Coverage: 0.9, Matched: 5, MatchedMax: 1, ContentCov: 0.9, Kind: "pitfall"},
		{ID: "b", Rank: 1, Coverage: 0.05, Matched: 1, MatchedMax: 0.1, Runes: 4000, Source: "note"},
	}
	scores, err := Ranker{}.ScoreFeatures(context.Background(), recall.PromptFeatures{Terms: 6, Known: 5, Runes: 40,
		Weights: 9}, fs)
	if err != nil {
		t.Fatal(err)
	}
	if len(scores) != len(fs) {
		t.Fatalf("%d scores for %d candidates", len(scores), len(fs))
	}
	for i, s := range scores {
		if s < 0 || s > 1 || math.IsNaN(s) {
			t.Errorf("score %d = %v, want 0..1", i, s)
		}
	}
}

func TestRankerRefusesTexts(t *testing.T) {
	if _, err := (Ranker{}).Scores(context.Background(), "prompt", []string{"a", "b"}); err == nil {
		t.Fatal("Scores answered; the local reader has nothing to say about texts")
	}
	var _ recall.FeatureRanker = Ranker{}
}
