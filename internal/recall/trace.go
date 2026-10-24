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
	Final       []Step  // the facts that reached the cut, in order: served or cut
}

// Term is a prompt term with the weight it carries in coverage.
type Term struct {
	Term   string
	Weight float64
}

// Step is one fact at one step of the decision.
type Step struct {
	ID       string
	Content  string
	Matched  []string // prompt terms the fact holds (lexical)
	Coverage float64  // IDF-weighted share of the prompt's terms (lexical)
	Verdict  string
}

// Verdicts of the steps.
const (
	VerdictPassed      = "passed"
	VerdictSeen        = "seen in this session"
	VerdictExpired     = "expired"
	VerdictOtherTicket = "no ticket of the prompt"
	VerdictFewTerms    = "too few terms"
	VerdictLowCoverage = "low coverage"
	VerdictForeign     = "another project, below its coverage"
	VerdictServed      = "served"
	VerdictCut         = "cut by max facts"
)

func (t *Trace) lexical(f store.Fact, matched []string, coverage float64, verdict string) {
	if t != nil {
		t.Lexical = append(t.Lexical, Step{ID: f.ID, Content: f.Content, Matched: matched, Coverage: coverage,
			Verdict: verdict})
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
		t.Final = append(t.Final, Step{ID: h.ID, Content: h.Content, Coverage: h.Coverage,
			Verdict: verdict})
	}
}

func (t *Trace) stop(why string) {
	if t != nil {
		t.Stopped = why
	}
}
