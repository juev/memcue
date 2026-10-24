//go:build research

// Command scope replays recall on a copy of a base with the place of a fact taken into account (#345): for every
// prompt it runs recall once with no candidate limit and builds, from that trace, what each variant of the place would
// serve. A variant acts only on facts scoped repo: or dir: that do not hold in the session's project; global, host:,
// service: and empty scopes are never filtered and never ranked down. A session with no project (no cwd, a directory
// memcue gives no key, or the Claude config directory) gets the baseline in every variant. One JSON line per prompt
// goes to stdout, the totals to stderr, with how often recall itself, since #345 shipped the gate, serves what the
// gate0.25 and filter variants do. Experiment only, not part of memcue.
//
// usage: go run -tags research ./docs/research/scripts/scope FACTS.db eval.json CLAUDE_DIR > scope.jsonl
//
// eval.json is {"prompts":[{"id","text","cwd"}]}, as prompt_pool.py writes it.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/juev/memcue/internal/recall"
	"github.com/juev/memcue/internal/store"
	"github.com/juev/memcue/internal/tier"
)

// unlimited stands for "no limit": larger than any base this runs on.
const unlimited = 1 << 30

// gates are the coverage a fact of another project must reach in the gate and tier variants.
var gates = []float64{0.25, 0.35, 0.5}

type row struct {
	ID      string              `json:"id"`
	Text    string              `json:"text"`
	CWD     string              `json:"cwd"`
	Keys    []string            `json:"keys"`
	Project bool                `json:"project"`
	Anchors []string            `json:"anchors,omitempty"` // scopes the prompt names
	Shipped []string            `json:"shipped"`           // what recall serves with recall.Default
	Filter  []string            `json:"filter"`            // what recall serves with FilterForeign
	Picks   map[string][]string `json:"picks"`             // variant -> served facts
	Foreign int                 `json:"foreign"`           // passing facts in the shipped pool that do not hold here
}

type candidate struct {
	id          string
	passed      bool
	coverage    float64
	scope       string
	provisional bool
}

func main() {
	s, err := store.Open(os.Args[1])
	if err != nil {
		panic(err)
	}
	defer func() { _ = s.Close() }()
	b, err := os.ReadFile(os.Args[2])
	if err != nil {
		panic(err)
	}
	claudeDir := os.Args[3]
	var in struct {
		Prompts []struct {
			ID   string `json:"id"`
			Text string `json:"text"`
			CWD  string `json:"cwd"`
		} `json:"prompts"`
	}
	if err := json.Unmarshal(b, &in); err != nil {
		panic(err)
	}
	facts, err := s.Current()
	if err != nil {
		panic(err)
	}
	byID := map[string]store.Fact{}
	names := map[string][]string{} // last segment of a repository key -> the repo: scopes it names
	for _, f := range facts {
		byID[f.ID] = f
		sc := strings.ToLower(f.Scope)
		if key, ok := strings.CutPrefix(sc, "repo:"); ok {
			name := strings.TrimSuffix(key[strings.LastIndex(key, "/")+1:], ".git")
			if len(name) >= 3 && !slices.Contains(names[name], sc) {
				names[name] = append(names[name], sc)
			}
		}
	}
	enc := json.NewEncoder(os.Stdout)
	var prompts, withProject, differ, gate, filter int
	for _, p := range in.Prompts {
		r := run(s, byID, names, claudeDir, p.ID, p.Text, p.CWD)
		prompts++
		if r.Project {
			withProject++
		}
		if slices.Equal(r.Shipped, r.Picks["gate0.25"]) {
			gate++
		}
		if slices.Equal(r.Filter, r.Picks["filter"]) {
			filter++
		}
		for v, ids := range r.Picks {
			if v != "base" && !slices.Equal(ids, r.Picks["base"]) {
				differ++
				break
			}
		}
		if err := enc.Encode(r); err != nil {
			panic(err)
		}
	}
	fmt.Fprintf(os.Stderr, "prompts %d, with a project %d, some variant differs from base in %d; recall.Default "+
		"serves what gate0.25 does in %d, FilterForeign what filter does in %d\n", prompts, withProject, differ, gate,
		filter)
}

var reToken = regexp.MustCompile(`[\p{L}\p{N}_.-]+`)

