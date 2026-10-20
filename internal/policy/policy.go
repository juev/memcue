// Package policy decides in code which extracted facts are stored. It cannot judge whether a fact is worth keeping
// (the user does that); it rejects what cannot be trusted or does not belong in the base: a kind that is not kept,
// a rule or decision the user did not state, a fact whose quoted evidence is not in the source, an environment fact
// without a scope, and anything that looks like a secret.
package policy

import (
	"regexp"
	"strings"

	"github.com/juev/memcue/internal/extract"
)

// Kept are the kinds the base keeps.
var Kept = map[string]bool{"rule": true, "decision": true, "pitfall": true, "environment": true}

var secret = regexp.MustCompile(`(?i)(sk-[a-z0-9_-]{20,}|gh[pousr]_[a-z0-9]{30,}|github_pat_[a-z0-9_]{30,}|` +
	`akia[0-9a-z]{16}|-----begin [a-z ]*private key-----|` +
	`\b(password|passwd|pwd|token|secret|api[_-]?key)\s*[:=]\s*["']?[^\s"']{6,})`)

// Check says whether f may be stored, given the source text it was extracted from; when not, it says why.
func Check(f extract.Fact, source string) (ok bool, reason string) {
	switch {
	case !Kept[f.Kind]:
		return false, "kind " + quote(f.Kind) + " is not kept"
	case (f.Kind == "rule" || f.Kind == "decision") && f.Source != "user" && f.Source != "user_confirmed":
		return false, "a " + f.Kind + " needs the user's words, source is " + quote(f.Source)
	case strings.TrimSpace(f.Evidence) == "":
		return false, "no evidence"
	case !strings.Contains(squash(source), squash(f.Evidence)):
		return false, "evidence is not in the source"
	case f.Kind == "environment" && strings.TrimSpace(f.Scope) == "":
		return false, "an environment fact needs a scope"
	case secret.MatchString(f.Content) || secret.MatchString(f.Evidence):
		return false, "looks like a secret"
	}
	return true, ""
}

// squash folds runs of white space into one space, so a quote that crosses a line break of the source still matches.
func squash(s string) string { return strings.Join(strings.Fields(s), " ") }

func quote(s string) string {
	if s == "" {
		return `""`
	}
	return s
}
