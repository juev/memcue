// Command history575 is the research tool of #575: what a compaction of the text history would keep, and which
// merges it would change.
//
//	go run ./docs/research/scripts/history575 cases
//	go run ./docs/research/scripts/history575 count DIR...
//
// cases replays the merge scenarios of internal/store/merge_test.go on in-memory histories through
// store.MergeHistories, the rule of the git merge driver, of a merge of two bases and of the cache sync alike. Each
// scenario runs once with full histories and once per candidate rule, with each side compacting its history after
// every write and merge it makes (the worst case: a rule applied on write). It prints, per rule, whether the current
// version after merging both ways is the one full histories give.
//
// count reads the fact files under each DIR (a text history: DIR/<c>/<ID>.jsonl) and prints the facts, versions and
// bytes each rule would keep. It reads only; nothing is written.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/juev/memcue/internal/store"
)

// A rule keeps some versions of one fact's history, in order, with their numbers; now is when it runs.
type rule struct {
	name string
	keep func(h []store.Fact, now time.Time) []store.Fact
}

var rules = []rule{
	{"full", func(h []store.Fact, _ time.Time) []store.Fact { return h }},
	{"latest", func(h []store.Fact, _ time.Time) []store.Fact { return tail(h, 1) }},
	{"latest+before-retire", func(h []store.Fact, _ time.Time) []store.Fact {
		if n := len(h); n > 1 && h[n-1].Status == store.Retired {
			return tail(h, 2)
		}
		return tail(h, 1)
	}},
	{"last-two", func(h []store.Fact, _ time.Time) []store.Fact { return tail(h, 2) }},
	// From the version before the last change of the text to the end: what correctionLast compares a version with.
	{"since-last-edit", func(h []store.Fact, _ time.Time) []store.Fact {
		for i := len(h) - 1; i > 0; i-- {
			if h[i].Content != h[i-1].Content {
				return h[i-1:]
			}
		}
		return tail(h, 1)
	}},
	{"superseded-within-30d", within(30)},
	{"superseded-within-14d", within(14)},
	{"superseded-within-7d", within(7)},
}

// within keeps the latest version and every version a newer one replaced less than days ago.
func within(days int) func(h []store.Fact, now time.Time) []store.Fact {
	window := time.Duration(days) * 24 * time.Hour
	return func(h []store.Fact, now time.Time) []store.Fact {
		var out []store.Fact
		for i, f := range h {
			if i == len(h)-1 || now.Sub(h[i+1].Created) < window {
				out = append(out, f)
			}
		}
		return out
	}
}

