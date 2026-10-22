package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/juev/memcue/internal/extract"
	"github.com/juev/memcue/internal/store"
	"github.com/juev/memcue/internal/tier"
)

// labelFn is the model step, replaced in tests.
var labelFn = extract.LabelFacts

// labelBatch is how many facts one model call labels: a label is three short fields, so a call holds more facts than
// a review does.
const labelBatch = 40

// reNote finds the note a migrated fact came from at the end of its origin: "_global 2026-10-19
// (_global/feedback_dns_dual_stack.md)".
var reNote = regexp.MustCompile(`\(([^/()\s]+)/([^/()\s]+\.md)\)\s*$`)

// noteKinds maps the prefix of a note's file name to the kind of every fact taken from it. Only the notes whose facts
// stay out of the card are settled this way: a feedback or user note splits into rules and plain knowledge about a
// tool, and a project note mixes state, decisions and events, so their facts go to the model.
var noteKinds = map[string]string{"negative": "pitfall", "reference": "environment"}

// noteLabel returns what the migrated note of a fact says about it: the scope from the note's directory, the kind from
// its name where the name settles it, and the note's type (the prefix of its name) as a hint for the model. A fact
// with no note in its origin gets nothing.
func noteLabel(origin string) (kind, scope, note string) {
	m := reNote.FindStringSubmatch(origin)
	if m == nil {
		return "", "", ""
	}
	scope = "repo:" + m[1]
	if m[1] == tier.Global {
		scope = "global"
	}
	note, _, _ = strings.Cut(m[2], "_")
	return noteKinds[note], scope, note
}

// labelValues are the fields a run fills.
type labelValues struct {
	Kind    string `json:"kind"`
	Scope   string `json:"scope"`
	Expires string `json:"expires,omitempty"`
}

// labelRecord is one line of a run's file: what a fact had and what it got, so the run can be reverted.
type labelRecord struct {
	Time   string      `json:"time"`
	ID     string      `json:"id"`
	From   int         `json:"from"` // the version labelled
	To     int         `json:"to"`   // the version the label wrote; 0 when it failed
	Before labelValues `json:"before"`
	After  labelValues `json:"after"`
	By     string      `json:"by"` // note, model, or note+model
	Error  string      `json:"error,omitempty"`
}

// labelInput is one fact as the model sees it; the kind and scope already known are given so the answer agrees.
type labelInput struct {
	ID     string `json:"id"`
	Text   string `json:"text"`
	Origin string `json:"origin,omitempty"`
	Kind   string `json:"kind,omitempty"`
	Scope  string `json:"scope,omitempty"`
	Note   string `json:"note,omitempty"` // the type of the migrated note: feedback, user, project, …
}

// pendingLabel is a fact of the run with the values it is going to get.
type pendingLabel struct {
	fact store.Fact
	to   labelValues
	by   []string
	note string
}

// runLabel fills the kind and scope of approved facts written before those fields existed: memcue label [-dry-run]
// [-no-model] [-limit N] | memcue label -revert FILE.
func runLabel(args []string, out io.Writer) error {
	fl := flag.NewFlagSet("label", flag.ContinueOnError)
	dry := fl.Bool("dry-run", false, "count what the notes settle and what the model would get; change nothing")
	noModel := fl.Bool("no-model", false, "apply only what the migrated notes settle")
	limit := fl.Int("limit", 0, "label at most this many facts")
	revert := fl.String("revert", "", "restore the values a run's file records")
	if err := fl.Parse(args); err != nil {
		return err
	}
	if *revert != "" {
		return withStore(func(s *store.Store) error { return revertLabels(s, *revert, out) })
	}
	return withStore(func(s *store.Store) error {
		facts, err := s.Active()
		if err != nil {
			return err
		}
		var todo []*pendingLabel
		for _, f := range facts {
			if f.Kind != "" && f.Scope != "" {
				continue
			}
			p := &pendingLabel{fact: f, to: labelValues{Kind: f.Kind, Scope: f.Scope, Expires: f.Expires}}
			kind, scope, note := noteLabel(f.Origin)
			p.note = note
			if p.to.Kind == "" && kind != "" {
				p.to.Kind = kind
			}
			if p.to.Scope == "" && scope != "" {
				p.to.Scope = scope
			}
			if p.to != (labelValues{Kind: f.Kind, Scope: f.Scope, Expires: f.Expires}) {
				p.by = append(p.by, "note")
			}
			todo = append(todo, p)
			if *limit > 0 && len(todo) >= *limit {
				break
			}
		}
		var settled, model []*pendingLabel
		for _, p := range todo {
			if p.to.Kind != "" && p.to.Scope != "" {
				settled = append(settled, p)
			} else {
				model = append(model, p)
			}
		}
		if *dry {
			_, err := fmt.Fprintf(out, "%d facts lack a kind or a scope: the notes settle %d, the model would get %d "+
				"(%d calls)\n", len(todo), len(settled), len(model), (len(model)+labelBatch-1)/labelBatch)
			return err
		}
		dir, err := stateDir()
		if err != nil {
			return err
		}
		runFile := filepath.Join(dir, "label-"+time.Now().UTC().Format("20060102T150405Z")+".jsonl")
		st := labelStats{}
		st.apply(s, settled, runFile)
		if *noModel {
			st.apply(s, model, runFile) // what the notes settle of these: the scope of a project note
		} else {
			host := currentHost()
			for start := 0; start < len(model); start += labelBatch {
				batch := model[start:min(start+labelBatch, len(model))]
				if err := askLabels(batch, host, &st); err != nil {
					st.failedCalls++
					_, _ = fmt.Fprintf(out, "model call for %d facts failed: %v\n", len(batch), err)
				}
				st.apply(s, batch, runFile) // after a failed call, what the notes settle; a next run asks again
			}
		}
		_, err = fmt.Fprintf(out, "labelled %d of %d facts (notes %d, model %d), %d left without a kind or a scope, "+
			"%d failed; model calls failed: %d; $%.4f\nrun file: %s (memcue label -revert to undo)\n",
			st.labelled, len(todo), st.byNote, st.byModel, len(todo)-st.labelled-st.failed, st.failed,
			st.failedCalls, st.cost, runFile)
		return err
	})
}

