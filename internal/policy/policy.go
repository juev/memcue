// Package policy decides in code which extracted facts are stored. It cannot judge whether a fact is worth keeping
// (the user does that); it rejects what cannot be trusted or does not belong in the base: a kind that is not kept,
// a fact that rests on the agent's own words, a rule or decision the user did not state, a quote that is not in the
// part of the record its source names, an environment fact without a scope, and anything that looks like a secret.
package policy

import (
	"regexp"
	"strings"
	"unicode"
)

// Kept are the kinds the base keeps.
var Kept = map[string]bool{"rule": true, "decision": true, "pitfall": true, "environment": true}

var (
	// a token in a known format
	token = regexp.MustCompile(`(?i)(sk-[a-z0-9_-]{20,}|gh[pousr]_[a-z0-9]{30,}|github_pat_[a-z0-9_]{30,}|` +
		`akia[0-9a-z]{16}|-----begin [a-z ]*private key-----)`)
	// a word for a secret, up to four words, then ":" or "=" and a value
	labelled = regexp.MustCompile(`(?i)(?:^|[^\p{L}\p{N}_])(password|passwd|pwd|passphrase|token|secret|api[_ -]?key|парол\p{L}*|` +
		`токен\p{L}*|секрет\p{L}*|ключ\p{L}* (?:api|доступа))(?:\s+[^\s:=]+){0,4}?\s*[:=]\s*["'«]?([^\s"'»,;]+)`)
)

// Secret says whether text holds a token in a known format, or a labelled value: after a password, secret or key
// label any value of four or more characters; after a token label a value of six or more characters with a letter in
// it, so "token budget: 150000" is not one. The value is measured as written: signs in it (base64 padding, a "!")
// count toward its length.
func Secret(text string) bool {
	if token.MatchString(text) {
		return true
	}
	for _, m := range labelled.FindAllStringSubmatch(text, -1) {
		v := []rune(m[2])
		if tokenLabel.MatchString(m[1]) {
			if len(v) >= 6 && strings.IndexFunc(string(v), unicode.IsLetter) >= 0 {
				return true
			}
		} else if len(v) >= 4 {
			return true
		}
	}
	return false
}

var tokenLabel = regexp.MustCompile(`(?i)^(token|токен\p{L}*)$`)
