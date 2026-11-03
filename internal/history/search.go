package history

import (
	"sort"
	"strings"
	"time"
	"unicode"
)

// stop are the function words of a query, left out of its terms: the list of the #602 parser.
var stop = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(`a an and are as at be by can do does for from has have how i if in into is it
		its me my no not of on or our so that the their then there these this to was we what when where which who why
		will with you your и в во не на что как к ко с со по о об от до за из для это этот эта эти то же ли или а но
		бы был была были есть у мы вы я он она они его её их как где когда какой какая какие чем там тут уже ещё`) {
		m[w] = true
	}
	return m
}()

// Terms are the distinct lowercase words of a query, in its order: runs of letters, digits and underscores, with
// dots and hyphens inside (a version, a hyphenated name), at least two characters long and not stop words.
func Terms(query string) []string {
	var out []string
	seen := map[string]bool{}
	for _, t := range words(strings.ToLower(query)) {
		if len([]rune(t)) >= 2 && !stop[t] && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// words cuts s the way the regular expression \w(?:[\w.\-]*\w)? does: a word character opens and closes a word, and
// dots and hyphens may sit inside.
func words(s string) []string {
	var out []string
	rs := []rune(s)
	for i := 0; i < len(rs); {
		if !wordRune(rs[i]) {
			i++
			continue
		}
		j := i + 1
		end := j
		for j < len(rs) && (wordRune(rs[j]) || rs[j] == '.' || rs[j] == '-') {
			j++
			if wordRune(rs[j-1]) {
				end = j
			}
		}
		out = append(out, string(rs[i:end]))
		i = j
	}
	return out
}

func wordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsNumber(r)
}

// MinMatch is how many of n query words a message must hold: 1 of 1, 2 of 2 to 4, half of 5 or more.
func MinMatch(n int) int {
	switch {
	case n <= 2:
		return n
	case n <= 4:
		return 2
	}
	return (n + 1) / 2
}

// Window is a passage of at most 2*half characters of text around the spot that holds the most of the words; a text
// that fits is returned whole. A cut end is marked with an ellipsis.
func Window(text string, terms []string, half int) string {
	rs := []rune(text)
	if len(rs) <= 2*half {
		return text
	}
	low := []rune(strings.ToLower(text))
	if len(low) != len(rs) {
		low = rs // a case change that moves the characters: judge the original, as the match did not
	}
	best, bestPos := -1, 0
	for _, w := range terms {
		needle := []rune(w)
		for start := 0; ; {
			i := index(low, needle, start)
			if i < 0 {
				break
			}
			lo, hi := max(0, i-half), min(len(low), i+half)
			seen := 0
			for _, v := range terms {
				if index(low[lo:hi], []rune(v), 0) >= 0 {
					seen++
				}
			}
			if seen > best {
				best, bestPos = seen, i
			}
			start = i + 1
		}
	}
	lo := max(0, bestPos-half)
	hi := min(len(rs), lo+2*half)
	lo = max(0, hi-2*half)
	out := string(rs[lo:hi])
	if lo > 0 {
		out = "…" + out
	}
	if hi < len(rs) {
		out += "…"
	}
	return out
}

// index is the position of needle in hay at or after from, or -1.
func index(hay, needle []rune, from int) int {
	for i := from; i+len(needle) <= len(hay); i++ {
		j := 0
		for j < len(needle) && hay[i+j] == needle[j] {
			j++
		}
		if j == len(needle) {
			return i
		}
	}
	return -1
}

// Hit is one passage found: the window and the message it comes from.
type Hit struct {
	Text    string
	Matched int
	Message Message
}

// Result is what a search found and what it left out.
type Result struct {
	Terms    []string
	MinMatch int
	// Candidates are the messages that hold enough of the words, before the time and the sessions were applied;
	// Future those at or after the time, Excluded those of the sessions left out, Repeats the windows that repeated
	// a newer one.
	Candidates, Future, Excluded, Repeats int
	Hits                                  []Hit
}

// Search finds the k newest messages written before the time that hold enough of the query's words, outside the
// sessions excluded, and cuts a window of 2*half characters from each; a window that repeats a newer one is left out.
// The time is a strict bound: a message at or after it is not history yet. A query without terms finds nothing.
func Search(msgs []Message, query string, before time.Time, exclude map[string]bool, k, half int) Result {
	terms := Terms(query)
	res := Result{Terms: terms, MinMatch: MinMatch(len(terms))}
	if len(terms) == 0 {
		return res
	}
	type cand struct {
		m   Message
		got int
	}
	var cands []cand
	for _, m := range msgs {
		low := strings.ToLower(m.Text)
		got := 0
		for _, w := range terms {
			if strings.Contains(low, w) {
				got++
			}
		}
		if got < res.MinMatch {
			continue
		}
		switch {
		case !m.Time.Before(before):
			res.Future++
		case exclude[m.Session]:
			res.Excluded++
		default:
			cands = append(cands, cand{m, got})
		}
	}
	res.Candidates = len(cands)
	sort.SliceStable(cands, func(i, j int) bool {
		if !cands[i].m.Time.Equal(cands[j].m.Time) {
			return cands[i].m.Time.After(cands[j].m.Time)
		}
		return cands[i].m.UID > cands[j].m.UID
	})
	shown := map[string]bool{}
	for _, c := range cands {
		if len(res.Hits) >= k {
			break
		}
		w := Window(strings.Join(strings.Fields(c.m.Text), " "), terms, half)
		key := strings.ToLower(w)
		if shown[key] {
			res.Repeats++
			continue
		}
		shown[key] = true
		res.Hits = append(res.Hits, Hit{Text: w, Matched: c.got, Message: c.m})
	}
	return res
}
