package recall

import (
	"errors"
	"strings"
	"testing"
)

// step returns the step of the fact whose content holds word, failing the test when there is none.
func step(t *testing.T, steps []Step, word string) Step {
	t.Helper()
	for _, s := range steps {
		if strings.Contains(s.Content, word) {
			return s
		}
	}
	t.Fatalf("no step holds %q in %+v", word, steps)
	return Step{}
}

// The trace of a lexical recall names the gate's verdict for every FTS5 candidate and which facts the cap cut.
func TestTraceRecordsTheLexicalDecision(t *testing.T) {
	s := setup(t)
	tr := &Trace{}
	p := Default
	p.MaxFacts, p.MinMatched, p.Trace = 1, 2, tr
	hits, err := Recall(s, "прокси mihomo смешанный порт caddy", "", p)
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.Terms) == 0 || tr.MinMatched != 2 || tr.MinCoverage != p.MinCoverage {
		t.Fatalf("terms = %v, gate = %d/%v", tr.Terms, tr.MinMatched, tr.MinCoverage)
	}
	mixed := step(t, tr.Lexical, "смешанный")
	if mixed.Verdict != VerdictPassed || len(mixed.Matched) < 3 || mixed.Coverage <= 0 {
		t.Errorf("mixed proxy = %+v, want passed with its terms", mixed)
	}
	if caddy := step(t, tr.Lexical, "Caddy"); caddy.Verdict != VerdictFewTerms {
		t.Errorf("caddy = %+v, want %q", caddy, VerdictFewTerms)
	}
	if len(tr.Final) != 2 || tr.Final[0].Verdict != VerdictServed || tr.Final[1].Verdict != VerdictCut {
		t.Fatalf("final = %+v, want one served and one cut", tr.Final)
	}
	if len(hits) != 1 || hits[0].ID != tr.Final[0].ID {
		t.Errorf("hits = %v, final = %+v", ids(hits), tr.Final)
	}
}

// A fact the session already got, or one past its expiry, is named as such instead of vanishing.
func TestTraceNamesSeenAndExpiredFacts(t *testing.T) {
	s := setup(t)
	all, err := s.Active()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, f := range all {
		if strings.Contains(f.Content, "смешанный") {
			seen[f.ID] = true
		}
	}
	tr := &Trace{}
	p := Default
	p.Seen, p.Trace = seen, tr
	if _, err := Recall(s, "прокси mihomo смешанный порт", "", p); err != nil {
		t.Fatal(err)
	}
	if got := step(t, tr.Lexical, "смешанный").Verdict; got != VerdictSeen {
		t.Errorf("verdict = %q, want %q", got, VerdictSeen)
	}
}

// A prompt with too few known terms stops before the search, and the trace says so.
func TestTraceNamesAnEarlyStop(t *testing.T) {
	s := setup(t)
	tr := &Trace{}
	p := Default
	p.Trace = tr
	if _, err := Recall(s, "да", "", p); err != nil {
		t.Fatal(err)
	}
	if tr.Stopped == "" || len(tr.Lexical) != 0 {
		t.Errorf("stopped = %q, lexical = %+v", tr.Stopped, tr.Lexical)
	}
}

// With a judge, the trace shows the vector reader's similarities, the judge's scores against its threshold and what
// was served.
func TestTraceRecordsVectorsAndTheJudge(t *testing.T) {
	s, target := meaningBase(t)
	e := fakeEmbedder{tag: modelTag, vec: []float32{1, 0}}
	r := &fakeRanker{min: 0.5, scores: map[string]float64{"Порог": 0.95, "Caddy": 0.2}}
	tr := &Trace{}
	p := rankedParams()
	p.Trace = tr
	hits, err := RecallJudge(s, "почему шумно в ответах caddy", "", p, r, e)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].ID != target {
		t.Fatalf("hits = %v, want the target first", ids(hits))
	}
	if tr.VectorPool != p.VectorPool || tr.VectorError != "" || tr.Fallback != "" {
		t.Errorf("pool = %d, vector error = %q, fallback = %q", tr.VectorPool, tr.VectorError, tr.Fallback)
	}
	if v := step(t, tr.Vector, "Порог"); v.Verdict != VerdictCandidate || v.Similarity < 0.9 || v.ID != tr.Vector[0].ID {
		t.Errorf("vector = %+v, want the target first as a candidate", v)
	}
	if tr.JudgeMin != 0.5 {
		t.Errorf("judge min = %v", tr.JudgeMin)
	}
	if j := step(t, tr.Judge, "Порог"); j.Verdict != VerdictAboveMin || j.Score != 0.95 {
		t.Errorf("judge = %+v", j)
	}
	if j := step(t, tr.Judge, "Caddy"); j.Verdict != VerdictBelowMin {
		t.Errorf("judge = %+v, want below min", j)
	}
	if f := step(t, tr.Final, "Порог"); f.Verdict != VerdictServed {
		t.Errorf("final = %+v", f)
	}
}

// A judge that fails leaves the lexical answer, and the trace says why.
func TestTraceNamesTheFallback(t *testing.T) {
	s, _ := meaningBase(t)
	e := fakeEmbedder{tag: modelTag, vec: []float32{1, 0}}
	tr := &Trace{}
	p := Default
	p.Trace = tr
	if _, err := RecallJudge(s, "Gradle требует JAVA_HOME, иначе сборка не находит JDK", "", p,
		&fakeRanker{min: 0.5, err: errors.New("down")}, e); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tr.Fallback, "down") {
		t.Errorf("fallback = %q, want the judge's error", tr.Fallback)
	}
	if f := step(t, tr.Final, "Gradle"); f.Verdict != VerdictServed {
		t.Errorf("final = %+v", f)
	}
}

// A dead embedder is named; recall goes on without vectors.
func TestTraceNamesAFailedEmbedder(t *testing.T) {
	s, _ := meaningBase(t)
	tr := &Trace{}
	p := rankedParams()
	p.Trace = tr
	r := &fakeRanker{min: 0.5, scores: map[string]float64{"Gradle": 0.9}}
	if _, err := RecallJudge(s, "Gradle требует JAVA_HOME сборка JDK", "", p, r,
		fakeEmbedder{tag: modelTag, err: errors.New("refused")}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tr.VectorError, "refused") || len(tr.Vector) != 0 {
		t.Errorf("vector error = %q, vector = %+v", tr.VectorError, tr.Vector)
	}
}
