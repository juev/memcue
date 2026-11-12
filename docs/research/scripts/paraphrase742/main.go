//go:build research

// Command paraphrase742 is the harness of memcue #742: FTS5 against vectors on paraphrased queries.
//
//	paraphrase742 overlap FIXTURE IN.jsonl OUT.jsonl
//	paraphrase742 run FIXTURE QUERIES.json VECTORS.json.gz PASSES OUT.json.gz
//
// overlap counts the content terms a query shares with its gold fact by memcue's own tokenization: the query's
// terms (text.Terms, as recall.Search builds them) that cover a stem of the fact (text.Stems, as the index stores
// them), by the same prefix rule FTS5 matches with. A negative has no gold fact; it gets the largest count over the
// facts of its group.
//
// run loads the fixture into a fresh local base and ranks every query with each arm: the native BM25 list
// (Store.SearchFiltered over the query's terms), the production `memcue search` default (recall.Search, five facts,
// floor 0.6), exact cosine over every eligible fact for each vector model, and RRF60 of the BM25 list and each
// vector list. Query vectors come from the file, so search time excludes encoding. Built only with -tags research.
package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"time"

	"github.com/juev/memcue/internal/recall"
	"github.com/juev/memcue/internal/store"
	"github.com/juev/memcue/internal/text"
)