func run(s *store.Store, byID map[string]store.Fact, names map[string][]string, claudeDir, id, prompt,
	cwd string) row {
	r := row{ID: id, Text: prompt, CWD: cwd, Picks: map[string][]string{}}
	if cwd != "" {
		r.Keys = tier.Keys(cwd, claudeDir)
	}
	r.Project = len(r.Keys) > 0 && !(len(r.Keys) == 1 && r.Keys[0] == tier.Global)
	for _, t := range reToken.FindAllString(strings.ToLower(prompt), -1) {
		for _, sc := range names[strings.Trim(t, ".-")] {
			if !slices.Contains(r.Anchors, sc) {
				r.Anchors = append(r.Anchors, sc)
			}
		}
	}
	sort.Strings(r.Anchors)

	p := recall.Default
	p.Today = "2026-10-23" // the day the base was archived, so expiries read as they did then
	shipped, err := recall.Recall(s, prompt, r.Keys, p)
	if err != nil {
		panic(err)
	}
	for _, h := range shipped {
		r.Shipped = append(r.Shipped, h.ID)
	}
	pf := p
	pf.FilterForeign = true
	filtered, err := recall.Recall(s, prompt, r.Keys, pf)
	if err != nil {
		panic(err)
	}
	for _, h := range filtered {
		r.Filter = append(r.Filter, h.ID)
	}
	all := p
	all.Candidates = unlimited
	all.ForeignCoverage = 0 // the variants are built from the gates as they were before #345
	tr := &recall.Trace{}
	all.Trace = tr
	if _, err := recall.Recall(s, prompt, r.Keys, all); err != nil {
		panic(err)
	}
	var pool []candidate // the whole BM25 order FTS5 returns
	for _, st := range tr.Lexical {
		f := byID[st.ID]
		pool = append(pool, candidate{id: st.ID, passed: st.Verdict == recall.VerdictPassed, coverage: st.Coverage,
			scope: strings.ToLower(f.Scope), provisional: f.Review == store.Provisional})
	}
	holds := func(c candidate, anchors bool) bool {
		if !r.Project {
			return true
		}
		if key, ok := strings.CutPrefix(c.scope, "repo:"); ok {
			return slices.Contains(r.Keys, key) || anchors && slices.Contains(r.Anchors, c.scope)
		}
		if dir, ok := strings.CutPrefix(c.scope, "dir:"); ok {
			abs, err := filepath.Abs(cwd)
			return err == nil && (abs == dir || strings.HasPrefix(abs, strings.TrimSuffix(dir, "/")+"/"))
		}
		return true // global, host:, service:, empty: a place that holds from any repository
	}
	head := pool[:min(len(pool), p.Candidates)]
	for _, c := range head {
		if c.passed && !holds(c, false) {
			r.Foreign++
		}
	}
	r.Picks["base"] = serve(head, p.MaxFacts, func(candidate) int { return 0 })
	for _, anchors := range []bool{false, true} {
		suffix := ""
		if anchors {
			suffix = "+a"
		}
		var kept []candidate
		for _, c := range pool {
			if holds(c, anchors) {
				kept = append(kept, c)
			}
		}
		r.Picks["filter"+suffix] = serve(kept[:min(len(kept), p.Candidates)], p.MaxFacts, func(candidate) int { return 0 })
		for _, g := range gates {
			gated := func(c candidate) int {
				if holds(c, anchors) {
					return 0
				}
				if c.coverage < g {
					return -1
				}
				return 0
			}
			r.Picks[fmt.Sprintf("gate%.2f%s", g, suffix)] = serve(head, p.MaxFacts, gated)
		}
		for _, g := range append([]float64{0}, gates...) {
			tiered := func(c candidate) int {
				if holds(c, anchors) {
					return 0
				}
				if c.coverage < g {
					return -1
				}
				return 1
			}
			r.Picks[fmt.Sprintf("tier%.2f%s", g, suffix)] = serve(head, p.MaxFacts, tiered)
		}
	}
	return r
}

// serve picks what recall would serve from pool, in BM25 order, approved before provisional: tier orders the facts
// first (lower first) and a negative tier turns a fact away.
func serve(pool []candidate, maxFacts int, tierOf func(candidate) int) []string {
	var hits []candidate
	for _, c := range pool {
		if c.passed && tierOf(c) >= 0 {
			hits = append(hits, c)
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].provisional != hits[j].provisional {
			return !hits[i].provisional
		}
		return tierOf(hits[i]) < tierOf(hits[j])
	})
	out := []string{}
	for i := 0; i < len(hits) && i < maxFacts; i++ {
		out = append(out, hits[i].id)
	}
	return out
}
