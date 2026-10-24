package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/juev/memcue/internal/recall"
	"github.com/juev/memcue/internal/store"
)

// evalSet is prepared outside the repository: it holds work prompts and private facts.
type evalSet struct {
	Questions []struct {
		ID       string `json:"id"`
		Category string `json:"category"`
		Tier     string `json:"tier"`
		Text     string `json:"text"`
		Expect   string `json:"expect"` // the note ("<tier>/<file>.md") a recalled fact must come from
	} `json:"questions"`
	Prompts []struct {
		ID          string `json:"id"`
		Tier        string `json:"tier"`
		Text        string `json:"text"`
		NeedsMemory bool   `json:"needs_memory"`
	} `json:"prompts"`
	Labels []struct {
		Prompt string `json:"prompt"`
		Fact   string `json:"fact"` // fact id
		Label  string `json:"label"`
	} `json:"labels"`
}

// evalPair is one recalled pair the set has no label for.
type evalPair struct {
	Prompt  string `json:"prompt"`
	Fact    string `json:"fact"`
	Content string `json:"content"`
	Origin  string `json:"origin"`
}

// runEval is the evaluation command: measure recall over a prepared set, optionally writing the unlabelled pairs.
func runEval(args []string, out io.Writer) error {
	fl := flag.NewFlagSet("eval", flag.ContinueOnError)
	set := fl.String("set", "", "eval.json")
	unlabeled := fl.String("unlabeled", "", "write the recalled (prompt, fact) pairs that have no label here")
	p := recall.Default
	fl.IntVar(&p.MinTerms, "min-terms", p.MinTerms, "")
	fl.IntVar(&p.MinMatched, "min-matched", p.MinMatched, "")
	fl.Float64Var(&p.MinCoverage, "min-coverage", p.MinCoverage, "")
	fl.IntVar(&p.MaxPromptTerms, "max-prompt-terms", p.MaxPromptTerms, "")
	fl.IntVar(&p.MaxFacts, "max-facts", p.MaxFacts, "")
	fl.Float64Var(&p.ForeignCoverage, "foreign-coverage", p.ForeignCoverage, "0 measures the recall before #345")
	if err := fl.Parse(args); err != nil {
		return err
	}
	b, err := os.ReadFile(*set)
	if err != nil {
		return err
	}
	var ev evalSet
	if err := json.Unmarshal(b, &ev); err != nil {
		return err
	}
	return withStore(func(s *store.Store) error {
		r := ranker()
		run := func(text, tier string) ([]recall.Hit, error) {
			if r := []rune(text); len(r) > maxPrompt {
				text = string(r[:maxPrompt])
			}
			var project []string
			if tier != "" {
				project = []string{tier}
			}
			if r != nil {
				return recall.RecallJudge(s, text, project, p, r)
			}
			return recall.Recall(s, text, project, p)
		}

		if len(ev.Questions) > 0 {
			hit, total := map[string]int{}, map[string]int{}
			var misses []string
			for _, q := range ev.Questions {
				hs, err := run(q.Text, q.Tier)
				if err != nil {
					return err
				}
				total[q.Category]++
				found := false
				for _, h := range hs {
					found = found || strings.Contains(h.Origin, "("+q.Expect+")")
				}
				if found {
					hit[q.Category]++
				} else {
					misses = append(misses, q.ID)
				}
			}
			if _, err := fmt.Fprintf(out, "recall: index %d/%d, file %d/%d, cross %d/%d, all %d/%d; missed %v\n",
				hit["index"], total["index"], hit["file"], total["file"], hit["cross"], total["cross"],
				hit["index"]+hit["file"]+hit["cross"], len(ev.Questions), misses); err != nil {
				return err
			}
		}

		label := map[[2]string]string{}
		for _, l := range ev.Labels {
			label[[2]string{l.Prompt, l.Fact}] = l.Label
		}
		counts := map[string]int{}
		withUseful, withAny, noMem := 0, 0, 0
		var unl []evalPair
		for _, pr := range ev.Prompts {
			hs, err := run(pr.Text, pr.Tier)
			if err != nil {
				return err
			}
			useful := false
			for _, h := range hs {
				l, ok := label[[2]string{pr.ID, h.ID}]
				if !ok {
					l = "UNLABELED"
					unl = append(unl, evalPair{pr.ID, h.ID, h.Content, h.Origin})
				}
				counts[l]++
				useful = useful || l == "USEFUL"
			}
			if len(hs) > 0 {
				withAny++
			}
			if useful {
				withUseful++
			}
			if !pr.NeedsMemory {
				noMem += len(hs)
			}
		}
		n := counts["USEFUL"] + counts["RELATED"] + counts["NOISE"] + counts["UNLABELED"]
		if _, err := fmt.Fprintf(out, "noise: %d prompts, %d with injection, %d with a useful fact; facts %d: useful %d, related %d, noise %d, unlabeled %d; facts on prompts not needing memory %d\n",
			len(ev.Prompts), withAny, withUseful, n, counts["USEFUL"], counts["RELATED"], counts["NOISE"],
			counts["UNLABELED"], noMem); err != nil {
			return err
		}
		if *unlabeled == "" {
			return nil
		}
		sort.Slice(unl, func(i, j int) bool { return unl[i].Prompt+unl[i].Fact < unl[j].Prompt+unl[j].Fact })
		return writeEvalPairs(*unlabeled, unl)
	})
}

// writeEvalPairs writes the unlabelled pairs with mode 0600: the file holds user prompts and private fact text.
func writeEvalPairs(path string, pairs []evalPair) error {
	b, err := json.MarshalIndent(pairs, "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}
