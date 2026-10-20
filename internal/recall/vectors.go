package recall

import (
	"context"
	"math"
	"sort"
	"time"

	"github.com/juev/memcue/internal/store"
)

// Embedder turns a prompt into a vector. A nil Embedder, or a vector space with no stored vectors, turns the whole
// vector pass off.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	Tag() string
}

// VectorParams are the knobs of the meaning pass. The zero value (no Embedder) means it never runs.
type VectorParams struct {
	Embedder Embedder
	// Only makes the meaning pass the only reader: the words are not consulted at all, and the answer is what the
	// vectors found, even when that is nothing. A failing endpoint still falls back to the words, so a dead model
	// costs precision, never the memory itself.
	Only bool
	// Margin is how far above the chance maximum a fact's similarity must stand to be offered on meaning alone, in
	// standard deviations of this query's similarities. The chance maximum grows with the store as sqrt(2 ln N), so
	// the bar rises with the base while a paraphrase of a stored fact keeps standing out.
	Margin float64
	// MinScore is an optional absolute floor; 0 leaves the decision to Margin alone. It is model-specific on
	// purpose: a paraphrase of a stored fact scores around 0.4-0.7 on one model and 0.7-0.85 on another.
	MinScore float64
	// MaxFacts is the total number of facts a prompt may get, lexical and vector together. <= 0 means Params.MaxFacts.
	MaxFacts int
}

// DefaultVectorMargin was measured on the Hermes prompt set: a raw cosine threshold does not separate a prompt that
// needs memory from one that does not (at 0.55 it kept 20 of 35 misses but also 33 of 37 prompts that needed
// nothing), while the spread of one query's similarities does.
const DefaultVectorMargin = 1.0

// MinVectors is the number of comparable vectors below which the spread of similarities says nothing.
const MinVectors = 8

// RecallHybrid is Recall plus, when the words were not enough, one pass over the meaning of the prompt. Any failure
// of the embedder leaves the lexical result exactly as it was: recall never gets worse because the endpoint is
// missing, slow or wrong.
//
// With VectorParams.Only the meaning pass is the only reader: what it found is the answer, and the words are read
// only when the endpoint fails or the base has no vectors to compare.
func RecallHybrid(s *store.Store, prompt, sessionTier string, p Params, vp VectorParams) ([]Hit, error) {
	if vp.Embedder != nil && vp.Only {
		hits, answered, err := onlyMeaning(s, prompt, p, vp)
		if err != nil {
			return nil, err
		}
		if answered {
			return hits, nil
		}
	}
	hits, err := Recall(s, prompt, sessionTier, p)
	if err != nil {
		return nil, err
	}
	return byMeaning(s, prompt, p, vp, hits)
}

// onlyMeaning is the meaning pass as the only reader. answered is false when it could not decide — the endpoint failed
// or the base has nothing to compare — and the caller is to fall back to the words.
func onlyMeaning(s *store.Store, prompt string, p Params, vp VectorParams) ([]Hit, bool, error) {
	rows, err := s.Vectors(vp.Embedder.Tag())
	if err != nil || len(rows) < MinVectors {
		return nil, false, nil
	}
	query, err := embed1(vp.Embedder, prompt)
	if err != nil || query == nil {
		return nil, false, nil
	}
	hits, err := meaningHits(s, rows, query, p, vp, nil)
	if err != nil {
		return nil, false, err
	}
	return hits, true, nil
}

// byMeaning adds at most the room left by MaxFacts facts that stand out from the rest of the base by meaning.
func byMeaning(s *store.Store, prompt string, p Params, vp VectorParams, hits []Hit) ([]Hit, error) {
	if vp.Embedder == nil {
		return hits, nil
	}
	maxFacts := vp.MaxFacts
	if maxFacts <= 0 {
		maxFacts = p.MaxFacts
	}
	if maxFacts > 0 && len(hits) >= maxFacts {
		return hits, nil // the words already filled the prompt
	}
	rows, err := s.Vectors(vp.Embedder.Tag())
	if err != nil || len(rows) < MinVectors {
		return hits, nil
	}
	query, err := embed1(vp.Embedder, prompt)
	if err != nil || query == nil {
		return hits, nil
	}
	return meaningHits(s, rows, query, p, vp, hits)
}

// meaningHits decides which facts stand out for query and appends them to hits.
func meaningHits(s *store.Store, rows []store.VectorRow, query []float32, p Params, vp VectorParams,
	hits []Hit) ([]Hit, error) {
	maxFacts := vp.MaxFacts
	if maxFacts <= 0 {
		maxFacts = p.MaxFacts
	}
	today := p.Today
	if today == "" {
		today = time.Now().UTC().Format(time.DateOnly)
	}
	taken := make(map[string]bool, len(hits))
	for _, h := range hits {
		taken[h.ID] = true
	}
	type candidate struct {
		fact store.VectorRow
		sim  float64
	}
	var candidates []candidate
	var sum float64
	for _, row := range rows {
		if row.Dim != len(query) || taken[row.ID] {
			continue
		}
		// A vector made from another text, or by another model that happens to share the tag, is not comparable.
		if row.Hash != store.TextHash(row.Content) || store.Expired(row.Expires, today) || p.Seen[row.ID] {
			continue
		}
		sim := store.Dot(query, store.Normalize(row.Values))
		candidates = append(candidates, candidate{fact: row, sim: sim})
		sum += sim
	}
	if len(candidates) < MinVectors {
		return hits, nil
	}
	n := float64(len(candidates))
	mean := sum / n
	var variance float64
	for _, c := range candidates {
		variance += (c.sim - mean) * (c.sim - mean)
	}
	std := math.Sqrt(variance / n)
	if std == 0 {
		return hits, nil
	}
	margin := vp.Margin
	if margin <= 0 {
		margin = DefaultVectorMargin
	}
	// The closest of n unrelated facts reaches about std*sqrt(2 ln n) above the mean by chance alone; a fact that
	// stands a margin of standard deviations above even that is a paraphrase, not a coincidence.
	threshold := mean + margin*std*math.Sqrt(2*math.Log(n))
	if vp.MinScore > 0 && vp.MinScore > threshold {
		threshold = vp.MinScore
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].sim > candidates[j].sim })
	room := maxFacts - len(hits)
	if room <= 0 || room > len(candidates) {
		room = len(candidates)
	}
	for _, c := range candidates {
		if room == 0 {
			break
		}
		if c.sim < threshold {
			break
		}
		hits = append(hits, Hit{Fact: c.fact.Fact, Meaning: c.sim})
		room--
	}
	return hits, nil
}

// embed1 asks the embedder for the vector of one text.
func embed1(e Embedder, text string) ([]float32, error) {
	vectors, err := e.Embed(context.Background(), []string{text})
	if err != nil {
		return nil, err
	}
	if len(vectors) == 0 || len(vectors[0]) == 0 {
		return nil, nil
	}
	return vectors[0], nil
}
