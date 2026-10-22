package recall

import (
	"context"
	"slices"
	"sort"

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
		p.Trace.fallback("no judge configured")
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
		if p.Trace != nil {
			for _, h := range lexical[n:] {
				relabel(p.Trace.Lexical, h.ID, VerdictOverJudge)
			}
		}
		lexical = lexical[:n]
	}
	candidates := append([]Hit(nil), lexical...)
	if e != nil {
		vector, err := vectorCandidates(s, prompt, p, e, n)
		if err == nil {
			candidates = mergeCandidates(candidates, vector)
			if p.Trace != nil {
				for _, h := range lexical {
					if slices.ContainsFunc(vector, func(v Hit) bool { return v.ID == h.ID }) {
						relabel(p.Trace.Vector, h.ID, VerdictFoundByWords)
					}
				}
			}
		} else if p.Trace != nil {
			p.Trace.VectorError = err.Error()
		}
	} else if p.Trace != nil {
		p.Trace.VectorError = "no embedder configured"
	}
	if len(candidates) < minRankCandidates {
		p.Trace.fallback("fewer than two candidates for the judge")
		return Recall(s, prompt, sessionTier, p)
	}
	contents := make([]string, len(candidates))
	for i := range candidates {
		contents[i] = candidates[i].Content
	}
	scores, err := r.Scores(context.Background(), prompt, contents)
	if err != nil || len(scores) != len(candidates) {
		if err != nil {
			p.Trace.fallback("judge failed: " + err.Error())
		} else {
			p.Trace.fallback("judge returned a wrong number of scores")
		}
		return Recall(s, prompt, sessionTier, p)
	}
	p.Trace.judge(candidates, scores, r.Min())
	order := make([]int, len(candidates))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool {
		pi, pj := candidates[order[i]].Provisional, candidates[order[j]].Provisional
		if pi != pj {
			return !pi
		}
		if scores[order[i]] != scores[order[j]] {
			return scores[order[i]] > scores[order[j]]
		}
		// Equal score: the same tie-break as the lexical pass, so enabling the judge does not silently drop the
		// host or repository preference.
		if pi, pj := place(candidates[order[i]], sessionTier, p.Host),
			place(candidates[order[j]], sessionTier, p.Host); pi != pj {
			return pi < pj
		}
		return candidates[order[i]].ID > candidates[order[j]].ID
	})
	// Keep every fact above the threshold first, then cut: the omitted tail is what the displacement flag is about.
	// The order puts approved facts before provisional ones, so it is not a score order: a weak approved candidate
	// must not hide a provisional fact that the judge scored above the threshold further down the list.
	var eligible []Hit
	for _, i := range order {
		if scores[i] < r.Min() {
			continue
		}
		h := candidates[i]
		h.Meaning = scores[i]
		eligible = append(eligible, h)
	}
	maxFacts := capFacts(p.MaxFacts, len(eligible))
	served := eligible
	if len(eligible) > maxFacts {
		served = eligible[:maxFacts]
	}
	p.Trace.final(eligible, len(served))
	if p.Host != "" {
		omitted := false
		for _, h := range eligible[len(served):] {
			if hostScope(h.Scope, p.Host) {
				omitted = true
				break
			}
		}
		if omitted {
			for i := range served {
				if otherHostScope(served[i].Scope, p.Host) {
					served[i].Displaced = true
				}
			}
		}
	}
	return served, nil
}

// vectorCandidates returns up to n active facts closest to the prompt by cosine. Only the VectorPool most recently
// written facts are read, so a large base is not decoded and scored on every prompt; the stored text hash and
// dimension decide whether a vector is comparable at all, and a fact the session already saw is left out, like
// lexical ones.
func vectorCandidates(s *store.Store, prompt string, p Params, e Embedder, n int) ([]Hit, error) {
	rows, err := s.VectorsLimit(e.Tag(), p.VectorPool)
	if p.Trace != nil {
		p.Trace.VectorPool = p.VectorPool
	}
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	query, err := embed1(e, prompt)
	if err != nil || query == nil {
		return nil, err
	}
	today := p.Today
	if today == "" {
		today = store.Today()
	}
	type candidate struct {
		row store.VectorRow
		sim float64
	}
	var found []candidate
	var skipped []Step
	for _, row := range rows {
		if row.Dim != len(query) || row.Hash != store.TextHash(row.Content) || store.Expired(row.Expires, today) ||
			p.Seen[row.ID] {
			if p.Trace != nil {
				skipped = append(skipped, Step{ID: row.ID, Content: row.Content,
					Verdict: vectorSkip(row, len(query), today, p.Seen)})
			}
			continue
		}
		found = append(found, candidate{row: row, sim: store.Dot(query, store.Normalize(row.Values))})
	}
	sort.SliceStable(found, func(i, j int) bool { return found[i].sim > found[j].sim })
	if p.Trace != nil {
		p.Trace.Vector = nil
		for i, c := range found {
			verdict := VerdictCandidate
			if n > 0 && i >= n {
				verdict = VerdictBelowTop
			}
			p.Trace.Vector = append(p.Trace.Vector, Step{ID: c.row.ID, Content: c.row.Content, Similarity: c.sim,
				Verdict: verdict})
		}
		p.Trace.Vector = append(p.Trace.Vector, skipped...)
	}
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
