//go:build research

// Command langpairs measures whether the dream meets two copies of one fact written in different languages (#392).
// For every live fact it runs the search the dream groups by, and writes two sets of pairs as JSON lines:
//
//   - "grouped": the pairs the dream's neighbour search links, with the language of each fact;
//   - "shared": pairs that share at least two rare identifiers (paths, versions, backticked names, dotted or
//     snake_case words), found without the lexer, so a cross-language pair that shares no stems is still listed.
//
// A labeller decides which "shared" pairs are duplicates; the share of those the dream links, by language pair, is
// the number the research needs. Experiment only, not part of memcue.
//
//	go run -tags research ./docs/research/scripts/langpairs FACTS.db > pairs.jsonl
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/juev/memcue/internal/recall"
	"github.com/juev/memcue/internal/store"
)

// The parameters of groupNeighbours in cmd/memcue/dream.go, copied: the command's package is main.
var group = recall.Params{
	Candidates: 80, MinTerms: 2, MinMatched: 3, MinCoverage: 0.35, MaxPromptTerms: 24, MaxFacts: 3, MinContained: 0.8,
}

var (
	reTick  = regexp.MustCompile("`[^`]+`")
	reIdent = regexp.MustCompile(`[A-Za-z0-9_.\-/:@]*[A-Za-z][A-Za-z0-9_.\-/:@]*`)
)

type pair struct {
	Set    string   `json:"set"`
	A      string   `json:"a"`
	B      string   `json:"b"`
	LangA  string   `json:"lang_a"`
	LangB  string   `json:"lang_b"`
	Shared []string `json:"shared,omitempty"`
	TextA  string   `json:"text_a"`
	TextB  string   `json:"text_b"`
}

// lang names the language most letters outside backticks belong to.
func lang(s string) string {
	s = reTick.ReplaceAllString(s, " ")
	var cy, la int
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Cyrillic, r):
			cy++
		case r < unicode.MaxASCII && unicode.IsLetter(r):
			la++
		}
	}
	if cy > la {
		return "ru"
	}
	return "en"
}

// idents are the identifiers of a fact: words that hold a digit, a path or dot separator, an underscore, or an
// inner capital, and every backticked span. Plain English words are left out, so an English fact does not share
// "the" with another one.
func idents(s string) map[string]bool {
	out := map[string]bool{}
	for _, m := range reTick.FindAllString(s, -1) {
		out[strings.ToLower(strings.Trim(m, "`"))] = true
	}
	for _, w := range reIdent.FindAllString(s, -1) {
		w = strings.Trim(w, ".:/-")
		if len(w) < 4 {
			continue
		}
		if strings.ContainsAny(w, "0123456789_./:@") || strings.ToLower(w[1:]) != w[1:] {
			out[strings.ToLower(w)] = true
		}
	}
	return out
}

func main() {
	s, err := store.Open(os.Args[1])
	if err != nil {
		panic(err)
	}
	defer func() { _ = s.Close() }()
	active, err := s.Active()
	if err != nil {
		panic(err)
	}
	byID := map[string]store.Fact{}
	langs := map[string]string{}
	ids := map[string]map[string]bool{}
	df := map[string]int{}
	for _, f := range active {
		byID[f.ID] = f
		langs[f.ID] = lang(f.Content)
		ids[f.ID] = idents(f.Content)
		for w := range ids[f.ID] {
			df[w]++
		}
	}
	enc := json.NewEncoder(os.Stdout)
	emit := func(set, a, b string, shared []string) {
		if a > b {
			a, b = b, a
		}
		if err := enc.Encode(pair{set, a, b, langs[a], langs[b], shared, byID[a].Content, byID[b].Content}); err != nil {
			panic(err)
		}
	}
	seen := map[[2]string]bool{}
	for _, f := range active {
		p := group
		p.Seen = map[string]bool{f.ID: true}
		hits, err := recall.Recall(s, f.Content+" "+strings.Join(f.Keywords, " "), p)
		if err != nil {
			panic(err)
		}
		for _, h := range hits {
			if _, ok := byID[h.ID]; !ok || h.ID == f.ID {
				continue
			}
			k := [2]string{min(f.ID, h.ID), max(f.ID, h.ID)}
			if !seen[k] {
				seen[k] = true
				emit("grouped", k[0], k[1], nil)
			}
		}
	}
	// Rare: an identifier held by at most five facts; a common one (a host name, the repository) links everything.
	post := map[string][]string{}
	for id, ws := range ids {
		for w := range ws {
			if df[w] <= 5 {
				post[w] = append(post[w], id)
			}
		}
	}
	shared := map[[2]string][]string{}
	for w, list := range post {
		sort.Strings(list)
		for i := range list {
			for j := i + 1; j < len(list); j++ {
				k := [2]string{list[i], list[j]}
				shared[k] = append(shared[k], w)
			}
		}
	}
	n := 0
	for k, ws := range shared {
		if len(ws) < 2 {
			continue
		}
		sort.Strings(ws)
		emit("shared", k[0], k[1], ws)
		n++
	}
	cnt := map[string]int{}
	for _, l := range langs {
		cnt[l]++
	}
	fmt.Fprintf(os.Stderr, "facts %d (%v), grouped pairs %d, shared pairs %d\n", len(active), cnt, len(seen), n)
}
