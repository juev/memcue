package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/juev/memcue/internal/recall"
	"github.com/juev/memcue/internal/store"
)

// explainWidth is how much of a fact's text a line of the explanation shows.
const explainWidth = 100

// explainRecall prints every step of one recall: the prompt terms, the gate's verdict for each lexical candidate, the
// and the final cut.
func explainRecall(t *recall.Trace, out io.Writer) error {
	var b strings.Builder
	if t.Stopped != "" {
		fmt.Fprintf(&b, "stopped: %s\n", t.Stopped)
	}
	if len(t.Terms) > 0 {
		terms := make([]string, len(t.Terms))
		for i, term := range t.Terms {
			terms[i] = fmt.Sprintf("%s %.2f", term.Term, term.Weight)
		}
		fmt.Fprintf(&b, "terms: %s\n", strings.Join(terms, ", "))
		fmt.Fprintf(&b, "gate: %d terms, coverage %.2f\n", t.MinMatched, t.MinCoverage)
	}
	if len(t.Lexical) > 0 {
		b.WriteString("lexical:\n")
		for _, s := range t.Lexical {
			fmt.Fprintf(&b, "  %-24s %.2f [%s] %s\n", s.Verdict, s.Coverage, strings.Join(s.Matched, " "), factLine(s))
		}
	}
	b.WriteString("final:\n")
	if len(t.Final) == 0 {
		b.WriteString("  nothing\n")
	}
	for _, s := range t.Final {
		fmt.Fprintf(&b, "  %-24s %s\n", s.Verdict, factLine(s))
	}
	_, err := io.WriteString(out, b.String())
	return err
}

// explainFact follows one fact through the steps of a recall and names where it dropped out.
func explainFact(s *store.Store, t *recall.Trace, id string, out io.Writer) error {
	f, err := s.Get(id)
	if err != nil {
		return err
	}
	var b strings.Builder
	state := f.Status
	if f.Review != "" {
		state += ", " + f.Review
	}
	if f.Expires != "" {
		state += ", expires " + f.Expires
	}
	fmt.Fprintf(&b, "fact %s (%s): %s\n", f.ID, state, strings.Join(strings.Fields(f.Content), " "))
	if t.Stopped != "" {
		fmt.Fprintf(&b, "stopped: %s\n", t.Stopped)
	}

	if st, ok := findStep(t.Lexical, id); ok {
		fmt.Fprintf(&b, "lexical: %s, coverage %.2f of %.2f, terms [%s] of %d needed\n", st.Verdict, st.Coverage,
			t.MinCoverage, strings.Join(st.Matched, " "), t.MinMatched)
	} else if t.Stopped == "" {
		b.WriteString("lexical: not among the FTS5 results (holds none of the prompt terms, or others ranked higher)\n")
	}

	if st, ok := findStep(t.Final, id); ok {
		fmt.Fprintf(&b, "final: %s\n", st.Verdict)
	} else {
		b.WriteString("final: not reached\n")
	}
	_, err = io.WriteString(out, b.String())
	return err
}

func findStep(steps []recall.Step, id string) (recall.Step, bool) {
	for _, st := range steps {
		if st.ID == id {
			return st, true
		}
	}
	return recall.Step{}, false
}

// factLine is the id and the start of the text of a fact, on one line.
func factLine(s recall.Step) string {
	text := strings.Join(strings.Fields(s.Content), " ")
	if r := []rune(text); len(r) > explainWidth {
		text = string(r[:explainWidth]) + "…"
	}
	return s.ID + " | " + text
}
