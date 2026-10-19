// Package text turns notes and prompts into the stems memcue indexes and matches.
//
// FTS5 has no Russian stemmer, so stems are computed here and stored pre-tokenized. Numbers, versions and issue
// references are dropped: matching "Go 1.27" or "#20" against notes recalls notes about other versions and issues.
package text

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/kljensen/snowball/english"
	"github.com/kljensen/snowball/russian"
)

var (
	// v0.21.6, 1.27, 2026-10-19, #20, !123: never a match.
	reNumeric = regexp.MustCompile(`(?:^|[\s(\[{"'])(?:[#!]\d+|v?\d+(?:[.\-:/]\d+)+)\b`)
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

func words(s string) []string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "ё", "е")
	s = reNumeric.ReplaceAllString(s, " ")
	return strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
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
