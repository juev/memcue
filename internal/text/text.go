// Package text turns notes and prompts into the stems memcue indexes and matches.
//
// FTS5 has no Russian stemmer, so stems are computed here and stored pre-tokenized. Bare numbers, versions, ports
// and addresses become one exact term each, so "Go 1.27" never matches notes about other versions; with
// MEMCUE_NUMBER_TERMS=0 they are dropped instead. An uppercase ticket key such as PROJ-1234, #123 or !45 is kept as
// one exact term, because a ticket is a precise anchor and losing its number loses the fact. A versioned name such as
// PostgreSQL-16 is not a ticket: its word stays an ordinary word next to the number.
package text

import (
	"os"
	"regexp"
	"strings"
	"unicode"

	"github.com/kljensen/snowball/english"
	"github.com/kljensen/snowball/russian"
)

var (
	// v0.21.6, 1.27, 2026-10-19: never a match.
	reNumeric = regexp.MustCompile(`(?:^|[\s(\[{"'@:/])(?:[#!]\d+|v?\d+(?:[.\-:/]\d+)+)\b`)
	// PROJ-1234, JIRA-12, #123, !45: one exact term. Only an uppercase project key is a ticket; a versioned name
	// such as PostgreSQL-16 keeps its word and its number is dropped.
	reTicket = regexp.MustCompile(`\b[A-Z][A-Z0-9]*-\d+\b|#\d+|!\d+`)
	// Ticket terms carry a tk prefix and always end in a digit; ordinary identifiers such as s3 and k8s do not.
	reTicketTerm = regexp.MustCompile(`^tk(?:h|b)?[a-z0-9]*\d+$`)
	// reNumberWord is the part of reNumeric that becomes a numeric term: a version, a date, a port or an address
	// after a boundary. A ticket (#123) leaves the text before reNumeric runs and never gives one, so it is not
	// here; neither is a number inside a word (PostgreSQL-16, gpt-6.1-sol), which has no boundary before it.
	reNumberWord = regexp.MustCompile(`(?:^|[\s(\[{"'@:/])[vV]?\d+(?:[.\-:/]\d+)+\b`)
)

// HasNumberTerm reports whether s holds a word Stems would turn into a numeric term. It answers the same question
// as scanning Stems for IsNumberTerm at a fraction of the price, which matters where every fact carrying a digit is
// asked on every open: the index repair does exactly that.
func HasNumberTerm(s string) bool {
	if !NumbersEnabled() {
		return false
	}
	return reNumberWord.MatchString(s)
}

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

// NumberTermsVersion is the version of the number terms an index row is stamped with when they are on. Bump it when
// the lexer starts giving a term to a number it dropped before: the next open of a base stems its older rows again.
// Version 2 gives a term to a number after ":" or "/": an image tag (vmalert:v1.106.1), a path (gradle/9.8.1/).
const NumberTermsVersion = 2

// NumbersEnabled reports whether bare numbers, versions, ports and addresses are indexed as one exact term
// instead of being dropped. On by default: the term is exact ("nm1x27" never matches "nm1x20"), so it can only add
// candidates and rank the prompt's own version above the others. MEMCUE_NUMBER_TERMS=0 (or false, off, no) keeps
// the word-only index of the releases before it.
func NumbersEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("MEMCUE_NUMBER_TERMS"))) {
	case "0", "false", "off", "no":
		return false
	}
	return true
}

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
	var nums []string
	if NumbersEnabled() {
		s = reNumeric.ReplaceAllStringFunc(s, func(m string) string {
			nums = append(nums, numberTerm(m))
			return " "
		})
	}
	s = reNumeric.ReplaceAllString(s, " ")
	out := strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	return append(append(out, tickets...), nums...)
}

// numberTerm normalizes a version, date, port or address into one FTS token: the "nm" prefix keeps it apart from
// an ordinary word or a ticket, and every separator becomes "x", so the unicode61 tokenizer cannot split it. The
// boundary character the regexp swallowed (space, paren, quote, the @ of dig @1.1.1.1) is dropped first.
func numberTerm(s string) string {
	s = strings.TrimLeft(s, " 	\n([{\"'@:/")
	s = strings.ToLower(s)
	var b strings.Builder
	b.WriteString("nm")
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		default:
			b.WriteRune('x')
		}
	}
	return b.String()
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

// IsNumberTerm reports whether term is a normalized version, date, port or address ("nm1x27"). Such a term is
// exact, so a prompt made of one is answered instead of silenced; it never filters, only adds and ranks.
func IsNumberTerm(term string) bool { return strings.HasPrefix(term, "nm") && isNumberWord(term[2:]) }

// isNumberWord reports whether s looks like the body of a number term: at least one digit and nothing but
// letters, digits and the "x" separators numberTerm writes.
func isNumberWord(s string) bool {
	digit := false
	for _, r := range s {
		switch {
		case unicode.IsDigit(r):
			digit = true
		case unicode.IsLetter(r), r == 'x':
		default:
			return false
		}
	}
	return digit
}

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
