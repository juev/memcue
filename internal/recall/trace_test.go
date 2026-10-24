package recall

import (
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
	hits, err := Recall(s, "прокси mihomo смешанный порт caddy", nil, p)
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
	if _, err := Recall(s, "прокси mihomo смешанный порт", nil, p); err != nil {
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
	if _, err := Recall(s, "да", nil, p); err != nil {
		t.Fatal(err)
	}
	if tr.Stopped == "" || len(tr.Lexical) != 0 {
		t.Errorf("stopped = %q, lexical = %+v", tr.Stopped, tr.Lexical)
	}
}
