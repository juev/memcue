// Package text turns notes and prompts into the stems memcue indexes and matches.
//
// FTS5 has no Russian stemmer, so stems are computed here and stored pre-tokenized. Bare numbers, versions, ports
// and addresses are dropped: matching "Go 1.27" against notes recalls notes about other versions. An uppercase
// ticket key such as PROJ-1234, #123 or !45 is kept as one exact term, because a ticket is a precise anchor and
// losing its number loses the fact. A versioned name such as PostgreSQL-16 is not a ticket: its word stays in the
// index and only the number is dropped.
package text

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/kljensen/snowball/english"
	"github.com/kljensen/snowball/russian"
)

var (
	// v0.21.6, 1.27, 2026-10-19: never a match.
	reNumeric = regexp.MustCompile(`(?:^|[\s(\[{"'])(?:[#!]\d+|v?\d+(?:[.\-:/]\d+)+)\b`)
	// PROJ-1234, JIRA-12, #123, !45: one exact term. Only an uppercase project key is a ticket; a versioned name
	// such as PostgreSQL-16 keeps its word and its number is dropped.
	reTicket = regexp.MustCompile(`\b[A-Z][A-Z0-9]*-\d+\b|#\d+|!\d+`)
	// Ticket terms carry a tk prefix and always end in a digit; ordinary identifiers such as s3 and k8s do not.
	reTicketTerm = regexp.MustCompile(`^tk(?:h|b)?[a-z0-9]*\d+$`)
)

// Stems returns the stems of every word of s in order, stop words and numbers left out. Notes are indexed with it.
func Stems(s string) []string {
	var out []string
	for _, w := range words(s) {
		if st, ok := stem(w); ok {
			out = append(out, st)
		}
	}
	return out
}

// Terms returns the distinct stems of a prompt: Stems without the words that say what to do rather than what about
// ("давай", "сделай", "check"), which match every note that mentions doing something.
func Terms(s string) []string {
	seen := map[string]bool{}
	var out []string
	for _, w := range words(s) {
		if promptStop[w] {
			continue
		}
		st, ok := stem(w)
		if !ok || seen[st] || promptStopStems[st] {
			continue
		}
		seen[st] = true
		out = append(out, st)
	}
	return out
}

// Snowball gives a verb and its noun different stems ("перезагрузить" → перезагруз, "перезагрузка" →
// перезагрузк). A Russian term of six letters or more therefore matches as the prefix of its stem without the last
// letter; shorter terms and other scripts match exactly.
const prefixMin = 6

// Prefix returns the part of term a stem must start with, and whether the match is by prefix at all.
func Prefix(term string) (string, bool) {
	r := []rune(term)
	if len(r) < prefixMin || !isCyrillic(r[0]) {
		return term, false
	}
	return string(r[:len(r)-1]), true
}

// Covers reports whether a stem of a note matches a term of a prompt.
func Covers(term, stem string) bool {
	p, ok := Prefix(term)
	if !ok {
		return stem == term
	}
	return strings.HasPrefix(stem, p)
}

// Query returns the FTS5 query token for term: a quoted string, with * when it matches by prefix.
func Query(term string) string {
	p, ok := Prefix(term)
	q := `"` + strings.ReplaceAll(p, `"`, `""`) + `"`
	if ok {
		q += "*"
	}
	return q
}

func isCyrillic(r rune) bool { return r >= 'а' && r <= 'я' }

// words splits s into words, taking ticket keys out first so their number survives. Tickets are recognized on the
// original text: only an uppercase key is one, so a versioned name such as PostgreSQL-16 stays an ordinary word and
// only its number is dropped.
func words(s string) []string {
	var tickets []string
	s = reTicket.ReplaceAllStringFunc(s, func(m string) string {
		tickets = append(tickets, ticketTerm(m))
		return " "
	})
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "ё", "е")
	s = reNumeric.ReplaceAllString(s, " ")
	out := strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	return append(out, tickets...)
}

// ticketTerm normalizes a ticket key into one FTS token. The prefix keeps it distinct from a bare number (#123)
// and from an ordinary identifier; a hyphen becomes nothing, so the unicode61 tokenizer cannot split the key. The
// key is lowercased so a base and a prompt always agree.
func ticketTerm(s string) string {
	s = strings.ToLower(s)
	switch s[0] {
	case '#':
		return "tkh" + s[1:]
	case '!':
		return "tkb" + s[1:]
	}
	var b strings.Builder
	b.WriteString("tk")
	for _, r := range s {
		if r != '-' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// IsTicket reports whether term is a normalized ticket key. Recall keeps such a term exact and lets it pass the
// minimum-term gate alone, but never matches a different ticket number.
func IsTicket(term string) bool { return reTicketTerm.MatchString(term) }

func stem(w string) (string, bool) {
	if stop[w] || isNumber(w) {
		return "", false
	}
	cyr, lat, digit := 0, 0, 0
	for _, r := range w {
		switch {
		case r >= 'а' && r <= 'я':
			cyr++
		case r >= 'a' && r <= 'z':
			lat++
		case unicode.IsDigit(r):
			digit++
		}
	}
	switch {
	case cyr > 0 && lat == 0 && digit == 0:
		if cyr < 2 { // two letters are mostly function words, and those are in stop; МР, БД stay
			return "", false
		}
		return russian.Stem(w, false), true
	case lat > 0 && cyr == 0 && digit == 0:
		if lat < 2 {
			return "", false
		}
		return english.Stem(w, false), true
	case digit > 0 && cyr+lat > 0:
		return w, len(w) >= 2 // identifiers such as s3, k8s, gpt6: as written
	}
	return "", false
}

func isNumber(w string) bool {
	for _, r := range w {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}