type fixture struct {
	Facts []struct {
		ID      string `json:"id"`
		Group   string `json:"group"`
		Lang    string `json:"lang"`
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"facts"`
}

func readJSON(path string, v any) {
	f, err := os.Open(path)
	check(err)
	defer func() { _ = f.Close() }()
	var r interface{ Read([]byte) (int, error) } = f
	if filepath.Ext(path) == ".gz" {
		zr, err := gzip.NewReader(f)
		check(err)
		r = zr
	}
	check(json.NewDecoder(r).Decode(v))
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "paraphrase742:", err)
		os.Exit(1)
	}
}

func main() {
	if len(os.Args) < 2 {
		check(fmt.Errorf("usage: paraphrase742 overlap|run ..."))
	}
	// The index and the queries must use the default number terms, whatever the shell exports.
	check(os.Unsetenv("MEMCUE_NUMBER_TERMS"))
	switch os.Args[1] {
	case "overlap":
		overlap(os.Args[2], os.Args[3], os.Args[4])
	case "run":
		var passes int
		_, err := fmt.Sscan(os.Args[5], &passes)
		check(err)
		run(os.Args[2], os.Args[3], os.Args[4], passes, os.Args[6])
	default:
		check(fmt.Errorf("unknown command %q", os.Args[1]))
	}
}

// shared is the query's terms that cover a stem of content.
func shared(query, content string) (terms, sharedTerms []string) {
	terms = text.Terms(query)
	stems := text.Stems(content)
	sharedTerms = []string{}
	for _, t := range terms {
		if slices.ContainsFunc(stems, func(st string) bool { return text.Covers(t, st) }) {
			sharedTerms = append(sharedTerms, t)
		}
	}
	return terms, sharedTerms
}

func overlap(fixturePath, inPath, outPath string) {
	var fx fixture
	readJSON(fixturePath, &fx)
	content := map[string]string{}
	group := map[string][]string{}
	for _, f := range fx.Facts {
		content[f.ID] = f.Content
		group[f.Group] = append(group[f.Group], f.ID)
	}
	in, err := os.ReadFile(inPath)
	check(err)
	out, err := os.Create(outPath)
	check(err)
	defer func() { check(out.Close()) }()
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	dec := json.NewDecoder(bytes.NewReader(in))
	for dec.More() {
		var q map[string]any
		check(dec.Decode(&q))
		qtext := q["text"].(string)
		var ids []string
		if gold, _ := q["gold"].([]any); len(gold) > 0 {
			for _, g := range gold {
				ids = append(ids, g.(string))
			}
		} else {
			ids = group[q["group"].(string)]
		}
		best := []string{}
		var terms []string
		against := ""
		for _, id := range ids {
			t, sh := shared(qtext, content[id])
			terms = t
			if against == "" || len(sh) > len(best) {
				best, against = sh, id
			}
		}
		if terms == nil {
			terms = []string{}
		}
		q["query_terms"] = terms
		q["shared_terms"] = best
		q["overlap"] = len(best)
		q["overlap_against"] = against
		check(enc.Encode(q))
	}
}

type query struct {
	ID      string   `json:"id"`
	Text    string   `json:"text"`
	Stratum string   `json:"stratum"`
	Gold    []string `json:"gold"`
}

type vectors struct {
	Models map[string]struct {
		Facts   map[string][]float32 `json:"facts"`
		Queries map[string][]float32 `json:"queries"`
	} `json:"models"`
}

type ranked struct {
	ID    string  `json:"id"`
	Score float64 `json:"score"`
}

// order sorts by score, better first, then by ID: the tie order of every arm. BM25 of FTS5 is negative, the best the
// lowest, so it is ranked by its negation.
func order(r []ranked) {
	sort.SliceStable(r, func(i, j int) bool {
		if r[i].Score != r[j].Score {
			return r[i].Score > r[j].Score
		}
		return r[i].ID < r[j].ID
	})
}

const listDepth = 100

func lexical(s *store.Store, terms []string, f store.Filter) []ranked {
	hits, err := s.SearchFiltered(terms, listDepth, f)
	check(err)
	out := make([]ranked, len(hits))
	for i, h := range hits {
		out[i] = ranked{h.ID, -h.BM25}
	}
	order(out)
	return out
}

func production(s *store.Store, q string) []ranked {
	hits, err := recall.Search(s, recall.Query{Text: q, N: 5, MinScore: recall.MinScore, In: []string{"global"}})
	check(err)
	out := make([]ranked, len(hits))
	for i, h := range hits {
		out[i] = ranked{h.ID, -h.BM25}
	}
	return out
}

func cosine(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	return dot / math.Sqrt(na*nb)
}

// vector is exact cosine over every eligible fact, positive scores only, as in #682.
func vector(eligible []string, facts map[string][]float32, qv []float32) []ranked {
	out := make([]ranked, 0, len(eligible))
	for _, id := range eligible {
		if c := cosine(facts[id], qv); c > 0 {
			out = append(out, ranked{id, c})
		}
	}
	order(out)
	if len(out) > listDepth {
		out = out[:listDepth]
	}
	return out
}

// rrf is equal-weight reciprocal rank fusion, 1/(60+rank), over the lists.
func rrf(lists ...[]ranked) []ranked {
	score := map[string]float64{}
	for _, l := range lists {
		for i, r := range l {
			score[r.ID] += 1 / float64(60+i+1)
		}
	}
	out := make([]ranked, 0, len(score))
	for id, s := range score {
		out = append(out, ranked{id, s})
	}
	order(out)
	return out
}

type armResult struct {
	Top      []ranked `json:"top"`       // the first five, what the arm returns
	Listed   int      `json:"listed"`    // how many facts the arm's whole list holds
	GoldRank int      `json:"gold_rank"` // 1-based place of the first gold fact in the whole list, 0 when absent
}

type queryResult struct {
	ID    string               `json:"id"`
	Terms []string             `json:"terms"`
	Arms  map[string]armResult `json:"arms"`
	// Times holds the search-only wall clock of every pass per arm, in milliseconds; pass 0 is the first.
	Times map[string][]float64 `json:"times_ms"`
}

func result(list []ranked, gold []string) armResult {
	r := armResult{Top: list[:min(5, len(list))], Listed: len(list)}
	for i, x := range list {
		if slices.Contains(gold, x.ID) {
			r.GoldRank = i + 1
			break
		}
	}
	if r.Top == nil {
		r.Top = []ranked{}
	}
	return r
}

func run(fixturePath, queriesPath, vectorsPath string, passes int, outPath string) {
	var fx fixture
	readJSON(fixturePath, &fx)
	var qs struct {
		Queries []query `json:"queries"`
	}
	readJSON(queriesPath, &qs)
	var vx vectors
	readJSON(vectorsPath, &vx)

	dir, err := os.MkdirTemp("", "paraphrase742-")
	check(err)
	defer func() { _ = os.RemoveAll(dir) }()
	s, err := store.Open(filepath.Join(dir, "facts.db"))
	check(err)
	defer func() { _ = s.Close() }()
	created := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	var facts []store.Fact
	var eligible []string
	for _, f := range fx.Facts {
		facts = append(facts, store.Fact{ID: f.ID, Version: 1, Content: f.Content, Created: created,
			Status: store.Active, Meta: store.Meta{Scope: "global"}})
		eligible = append(eligible, f.ID)
	}
	check(store.ResearchPut(s, facts))
	_, total, err := s.DocFreq([]string{"x"})
	check(err)
	if total != len(eligible) {
		check(fmt.Errorf("base holds %d active facts, the fixture %d", total, len(eligible)))
	}
	filter, err := recall.Query{In: []string{"global"}}.Filter()
	check(err)

	models := make([]string, 0, len(vx.Models))
	for m := range vx.Models {
		models = append(models, m)
	}
	slices.Sort(models)
	for _, m := range models {
		if len(vx.Models[m].Facts) != len(eligible) {
			check(fmt.Errorf("model %s has %d fact vectors, want %d", m, len(vx.Models[m].Facts), len(eligible)))
		}
	}
	arms := []string{"bm25", "production"}
	for _, m := range models {
		arms = append(arms, "vector-"+m, "hybrid-"+m)
	}

	var out []queryResult
	for qi, q := range qs.Queries {
		terms := text.Terms(q.Text)
		lists := map[string][]ranked{}
		qr := queryResult{ID: q.ID, Terms: terms, Arms: map[string]armResult{}, Times: map[string][]float64{}}
		if qr.Terms == nil {
			qr.Terms = []string{}
		}
		for p := 0; p < passes; p++ {
			for k := range arms {
				arm := arms[(k+qi+p)%len(arms)]
				var list []ranked
				start := time.Now()
				switch {
				case arm == "bm25":
					list = lexical(s, text.Terms(q.Text), filter)
				case arm == "production":
					list = production(s, q.Text)
				case len(arm) > 7 && arm[:7] == "vector-":
					m := vx.Models[arm[7:]]
					list = vector(eligible, m.Facts, m.Queries[q.ID])
				default:
					m := vx.Models[arm[7:]]
					list = rrf(lexical(s, text.Terms(q.Text), filter), vector(eligible, m.Facts, m.Queries[q.ID]))
				}
				qr.Times[arm] = append(qr.Times[arm], float64(time.Since(start).Nanoseconds())/1e6)
				if prev, ok := lists[arm]; ok && !slices.Equal(prev, list) {
					check(fmt.Errorf("query %s arm %s: pass %d ranked differently", q.ID, arm, p))
				}
				lists[arm] = list
			}
		}
		for _, arm := range arms {
			qr.Arms[arm] = result(lists[arm], q.Gold)
		}
		out = append(out, qr)
	}
	f, err := os.Create(outPath)
	check(err)
	zw := gzip.NewWriter(f)
	enc := json.NewEncoder(zw)
	enc.SetEscapeHTML(false)
	check(enc.Encode(map[string]any{"arms": arms, "passes": passes, "eligible": len(eligible), "queries": out}))
	check(zw.Close())
	check(f.Close())
}
