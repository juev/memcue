//go:build research

// Command vet_neighbours asks the duplicate judge about every candidate of a base twice: against the whole approved
// base, the way vet asked before #240, and against the candidate's nearest approved facts in BM25 order, for each
// neighbour count given. One JSON line per candidate, shape and run: the strongest "same claim", the fact it names,
// the neighbours asked about, and the input tokens. Experiment only, not part of memcue.
//
//	MEMCUE_JUDGE_URL=… MEMCUE_JUDGE_MODEL=… MEMCUE_JUDGE_KEY=… \
//	    go run -tags research ./docs/research/scripts/vet_neighbours -facts FACTS.db -n 20,30,50 -runs 2 [-whole=false] > out.jsonl
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/juev/memcue/internal/extract"
	"github.com/juev/memcue/internal/recall"
	"github.com/juev/memcue/internal/store"
)

type row struct {
	ID        string   `json:"id"`
	Shape     string   `json:"shape"` // whole, or n<count>
	Run       int      `json:"run"`
	P         float64  `json:"p"`
	Hit       string   `json:"hit,omitempty"`
	Asked     int      `json:"asked"` // approved facts in the request
	Input     int      `json:"input"`
	Error     string   `json:"error,omitempty"`
	Neighbour []string `json:"neighbours,omitempty"`
}

func main() {
	facts := flag.String("facts", "", "fact base")
	ns := flag.String("n", "20,30,50", "neighbour counts")
	runs := flag.Int("runs", 1, "runs of each shape")
	withWhole := flag.Bool("whole", true, "ask about the whole approved base too")
	flag.Parse()
	extract.JevJudge.URL = os.Getenv("MEMCUE_JUDGE_URL")
	extract.JevJudge.Model = os.Getenv("MEMCUE_JUDGE_MODEL")
	extract.JevJudge.Key = os.Getenv("MEMCUE_JUDGE_KEY")
	extract.JevJudge.MaxFacts = 400
	extract.JevJudge.DupTimeout = 30 * time.Second

	s, err := store.Open(*facts)
	if err != nil {
		panic(err)
	}
	defer func() { _ = s.Close() }()
	if err := s.Rebuild(); err != nil {
		panic(err)
	}
	approved, err := s.Active()
	if err != nil {
		panic(err)
	}
	cands, err := s.Candidates()
	if err != nil {
		panic(err)
	}
	fmt.Fprintln(os.Stderr, "approved", len(approved), "candidates", len(cands))
	isApproved := map[string]bool{}
	whole := make([]extract.JudgeFact, 0, len(approved))
	for _, f := range approved {
		isApproved[f.ID] = true
		whole = append(whole, extract.JudgeFact{ID: f.ID, Text: f.Content})
	}
	var counts []int
	for _, v := range strings.Split(*ns, ",") {
		n, err := strconv.Atoi(v)
		if err != nil {
			panic(err)
		}
		counts = append(counts, n)
	}

	type job struct {
		f     store.Fact
		shape string
		run   int
		base  []extract.JudgeFact
	}
	var jobs []job
	for _, f := range cands {
		for _, n := range counts {
			p := recall.Params{Candidates: max(80, 2*n), MinTerms: 2, MaxPromptTerms: 24,
				Seen: map[string]bool{f.ID: true}}
			hits, err := recall.Nearest(s, f.Content+" "+strings.Join(f.Keywords, " "), p)
			if err != nil {
				panic(err)
			}
			var base []extract.JudgeFact
			for _, h := range hits {
				if isApproved[h.ID] && len(base) < n {
					base = append(base, extract.JudgeFact{ID: h.ID, Text: h.Content})
				}
			}
			for r := 1; r <= *runs; r++ {
				jobs = append(jobs, job{f, "n" + strconv.Itoa(n), r, base})
			}
		}
		for r := 1; *withWhole && r <= *runs; r++ {
			jobs = append(jobs, job{f, "whole", r, whole})
		}
	}

	var mu sync.Mutex
	enc := json.NewEncoder(os.Stdout)
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Add(1)
		sem <- struct{}{}
		go func(j job) {
			defer func() { <-sem; wg.Done() }()
			out := row{ID: j.f.ID, Shape: j.shape, Run: j.run, Asked: len(j.base)}
			if j.shape != "whole" {
				for _, b := range j.base {
					out.Neighbour = append(out.Neighbour, b.ID)
				}
			}
			v, err := extract.JudgeDuplicate(context.Background(), extract.JudgeFact{ID: j.f.ID, Text: j.f.Content}, j.base)
			if err != nil {
				out.Error = err.Error()
			}
			out.P, out.Hit, out.Input = v.P, v.HitID, v.Tokens.Input
			mu.Lock()
			defer mu.Unlock()
			if err := enc.Encode(out); err != nil {
				panic(err)
			}
		}(j)
	}
	wg.Wait()
}