// labelStats counts what a run did.
type labelStats struct {
	labelled, byNote, byModel, failed, failedCalls int
	cost                                           float64
}

// apply writes the labels of the batch that change something and records each in the run file.
func (st *labelStats) apply(s *store.Store, batch []*pendingLabel, runFile string) {
	for _, p := range batch {
		f := p.fact
		before := labelValues{Kind: f.Kind, Scope: f.Scope, Expires: f.Expires}
		if p.to == before {
			continue
		}
		rec := labelRecord{Time: time.Now().UTC().Format(time.RFC3339), ID: f.ID, From: f.Version, Before: before,
			After: p.to, By: strings.Join(p.by, "+")}
		nf, err := s.Relabel(f.ID, f.Version, p.to.Kind, p.to.Scope, p.to.Expires)
		if err != nil {
			rec.Error = err.Error()
			st.failed++
		} else {
			rec.To = nf.Version
			if p.to.Kind != "" && p.to.Scope != "" {
				st.labelled++
			}
			if strings.Contains(rec.By, "model") {
				st.byModel++
			} else {
				st.byNote++
			}
		}
		if err := appendJSON(runFile, rec); err != nil {
			fmt.Fprintln(os.Stderr, "memcue:", err)
		}
	}
}

// askLabels fills the empty fields of the batch from one model call. An answer about a fact the batch did not hold is
// dropped, and a value is taken only for a field the fact and its note left empty.
func askLabels(batch []*pendingLabel, host string, st *labelStats) error {
	in := struct {
		Host  string       `json:"host,omitempty"`
		Facts []labelInput `json:"facts"`
	}{Host: host}
	byID := map[string]*pendingLabel{}
	for _, p := range batch {
		in.Facts = append(in.Facts, labelInput{ID: p.fact.ID, Text: p.fact.Content, Origin: p.fact.Origin,
			Kind: p.to.Kind, Scope: p.to.Scope, Note: p.note})
		byID[p.fact.ID] = p
	}
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	ctx, cancel := contextTimeout()
	defer cancel()
	r, err := labelFn(ctx, string(b))
	if err != nil {
		return err
	}
	st.cost += r.CostUSD
	for _, l := range r.Labels {
		p := byID[l.ID]
		if p == nil {
			continue
		}
		took := false
		if p.to.Kind == "" && l.Kind != "" {
			p.to.Kind, took = l.Kind, true
		}
		if p.to.Scope == "" && l.Scope != "" {
			p.to.Scope, took = l.Scope, true
		}
		if p.to.Expires == "" && l.Expires != "" {
			p.to.Expires, took = l.Expires, true
		}
		if took {
			p.by = append(p.by, "model")
		}
	}
	return nil
}

// revertLabels restores the values a run's file records, for each fact still at the version the run wrote.
func revertLabels(s *store.Store, path string, out io.Writer) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	reverted, skipped := 0, 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		var rec labelRecord
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if rec.To == 0 {
			continue
		}
		_, err := s.Relabel(rec.ID, rec.To, rec.Before.Kind, rec.Before.Scope, rec.Before.Expires)
		switch {
		case err == nil:
			reverted++
		case errors.Is(err, store.ErrChanged) || errors.Is(err, store.ErrRetired):
			skipped++
			_, _ = fmt.Fprintf(out, "kept %s: %v\n", rec.ID, err)
		default:
			return err
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "reverted %d facts, kept %d changed since the run\n", reverted, skipped)
	return err
}
