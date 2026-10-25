// Package policy decides in code which extracted facts are stored. It cannot judge whether a fact is worth keeping
// (the user does that); it rejects what cannot be trusted or does not belong in the base: a kind that is not kept,
// a fact that rests on the agent's own words, a rule or decision the user did not state, a quote that is not in the
// part of the record its source names, an environment fact without a scope, and anything that looks like a secret.
package policy

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/juev/memcue/internal/extract"
)

// Kept are the kinds the base keeps.
var Kept = map[string]bool{"rule": true, "decision": true, "pitfall": true, "environment": true}

// roleOf names the blocks of the rendered record a source's evidence must come from: the user's words for the user,
// tool output for a tool. The agent's own words are no evidence: a fact needs the user or a tool behind it.
var roleOf = map[string]string{"user": "USER", "user_confirmed": "USER", "tool": "RESULT"}

var (
	// a token in a known format
	token = regexp.MustCompile(`(?i)(sk-[a-z0-9_-]{20,}|gh[pousr]_[a-z0-9]{30,}|github_pat_[a-z0-9_]{30,}|` +
		`akia[0-9a-z]{16}|-----begin [a-z ]*private key-----)`)
	// a word for a secret, up to four words, then ":" or "=" and a value
	labelled = regexp.MustCompile(`(?i)(?:^|[^\p{L}\p{N}_])(password|passwd|pwd|passphrase|token|secret|api[_ -]?key|парол\p{L}*|` +
		`токен\p{L}*|секрет\p{L}*|ключ\p{L}* (?:api|доступа))(?:\s+[^\s:=]+){0,4}?\s*[:=]\s*["'«]?([^\s"'»,;]+)`)
	// a rendered record block: USER, ASSISTANT, TOOL <name>, RESULT or RESULT <tool> at the start of the record or
	// after an empty line
	block = regexp.MustCompile(`(?:^|\n\n)(USER|ASSISTANT|TOOL [^:\n]*|RESULT(?: [^:\n]*)?): `)
)

// Check says whether f may be stored, given the rendered record it was extracted from; when not, it says why.
func Check(f extract.Fact, record string) (ok bool, reason string) {
	role, known := roleOf[f.Source]
	switch {
	case !Kept[f.Kind]:
		return false, "kind " + quote(f.Kind) + " is not kept"
	case (f.Kind == "rule" || f.Kind == "decision") && f.Source != "user" && f.Source != "user_confirmed":
		return false, "a " + f.Kind + " needs the user's words, source is " + quote(f.Source)
	case !known:
		return false, "source " + quote(f.Source) + " is not the user or a tool"
	case strings.TrimSpace(f.Content) == "":
		return false, "empty content"
	case strings.TrimSpace(f.Evidence) == "":
		return false, "no evidence"
	case !quoted(record, role, f.Evidence):
		return false, "evidence is not in a " + role + " block"
	case f.Kind == "environment" && strings.TrimSpace(f.Scope) == "":
		return false, "an environment fact needs a scope"
	case Secret(f.Content) || Secret(f.Evidence):
		return false, "looks like a secret"
	}
	return true, ""
}

// quoted says whether evidence is in one of the record's blocks of role; a RESULT block named by its tool is a
// RESULT block. White space is folded, so a quote that crosses a line break still matches.
func quoted(record, role, evidence string) bool {
	for _, p := range Where(record, evidence) {
		if p.Label == role || strings.HasPrefix(p.Label, role+" ") {
			return true
		}
	}
	return false
}

// Place is a block of the rendered record: its label (USER, ASSISTANT, TOOL <name>, RESULT, RESULT <tool>) and,
// for a result named by its tool, the tool.
type Place struct {
	Label string
	Tool  string
}

// Where lists the blocks of record that hold evidence, in the record's order. White space is folded, as in Check.
func Where(record, evidence string) []Place {
	want := squash(evidence)
	if want == "" {
		return nil
	}
	var out []Place
	locs := block.FindAllStringSubmatchIndex(record, -1)
	for i, l := range locs {
		end := len(record)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		if !strings.Contains(squash(record[l[1]:end]), want) {
			continue
		}
		label := record[l[2]:l[3]]
		p := Place{Label: label}
		if tool, ok := strings.CutPrefix(label, "RESULT "); ok {
			p.Tool = tool
		}
		out = append(out, p)
	}
	return out
}

// External says whether f is a fact read from the outside world (#433): a tool-sourced fact whose evidence is quoted
// from the result of an external tool and from no other tool's result. The user's words are never external, whatever
// the tools returned: the user already chose to say them.
func External(f extract.Fact, record string) bool {
	if roleOf[f.Source] != "RESULT" {
		return false
	}
	found := false
	for _, p := range Where(record, f.Evidence) {
		if p.Label != "RESULT" && p.Tool == "" {
			continue
		}
		if !ExternalTool(p.Tool) {
			return false
		}
		found = true
	}
	return found
}

// ExternalTool says whether a tool reads the outside world: a web page, a browser, a fetched document, a paper. It is
// a list to review, not a heuristic: a tool that fits none of the prefixes is ours (a shell, a file, a host, memcue),
// and a shell command that fetches a URL is not marked.
func ExternalTool(name string) bool {
	name = strings.ToLower(name)
	for _, p := range []string{"web_", "browser", "fetch", "http", "curl", "arxiv", "pdf"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

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

// squash folds runs of white space into one space.
func squash(s string) string { return strings.Join(strings.Fields(s), " ") }

func quote(s string) string {
	if s == "" {
		return `""`
	}
	return s
}
