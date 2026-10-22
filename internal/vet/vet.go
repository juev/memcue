// Package vet decides what can be decided about a candidate fact before a human is asked, and hands the rest on.
//
// A candidate is a fact the base stores but does not recall until someone approves it: everything extracted from a
// session and every mirror of the built-in memory notes. Most of that queue needs no judgement at all — a mirror the
// notes no longer hold, a trace of a test run, a fact an approved one already covers — and the rest is a question of
// durable knowledge against a session journal, which a model answers well enough to keep the queue from growing.
//
// Two rules matter here beyond the verdicts: a rule decides only what code can prove, and an undecided candidate
// stays a candidate. A wrong approval reaches every later prompt, while a wrong "unsure" only costs a line in a report.
package vet

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/juev/memcue/internal/store"
	"github.com/juev/memcue/internal/text"
)

// Verdict is what vet decided about a candidate.
type Verdict string

const (
	Keep   Verdict = "KEEP"   // take it into recall
	Drop   Verdict = "DROP"   // retire it: it is not worth a place in the base
	Unsure Verdict = "UNSURE" // leave it in the queue: a human or a later run decides
	Split  Verdict = "SPLIT"  // replace it with its parts, each a candidate of its own
)

// Decision is one verdict, with why and who made it.
type Decision struct {
	Fact        store.Fact
	Verdict     Verdict
	Reason      string
	Expires     string   // last day the fact is worth recalling, when the decision came with one
	Parts       []string // with Split: the simple facts the candidate holds
	By          string   // rule, model or policy
	Provisional bool     // keep it for a TTL instead of approving it
	// Failed says the step could not decide: a failed or empty model answer, or a malformed expiry. Such a candidate stays for the next run and never becomes provisional.
	Failed bool
}

// Rules decides what code can decide and returns the candidates it left for the model.
//
// A mirror of the built-in notes (Source memory) stays only while the notes still hold its text: approving a mirror
// whose note was deleted would keep a deleted memory alive, approving a live one only makes it recallable. The notes
// are given as the whole text of their directory; haveMemory says whether that directory was found at all, because
// an unreadable directory must not turn every mirror into "stale".
//
// An origin whose tier is exactly "test" is a test run that wrote to a live base: it never belongs there. Origins are
// "<tier> <date>", so a repository called test-utils or testing must not be mistaken for one.
//
// A candidate whose words are all in an approved fact says nothing new and would only take room in a prompt.
func Rules(cands, approved []store.Fact, memory string, haveMemory bool) (decided []Decision, rest []store.Fact) {
	notes := norm(memory)
	for _, c := range cands {
		switch {
		case c.Source == "memory" && haveMemory:
			// Word boundaries: a one-word mirror such as "Go" must not match "Django" or "Golang".
			if key := norm(c.Content); key != "" && strings.Contains(" "+notes+" ", " "+key+" ") {
				decided = append(decided, Decision{Fact: c, Verdict: Keep, By: "rule",
					Reason: "mirror of the built-in memory, the note still holds it"})
			} else {
				decided = append(decided, Decision{Fact: c, Verdict: Drop, By: "rule",
					Reason: "stale mirror: the built-in memory no longer holds it"})
			}
		case isTestOrigin(c.Origin):
			decided = append(decided, Decision{Fact: c, Verdict: Drop, By: "rule",
				Reason: "origin=test: a test run wrote it"})
		default:
			if cover, ok := covers(c, approved); ok {
				decided = append(decided, Decision{Fact: c, Verdict: Drop, By: "rule",
					Reason: fmt.Sprintf("covered by approved fact %s", cover)})
			} else {
				rest = append(rest, c)
			}
		}
	}
	return decided, rest
}

// isTestOrigin reports whether origin is a test run's: its tier, the first word of "<tier> <date>", has to be exactly
// "test". A prefix test would also catch real tiers such as test-utils, testing or test-infra and retire every fact
// extracted there.
func isTestOrigin(origin string) bool {
	fields := strings.Fields(strings.ToLower(origin))
	return len(fields) > 0 && fields[0] == "test"
}

// covers names an approved fact that already holds every word of the candidate. Equal text is a duplicate outright;
// otherwise the candidate has to be long enough that the words it shares are its whole content, not a topic it
// merely touches: a short fact inside a long one is usually a detail the long one does not state.
func covers(c store.Fact, approved []store.Fact) (string, bool) {
	key := distinct(text.Stems(c.Content))
	if len(key) == 0 {
		return "", false
	}
	want := norm(c.Content)
	for _, a := range approved {
		if a.ID == c.ID {
			continue
		}
		if want != "" && norm(a.Content) == want {
			return a.ID, true
		}
		other := distinct(text.Stems(a.Content))
		if len(key) < 6 || len(key) > len(other) {
			continue
		}
		if float64(len(key))/float64(len(other)) < 0.6 {
			continue
		}
		if hasAll(other, key) {
			return a.ID, true
		}
	}
	return "", false
}

func distinct(stems []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(stems))
	for _, s := range stems {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func hasAll(have, want []string) bool {
	set := map[string]bool{}
	for _, s := range have {
		set[s] = true
	}
	for _, s := range want {
		if !set[s] {
			return false
		}
	}
	return true
}

// isWord says what keeps a word together: letters of any script, digits and the underscore. Everything else is a
// separator, so punctuation and dashes fold away the same way they do in the index.
func isWord(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

// norm folds text for comparison: lower case, one space between words.
func norm(s string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !isWord(r) }), " ")
}
