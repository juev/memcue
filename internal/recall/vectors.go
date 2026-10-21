package recall

import (
	"context"
	"sort"
	"time"

	"github.com/juev/memcue/internal/store"
)

// Embedder turns a prompt into a vector. A nil Embedder turns the vector candidate source off.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	Tag() string
}

// RecallJudge is recall with the lexical candidates and, when an embedder is configured, the vector candidates in
// front of a judge. The judge orders both sources and drops what is below its threshold. Any failure of the judge
// leaves the lexical answer; an embedder failure only removes the vector candidates. A nil ranker means lexical only:
// the vector reader is not a standalone reader.
func RecallJudge(s *store.Store, prompt, sessionTier string, p Params, r Ranker, e Embedder) ([]Hit, error) {
	if r == nil {
		return Recall(s, prompt, sessionTier, p)
	}
	lexical, err := recallCandidates(s, prompt, p)
	if err != nil {
		return nil, err
	}
	n := r.Candidates()
	if n <= 0 {
		n = len(lexical)
	}
	if len(lexical) > n {
		lexical = lexical[:n]
	}
	candidates := append([]Hit(nil), lexical...)
	if e != nil {
		if vector, err := vectorCandidates(s, prompt, p, e, n); err == nil {
			candidates = mergeCandidates(candidates, vector)
		}
	}
	if len(candidates) < minRankCandidates {
		return Recall(s, prompt, sessionTier, p)
	}
	contents := make([]string, len(candidates))
	for i := range candidates {
		contents[i] = candidates[i].Content
	}
	scores, err := r.Scores(context.Background(), prompt, contents)
	if err != nil || len(scores) != len(candidates) {
		return Recall(s, prompt, sessionTier, p)
	}
	order := make([]int, len(candidates))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool {
		pi, pj := candidates[order[i]].Provisional, candidates[order[j]].Provisional
		if pi != pj {
			return !pi
		}
		return scores[order[i]] > scores[order[j]]
	})
	maxFacts := p.MaxFacts
	if maxFacts <= 0 {
		maxFacts = len(order)
	}
	var hits []Hit
	for _, i := range order {
		if len(hits) >= maxFacts {
			break
		}
		if scores[i] < r.Min() {
			break
		}
		h := candidates[i]
		h.Meaning = scores[i]
		hits = append(hits, h)
	}
	return hits, nil
}

// vectorCandidates returns up to n active facts closest to the prompt by cosine. The stored text hash and dimension
// decide whether a vector is comparable at all; a fact the session already saw is left out here, like lexical ones.
func vectorCandidates(s *store.Store, prompt string, p Params, e Embedder, n int) ([]Hit, error) {
	rows, err := s.Vectors(e.Tag())
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	query, err := embed1(e, prompt)
	if err != nil || query == nil {
		return nil, err
	}
	today := p.Today
	if today == "" {
		today = time.Now().UTC().Format(time.DateOnly)
	}
	type candidate struct {
		row store.VectorRow
		sim float64
	}
	var found []candidate
	for _, row := range rows {
		if row.Dim != len(query) || row.Hash != store.TextHash(row.Content) || store.Expired(row.Expires, today) ||
			p.Seen[row.ID] {
			continue
		}
		found = append(found, candidate{row: row, sim: store.Dot(query, store.Normalize(row.Values))})
	}
	sort.SliceStable(found, func(i, j int) bool { return found[i].sim > found[j].sim })
	if n > 0 && len(found) > n {
		found = found[:n]
	}
	out := make([]Hit, 0, len(found))
	for _, c := range found {
		out = append(out, Hit{Fact: c.row.Fact, Source: "vector", Provisional: c.row.Review == store.Provisional,
			Meaning: c.sim})
	}
	return out, nil
}

// mergeCandidates appends vector candidates that the lexical reader did not already find.
func mergeCandidates(lexical, vector []Hit) []Hit {
	seen := make(map[string]bool, len(lexical))
	for _, h := range lexical {
		seen[h.ID] = true
	}
	out := lexical
	for _, h := range vector {
		if !seen[h.ID] {
			out = append(out, h)
		}
	}
	return out
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
