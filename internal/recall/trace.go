package recall

import "github.com/juev/memcue/internal/store"

// Trace records how one recall was decided, so a miss can be traced to the step that lost the fact (`memcue recall
// -explain`). Recall fills it when Params.Trace is set; a nil Trace records nothing.
type Trace struct {
	Stopped     string  // why recall gave up before searching; empty when it searched
	Terms       []Term  // prompt terms the facts know, as weighed by the coverage gate
	MinMatched  int     // the gate actually used: a short prompt or a ticket key lowers it
	MinCoverage float64 // the gate actually used
	Lexical     []Step  // every fact FTS5 returned, in its order, with the gate's verdict
	VectorPool  int     // vectors read: those of the most recent facts; <= 0 means every vector
	VectorError string  // why the vector reader gave nothing; empty when it ran
	Vector      []Step  // every vector read, the comparable ones by similarity
	JudgeMin    float64 // score below which the judge drops a fact
	Judge       []Step  // what the judge was asked about, in the order asked, with its score
	Fallback    string  // why the lexical answer was given instead of the judge's; empty when it was not
	Final       []Step  // the facts that reached the cut, in order: served or cut
}

// Term is a prompt term with the weight it carries in coverage.
type Term struct {
	Term   string
	Weight float64
}

// Step is one fact at one step of the decision.
type Step struct {
	ID         string
	Content    string
	Matched    []string // prompt terms the fact holds (lexical)
	Coverage   float64  // IDF-weighted share of the prompt's terms (lexical)
	Similarity float64  // cosine to the prompt (vector)
	Score      float64  // the judge's score (judge, and final when the judge decided)
	Verdict    string
}

// Verdicts of the steps.
const (
	VerdictPassed       = "passed"
	VerdictSeen         = "seen in this session"
	VerdictExpired      = "expired"
	VerdictOtherTicket  = "no ticket of the prompt"
	VerdictFewTerms     = "too few terms"
	VerdictLowCoverage  = "low coverage"
	VerdictOverJudge    = "passed, over the judge's candidate limit"
	VerdictGatedToJudge = "below the gate, sent to the judge"
	VerdictCandidate    = "candidate"
	VerdictBelowTop     = "below the top"
	VerdictFoundByWords = "candidate, already found by the words"
	VerdictOtherText    = "vector of an older text"
	VerdictOtherDim     = "vector of another dimension"
	VerdictAboveMin     = "above min"
	VerdictBelowMin     = "below min"
	VerdictServed       = "served"
	VerdictCut          = "cut by max facts"
)

func (t *Trace) lexical(f store.Fact, matched []string, coverage float64, verdict string) {
	if t != nil {
		t.Lexical = append(t.Lexical, Step{ID: f.ID, Content: f.Content, Matched: matched, Coverage: coverage,
			Verdict: verdict})
	}
}

// relabel changes the verdict of the fact in steps, if it is there.
func relabel(steps []Step, id, verdict string) {
	for i := range steps {
		if steps[i].ID == id {
			steps[i].Verdict = verdict
		}
	}
}

// final records the facts that reached the cut: the first served of them served, the rest cut.
func (t *Trace) final(hits []Hit, served int) {
	if t == nil {
		return
	}
	t.Final = nil
	for i, h := range hits {
		verdict := VerdictServed
		if i >= served {
			verdict = VerdictCut
		}
		t.Final = append(t.Final, Step{ID: h.ID, Content: h.Content, Coverage: h.Coverage, Score: h.Meaning,
			Verdict: verdict})
	}
}

func (t *Trace) stop(why string) {
	if t != nil {
		t.Stopped = why
	}
}

func (t *Trace) fallback(why string) {
	if t != nil {
		t.Fallback = why
	}
}

// judge records the judge's score of each candidate against its threshold.
func (t *Trace) judge(candidates []Hit, scores []float64, minScore float64) {
	if t == nil {
		return
	}
	t.JudgeMin = minScore
	t.Judge = nil
	for i, h := range candidates {
		verdict := VerdictAboveMin
		if scores[i] < minScore {
			verdict = VerdictBelowMin
		}
		t.Judge = append(t.Judge, Step{ID: h.ID, Content: h.Content, Coverage: h.Coverage, Similarity: similarity(h),
			Score: scores[i], Verdict: verdict})
	}
}

// similarity is the cosine of a vector candidate; a lexical one has none.
func similarity(h Hit) float64 {
	if h.Source == "vector" {
		return h.Meaning
	}
	return 0
}

// vectorSkip names why a vector was not compared with the prompt.
func vectorSkip(row store.VectorRow, dim int, today string, seen map[string]bool) string {
	switch {
	case row.Dim != dim:
		return VerdictOtherDim
	case row.Hash != store.TextHash(row.Content):
		return VerdictOtherText
	case store.Expired(row.Expires, today):
		return VerdictExpired
	case seen[row.ID]:
		return VerdictSeen
	}
	return ""
}
