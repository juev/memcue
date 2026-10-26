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
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/juev/memcue/internal/store"
	"github.com/juev/memcue/internal/text"
)

// The signals of installation state (#426). Each one fires only together with a word near it, so a fact that merely
// names a version or a path is left alone. The text is lower-cased before matching.
var (
	// semVer is a release number such as 1.2.3 or v0.40.1; an address such as 10.0.0.1 is filtered out by the caller.
	semVer = regexp.MustCompile(`v?\d+\.\d+\.\d+`)
	// installWord says the number is the version of something installed, not a value of some other kind.
	installWord = regexp.MustCompile(`верси|version|установлен|installed|бинар|binar|обёртк|обертк|wrapper|релиз|release|тег|tag|сборк|build`)
	// installPath is a place binaries, wrappers and plugins are installed to: the part of a base that carries the
	// tool itself. A service's directory or a data path is knowledge a command does not give, so it is not here.
	installPath = regexp.MustCompile(`/(?:bin|plugins)/`)
	// placeWord says the path is where something lies, not an argument of a command.
	placeWord = regexp.MustCompile(`лежит|лежат|находится|хранится|пут[ьи]|path|stored`)
	// defaultWord marks a config default.
	defaultWord = regexp.MustCompile(`по умолчанию|by default|default`)
	// configKey is a dotted key such as memory.memcue.search_facts; a file name such as config.yaml is filtered out by
	// the caller.
	configKey = regexp.MustCompile(`[a-z_][a-z0-9_]*(?:\.[a-z_][a-z0-9_]*)+`)
	// fileExt is the last part of a dotted name that makes it a file, not a config key.
	fileExt = regexp.MustCompile(`^(?:ya?ml|json|toml|ini|conf|env|log|md|txt|go|py|sh|js|ts|db|sqlite)$`)
	// hostExt is the last part of a dotted name that makes it a host name (a private forge, a site), not a key.
	hostExt = regexp.MustCompile(`^(?:com|org|net|io|ru|de|me|co|ai|app|cloud|dev|local|internal|online|site|tech|xyz|info)$`)
	// baseSize is the size of a base or the count of its facts.
	baseSize = regexp.MustCompile(`живых фактов|фактов в базе|live facts|facts in the base|размер базы|занимает \d+(?:[.,]\d+)? ?(?:кб|мб|гб|kb|mb|gb)`)
)

