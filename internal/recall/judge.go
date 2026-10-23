package recall

import (
	"context"
	"errors"
	"sort"

	"github.com/juev/memcue/internal/judge"
	"github.com/juev/memcue/internal/store"
)

// Ranker scores how much each of the facts the words found would help answer the prompt, on 0..1. A nil Ranker turns
// the pass off.
type Ranker interface {
	Scores(ctx context.Context, prompt string, facts []string) ([]float64, error)
	Min() float64
	Candidates() int
}

// minRankCandidates is the smallest candidate list worth asking about: with one fact there is nothing to choose.
const minRankCandidates = 2

// RecallJudge is recall with the facts the words found in front of a judge, which orders them and drops what is below
// its threshold. The judge reads the first Candidates facts in the search's BM25 order, whether the term and coverage
// gate let them pass or not: the gate was tuned for recall by words alone, and with a judge the facts it turns away
// still answer prompts (gold set of 180 prompts, 2026-10-22, #232: 20 facts in BM25 order on expanded facts served a
// useful fact to 122-124 prompts, as many as the vector reader and the judge together). Any failure of the judge
// leaves the lexical answer, gate included; a nil ranker means lexical only.
func RecallJudge(s *store.Store, prompt, sessionTier string, p Params, r Ranker) ([]Hit, error) {
	if r == nil {
		p.Trace.fallback("no judge configured")
		return Recall(s, prompt, sessionTier, p)
	}
	passed, gated, err := recallCandidates(s, prompt, p)
	if err != nil {
		return nil, err
	}
	candidates := append(append([]Hit(nil), passed...), gated...)
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].rank < candidates[j].rank })
	n := r.Candidates()
	if n <= 0 {
		n = len(candidates)
	}
	if p.Trace != nil {
		for i, h := range candidates {
			switch {
			case i < n && h.gated:
				relabel(p.Trace.Lexical, h.ID, VerdictGatedToJudge)
			case i >= n && !h.gated:
				relabel(p.Trace.Lexical, h.ID, VerdictOverJudge)
			}
		}
	}
	if len(candidates) > n {
		candidates = candidates[:n]
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
		switch {
		case errors.Is(err, judge.ErrPaused):
			// Not a failure of this prompt: an earlier one paused the endpoint, and this prompt never reached it.
			p.Trace.fallback("judge skipped: " + err.Error())
			if p.Trace != nil {
				p.Trace.Paused = true
			}
		case err != nil:
			p.Trace.fallback("judge failed: " + err.Error())
		default:
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