func tail(h []store.Fact, n int) []store.Fact {
	if len(h) <= n {
		return h
	}
	return h[len(h)-n:]
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: history575 cases | count DIR...")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "cases":
		cases()
	case "count":
		err = count(os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func count(dirs []string) error {
	for _, dir := range dirs {
		files, err := filepath.Glob(filepath.Join(dir, "*", "*.jsonl"))
		if err != nil {
			return err
		}
		now := time.Now()
		type tally struct{ versions, bytes int }
		got := make([]tally, len(rules))
		retired := 0
		for _, path := range files {
			h, err := store.ReadFactFile(path)
			if err != nil {
				return err
			}
			if len(h) > 0 && h[len(h)-1].Status == store.Retired {
				retired++
			}
			for i, r := range rules {
				kept := r.keep(h, now)
				b, err := store.EncodeFacts(kept)
				if err != nil {
					return err
				}
				got[i].versions += len(kept)
				got[i].bytes += len(b)
			}
		}
		fmt.Printf("%s: %d facts, %d retired\n", dir, len(files), retired)
		for i, r := range rules {
			fmt.Printf("  %-22s %7d versions %10d bytes %5.1f%%\n", r.name, got[i].versions, got[i].bytes,
				100*float64(got[i].bytes)/float64(got[0].bytes))
		}
	}
	return nil
}

// A side is one machine's history of the fact.
type side struct {
	name string
	h    []store.Fact
	keep func([]store.Fact, time.Time) []store.Fact
}

var t0 = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

// later is how long after the shared history the scenario's writes come.
var later time.Duration

func (s *side) last() store.Fact { return s.h[len(s.h)-1] }

func (s *side) put(f store.Fact, at time.Duration) {
	f.Version, f.Created, f.Author = s.last().Version+1, t0.Add(later+at), s.name
	s.h = reread(s.keep(append(append([]store.Fact{}, s.h...), f), f.Created))
}

func (s *side) correct(content string, at time.Duration) {
	f := s.last()
	f.Content, f.Status = content, store.Active
	s.put(f, at)
}

func (s *side) keywords(words []string, at time.Duration) {
	f := s.last()
	f.Keywords = words
	s.put(f, at)
}

func (s *side) queries(q []string, at time.Duration) {
	f := s.last()
	f.Queries = q
	s.put(f, at)
}

func (s *side) retire(at time.Duration) {
	f := s.last()
	f.Status = store.Retired
	s.put(f, at)
}

// merge takes the other side's history at the time of the side's last version.
func (s *side) merge(o *side) {
	s.h = reread(store.MergeHistories(s.h, o.h).Versions)
	s.h = reread(s.keep(s.h, s.last().Created))
}

// compact applies the side's rule at a time of its own, as a periodic command would.
func (s *side) compact(at time.Duration) { s.h = reread(s.keep(s.h, t0.Add(later+at))) }

// unreadable is a fact file the next command cannot read.
type unreadable struct{ err error }

// reread writes a history as its fact file and reads it back, as the next command would: the file is in version
// order, whatever order the slice had. A file that does not read back stops the scenario with unreadable.
func reread(h []store.Fact) []store.Fact {
	b, err := store.EncodeFacts(h)
	if err != nil {
		panic(err)
	}
	out, err := store.ParseFacts(b)
	if err != nil {
		panic(unreadable{err})
	}
	return out
}

// shared is the history both sides start from: added, expanded by a dream, corrected once, expanded again.
func shared() []store.Fact {
	v := store.Fact{ID: "01TEST", Version: 1, Content: "Hermes работает на deepseek-flash", Origin: "x",
		Created: t0, Status: store.Active, Author: "mac"}
	h := []store.Fact{v}
	add := func(f store.Fact, at time.Duration) {
		f.Version, f.Created = h[len(h)-1].Version+1, t0.Add(at)
		h = append(h, f)
	}
	v.Queries = []string{"модель hermes"}
	add(v, time.Minute)
	v.Content = "Hermes работает на модели deepseek-flash"
	add(v, 2*time.Minute)
	v.Queries = []string{"модель hermes", "deepseek"}
	add(v, 3*time.Minute)
	return h
}

type scenario struct {
	name string
	run  func(mac, server *side)
}

const corrected = "Hermes работает на deepseek-pro, fallback Codex"

var scenarios = []scenario{
	{"retired first, corrected later", func(mac, server *side) {
		server.retire(time.Hour)
		mac.correct(corrected, 2*time.Hour)
	}},
	{"corrected first, retired later", func(mac, server *side) {
		mac.correct(corrected, time.Hour)
		server.retire(2 * time.Hour)
	}},
	{"keywords changed, retired later", func(mac, server *side) {
		mac.keywords([]string{"hermes", "flash"}, time.Hour)
		server.retire(2 * time.Hour)
	}},
	{"queries changed, retired later", func(mac, server *side) {
		mac.queries([]string{"hermes", "модель"}, time.Hour)
		server.retire(2 * time.Hour)
	}},
	{"retired after merging the correction", func(mac, server *side) {
		mac.correct(corrected, time.Hour)
		server.merge(mac)
		server.retire(2 * time.Hour)
	}},
	{"retired after merging, the other side stale", func(mac, server *side) {
		server.correct("Hermes работает на deepseek-lite", time.Hour)
		mac.correct(corrected, 90*time.Minute)
		server.merge(mac)
		server.retire(3 * time.Hour)
	}},
	{"corrected and retired on one side, keywords changed on the other", func(mac, server *side) {
		server.correct(corrected, time.Hour)
		server.retire(time.Hour + time.Second)
		mac.keywords([]string{"hermes", "flash"}, 30*time.Minute)
	}},
	{"corrected and retired on one side, queries changed on the other", func(mac, server *side) {
		server.correct(corrected, time.Hour)
		server.retire(time.Hour + time.Second)
		mac.queries([]string{"hermes", "модель"}, 30*time.Minute)
	}},
	{"corrected first, retired later, the corrected side merges the result", func(mac, server *side) {
		mac.correct(corrected, time.Hour)
		server.retire(2 * time.Hour)
		server.merge(mac)
	}},
	{"a stale side merges again (#540)", func(mac, server *side) {
		mac.correct("Hermes работает на deepseek-pro", time.Hour)
		server.correct(corrected, 2*time.Hour)
		mac.merge(server)
	}},
	{"retired and reopened on one side, corrected on the other", func(mac, server *side) {
		server.retire(time.Hour)
		f := server.last()
		f.Status = store.Active
		server.put(f, 90*time.Minute)
		mac.correct(corrected, 2*time.Hour)
	}},
	{"only one side changed", func(mac, server *side) {
		mac.correct(corrected, time.Hour)
	}},
	// The #549 case again, with both sides compacting 40 days later before they merge: past the window.
	{"#549 merged 40 days after the writes", func(mac, server *side) {
		server.correct(corrected, time.Hour)
		server.retire(time.Hour + time.Second)
		mac.queries([]string{"hermes", "модель"}, 30*time.Minute)
		mac.compact(40 * 24 * time.Hour)
		server.compact(40 * 24 * time.Hour)
	}},
}

// outcome merges both ways and returns the current version and the merged history of each direction.
func outcome(sc scenario, macKeep, serverKeep func([]store.Fact, time.Time) []store.Fact) (a1, b1 string, na, nb int) {
	defer func() {
		if r := recover(); r != nil {
			u, ok := r.(unreadable)
			if !ok {
				panic(r)
			}
			a1, b1 = "UNREADABLE "+u.err.Error(), "UNREADABLE"
		}
	}()
	start := t0.Add(later)
	mac := &side{name: "mac", h: macKeep(shared(), start), keep: macKeep}
	server := &side{name: "server", h: serverKeep(shared(), start), keep: serverKeep}
	sc.run(mac, server)
	a := reread(store.MergeHistories(mac.h, server.h).Versions)
	b := reread(store.MergeHistories(server.h, mac.h).Versions)
	cur := func(h []store.Fact) string {
		f := h[len(h)-1]
		return fmt.Sprintf("v%d %s %s", f.Version, f.Status, f.Content)
	}
	return cur(a), cur(b), len(a), len(b)
}

func cases() {
	for _, d := range []int{0, 10} {
		later = time.Duration(d) * 24 * time.Hour
		fmt.Printf("=== the writes come %d days after the shared history\n\n", d)
		casesOnce()
		fmt.Println()
	}
}

func casesOnce() {
	full := rules[0].keep
	fmt.Println("each side compacts after every write and merge; current version after merging both ways")
	for _, r := range rules[1:] {
		fmt.Printf("\n%s\n", r.name)
		for _, sc := range scenarios {
			want, _, _, _ := outcome(sc, full, full)
			a, b, _, _ := outcome(sc, r.keep, r.keep)
			mark := verdict(a, b, want)
			fmt.Printf("  %-13s %s\n", mark, sc.name)
			if mark != "same" {
				fmt.Printf("                full:      %s\n                compacted: %s | %s\n", want, a, b)
			}
		}
	}
	fmt.Println("\nonly the Mac compacts (the server runs a memcue that does not): versions after the merge, Mac / server")
	for _, r := range rules[1:] {
		fmt.Printf("\n%s\n", r.name)
		for _, sc := range scenarios {
			want, _, nf, _ := outcome(sc, full, full)
			a, b, na, nb := outcome(sc, r.keep, full)
			mark := verdict(a, b, want)
			fmt.Printf("  %-13s %2d/%2d of %2d  %s\n", mark, na, nb, nf, sc.name)
			if mark != "same" {
				fmt.Printf("                full:      %s\n                compacted: %s | %s\n", want, a, b)
			}
		}
	}
}

// verdict compares the current versions of the two merge directions with the one full histories give.
func verdict(a, b, want string) string {
	switch {
	case strings.HasPrefix(a, "UNREADABLE"):
		return "UNREADABLE"
	case a != b:
		return "SIDES DIFFER"
	case a == want:
		return "same"
	case unnumbered(a) == unnumbered(want):
		return "NUMBER"
	}
	return "CHANGED"
}

// unnumbered drops the version number of a current version as outcome prints it.
func unnumbered(cur string) string {
	if _, rest, ok := strings.Cut(cur, " "); ok {
		return rest
	}
	return cur
}