// nearRunes is how far, in characters either side, a signal looks for the word that confirms it.
const nearRunes = 60

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
// whose note was deleted would keep a deleted memory alive, approving a live one only makes it recallable, and a live
// one whose words an approved fact already holds is a second copy of that fact and is dropped (#458). The notes
// are given as the whole text of their directory; haveMemory says whether that directory was found at all, because
// an unreadable directory must not turn every mirror into "stale".
//
// An origin whose tier is exactly "test" is a test run that wrote to a live base: it never belongs there. Origins are
// "<tier> <date>", so a repository called test-utils or testing must not be mistaken for one.
//
// An environment fact (or one of no kind) that only records installation state is dropped: a command reads it, and
// the stored copy is stale after the next release (#426). A rule, a decision, a pitfall or a procedure that names a
// version is knowledge, not state, and is left alone.
//
// A candidate whose words are all in an approved fact says nothing new and would only take room in a prompt.
func Rules(cands, approved []store.Fact, memory string, haveMemory bool) (decided []Decision, rest []store.Fact) {
	notes := norm(memory)
	for _, c := range cands {
		state := ""
		if stateKind(c.Kind) {
			state = installationState(c)
		}
		switch {
		case c.Source == "memory" && haveMemory:
			// A live note is kept unless the base already states it: a mirror of a rule that is live would put the
			// rule in the base twice (#458).
			cover, covered := covers(c, approved)
			switch {
			case !HoldsNote(notes, c.Content):
				decided = append(decided, Decision{Fact: c, Verdict: Drop, By: "rule",
					Reason: "stale mirror: the built-in memory no longer holds it"})
			case covered:
				decided = append(decided, Decision{Fact: c, Verdict: Drop, By: "rule",
					Reason: fmt.Sprintf("mirror of a note the base already holds: covered by approved fact %s", cover)})
			default:
				decided = append(decided, Decision{Fact: c, Verdict: Keep, By: "rule",
					Reason: "mirror of the built-in memory, the note still holds it"})
			}
		case isTestOrigin(c.Origin):
			decided = append(decided, Decision{Fact: c, Verdict: Drop, By: "rule",
				Reason: "origin=test: a test run wrote it"})
		case state != "":
			decided = append(decided, Decision{Fact: c, Verdict: Drop, By: "rule",
				Reason: fmt.Sprintf("installation state: the command that reads %s is the answer", state)})
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

// stateKind reports whether a fact of this kind can be mere installation state: knowledge kinds never are.
func stateKind(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "", "environment", "other":
		return true
	}
	return false
}

// installationState reports whether a fact only records the state of an installation: the version of something
// installed, the path of a binary or a wrapper, the variables a wrapper exports, a config default, or the size and
// the fact count of a base. A command reads each of these (#426), and the stored copy is stale after the next
// release; a rule, a decision or a pitfall that merely names a version is not this.
//
// It names the part found — version, path, config default or size — or returns "" when there is none. When in doubt
// it returns "": a wrong drop loses knowledge, an undecided candidate only costs a line in a report.
func installationState(c store.Fact) string {
	s := strings.ToLower(c.Content)
	switch {
	case near(s, semVer, installWord, isVersion):
		return "version"
	case near(s, installPath, placeWord, nil) || near(s, installPath, installWord, nil):
		return "path"
	case near(s, configKey, defaultWord, isConfigKey):
		return "config default"
	case baseSize.MatchString(s):
		return "size"
	}
	return ""
}

// near reports whether a match of sig, accepted by ok when ok is set, has a match of word within nearRunes
// characters either side of it.
func near(s string, sig, word *regexp.Regexp, ok func(s string, at []int) bool) bool {
	for _, at := range sig.FindAllStringIndex(s, -1) {
		if ok != nil && !ok(s, at) {
			continue
		}
		if word.MatchString(s[back(s, at[0], nearRunes):ahead(s, at[1], nearRunes)]) {
			return true
		}
	}
	return false
}

// isVersion rejects a number that is part of a longer dotted one or of a word: 10.0.0.1 is an address, not 10.0.0.
func isVersion(s string, at []int) bool {
	if at[0] > 0 {
		if r, _ := utf8.DecodeLastRuneInString(s[:at[0]]); r == '.' || isWord(r) {
			return false
		}
	}
	if at[1] < len(s) {
		r, _ := utf8.DecodeRuneInString(s[at[1]:])
		if isWord(r) || r == '.' && at[1]+1 < len(s) && s[at[1]+1] >= '0' && s[at[1]+1] <= '9' {
			return false
		}
	}
	return true
}

// isConfigKey rejects a dotted name that is a file (config.yaml) or a part of a path or a longer word.
func isConfigKey(s string, at []int) bool {
	if at[0] > 0 {
		if r, _ := utf8.DecodeLastRuneInString(s[:at[0]]); r == '/' || r == '.' || r == '-' || isWord(r) {
			return false
		}
	}
	key := s[at[0]:at[1]]
	ext := key[strings.LastIndexByte(key, '.')+1:]
	return !fileExt.MatchString(ext) && !hostExt.MatchString(ext)
}

// back returns the byte offset n characters before i in s, or 0.
func back(s string, i, n int) int {
	for ; n > 0 && i > 0; n-- {
		_, size := utf8.DecodeLastRuneInString(s[:i])
		i -= size
	}
	return i
}

// ahead returns the byte offset n characters after i in s, or len(s).
func ahead(s string, i, n int) int {
	for ; n > 0 && i < len(s); n-- {
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
	}
	return i
}

// HoldsNote reports whether the built-in memory notes hold a fact's text: a mirror of the notes lives only while its
// note does, so the rule that retires a stale mirror and the rule that approves a live one ask the same question.
// Word boundaries matter: a one-word mirror such as "Go" must not match "Django" or "Golang".
func HoldsNote(notes, content string) bool {
	key := norm(content)
	return key != "" && strings.Contains(" "+norm(notes)+" ", " "+key+" ")
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
