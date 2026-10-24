package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/juev/memcue/internal/extract"
	"github.com/juev/memcue/internal/recall"
	"github.com/juev/memcue/internal/store"
	"github.com/juev/memcue/internal/tier"
)

// A dream is a pass over the facts written since the last one: each new fact is grouped with the stored facts close
// to it in words, and the model says which of them to retire as duplicates or contradicted, and which to correct.
// Extraction never sees the base, so this is where two sessions' copies of one fact, or an old fact and the fact
// that disproved it, meet.

// dreamNeighbours finds the facts close to a fact: the fact itself is the prompt. Coverage is higher than for a
// prompt because a fact holds many words and shares some of them with unrelated facts.
var dreamNeighbours = recall.Params{
	Candidates:     80,
	MinTerms:       2,
	MinMatched:     3,
	MinCoverage:    0.35,
	MaxPromptTerms: 24,
	MaxFacts:       3,
}

const (
	maxGroup      = 6         // facts in one group: a chain of neighbours longer than that is cut
	groupsPerCall = 10        // groups the model sees at once
	dreamEvery    = time.Hour // with no new fact and none left without queries, a dream calls no model
)

const (
	splitBatch    = 20 // facts one split call reads
	splitParallel = 8
)

const (
	expandBatch    = 25 // facts one expansion call reads
	expandParallel = 6
)

// expandBacklog is how many facts outside the dream's fresh set one dream gives queries to: the facts of a base written
// before extraction wrote queries, which no later dream sees as fresh (#276). 500 facts are 20 calls without reasoning.
var expandBacklog = 500

// expandFn is the expansion step, replaced in tests.
var expandFn = func(input string) (extract.ExpandResult, error) {
	ctx, cancel := contextTimeout()
	defer cancel()
	return extract.ExpandFacts(ctx, input)
}

// splitFn is the split step, replaced in tests.
var splitFn = func(input string) (extract.SplitResult, error) {
	ctx, cancel := contextTimeout()
	defer cancel()
	return extract.SplitFacts(ctx, input)
}

// dreamFn is the model step, replaced in tests.
var dreamFn = func(groups string) (extract.DreamResult, error) {
	ctx, cancel := contextTimeout()
	defer cancel()
	return extract.Dream(ctx, groups)
}

// dreamRecord is one line of dream.log: what a dream changed and why, the record its decisions are checked by.
type dreamRecord struct {
	Time    string           `json:"time"`
	Since   string           `json:"since"`
	Fresh   int              `json:"fresh"`
	Groups  int              `json:"groups"`
	DryRun  bool             `json:"dry_run,omitempty"`
	Splits  []extract.Split  `json:"splits,omitempty"` // compound facts replaced by their parts
	Actions []extract.Action `json:"actions,omitempty"`
	Refused []string         `json:"refused,omitempty"` // actions dropped by the checks, with why
	// Promoted are the facts that became global: a duplicate of them was learnt in another project (#346).
	Promoted []string `json:"promoted,omitempty"`
	// SplitErrors counts split calls that failed; their facts stay as they are until the next dream reviews them.
	SplitErrors int `json:"split_errors,omitempty"`
	// Expanded counts the facts that got search queries; ExpandErrors the expansion calls that failed, whose facts
	// wait for the next dream.
	Expanded     int            `json:"expanded,omitempty"`
	ExpandErrors int            `json:"expand_errors,omitempty"`
	CostUSD      float64        `json:"cost_usd"`
	Tokens       extract.Tokens `json:"tokens"`
	Plan         string         `json:"plan,omitempty"` // the plan file a dry run wrote, or -apply applied
	Error        string         `json:"error,omitempty"`
}

// dreamPlan is what a dry run decided, for -apply to carry out later without the model: the actions of each group
// with the versions of the facts they were decided on, and what the dream would have counted as seen.
type dreamPlan struct {
	Start  time.Time        `json:"start"`
	Seen   map[string]int   `json:"seen"`
	Splits []dreamPlanSplit `json:"splits,omitempty"`
	// Expansions are the search queries for facts that had none, with the version the model read.
	Expansions []dreamPlanExpansion `json:"expansions,omitempty"`
	Groups     []dreamPlanGroup     `json:"groups"`
}

type dreamPlanExpansion struct {
	ID      string   `json:"id"`
	Version int      `json:"version"`
	Queries []string `json:"queries"`
}

// dreamPlanSplit is a compound fact the dry run would split, with the version the model read. The owner may edit the
// parts or delete the entry before -apply.
type dreamPlanSplit struct {
	ID      string   `json:"id"`
	Version int      `json:"version"`
	Parts   []string `json:"parts"`
	Reason  string   `json:"reason"`
}

type dreamPlanGroup struct {
	Facts   map[string]int   `json:"facts"` // id → version the model saw
	Actions []extract.Action `json:"actions"`
}

func runDream(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("dream", flag.ContinueOnError)
	since := fs.String("since", "", "review facts written since this date or RFC 3339 time (default: since the last dream)")
	all := fs.Bool("all", false, "review every active fact")
	dryRun := fs.Bool("dry-run", false, "ask the model but change nothing")
	list := fs.Bool("list", false, "print the groups and stop, without the model")
	plan := fs.String("plan", "", "with -dry-run: write the checked actions to this file for -apply")
	applyFile := fs.String("apply", "", "apply a plan written by -dry-run -plan, without the model")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *plan != "" && (!*dryRun || *list) {
		return errors.New("-plan needs -dry-run")
	}
	if *applyFile != "" && (*since != "" || *all || *dryRun || *list || *plan != "") {
		return errors.New("-apply takes no other flag")
	}
	dir, err := stateDir()
	if err != nil {
		return err
	}
	if *applyFile != "" {
		unlock, err := lockDream(dir)
		if err != nil {
			return err
		}
		defer unlock()
		rec, err := applyPlan(dir, *applyFile, out)
		if err != nil {
			rec.Error = err.Error()
		}
		return errors.Join(err, appendJSON(filepath.Join(dir, "dream.log"), rec))
	}
	var from time.Time
	var seen map[string]int
	switch {
	case *all:
	case *since != "":
		if from, err = parseSince(*since); err != nil {
			return err
		}
	default:
		var ok bool
		if from, ok = lastDream(dir); !ok {
			return errors.New("no dream yet on this machine: give -since DATE or -all")
		}
		if seen, err = readDreamSeen(dir); err != nil {
			return err
		}
	}
	unlock, err := lockDream(dir)
	if err != nil {
		return err
	}
	defer unlock()
	rec, err := dream(dir, from, seen, *dryRun, *list, *plan, out)
	if *list {
		return err
	}
	if err != nil {
		rec.Error = err.Error()
	}
	return errors.Join(err, appendJSON(filepath.Join(dir, "dream.log"), rec))
}

// maybeDream runs a dream after an extraction when MEMCUE_DREAM=1 and an hour has passed since the last one. The first
// call only marks the start and the facts the base holds, so the base a machine already had is not swept by surprise
// (memcue dream -all does that).
// Its failures go to dream.log and never fail the extraction.
func maybeDream() {
	if os.Getenv("MEMCUE_DREAM") != "1" {
		return
	}
	dir, err := stateDir()
	if err != nil {
		return
	}
	from, ok := lastDream(dir)
	if !ok {
		_ = markBase(dir)
		return
	}
	if time.Since(from) < dreamEvery {
		return
	}
	unlock, err := lockDream(dir)
	if err != nil {
		return // another dream is running
	}
	defer unlock()
	if from, ok = lastDream(dir); !ok || time.Since(from) < dreamEvery { // it may have just finished
		return
	}
	seen, err := readDreamSeen(dir) // unreadable: the dream falls back to the facts written since the mark, and says why
	rec, derr := dream(dir, from, seen, false, false, "", io.Discard)
	if err = errors.Join(err, derr); err != nil {
		rec.Error = err.Error()
	}
	_ = appendJSON(filepath.Join(dir, "dream.log"), rec)
}

// dream reviews the facts the last dream did not see: those of seen, the facts and versions it saw, missing. A fact
// pulled from another machine is new to it whenever it was written. Without seen, it reviews the facts written since
// from; the zero time means every active fact. A dry run with planPath writes what it decided there, once every group
// has an answer.
func dream(dir string, from time.Time, seen map[string]int, dryRun, list bool, planPath string, out io.Writer) (dreamRecord, error) {
	start := time.Now()
	rec := dreamRecord{Time: start.UTC().Format(time.RFC3339), Since: from.UTC().Format(time.RFC3339), DryRun: dryRun}
	s, err := store.Open(factsPath())
	if err != nil {
		return rec, err
	}
	defer func() { _ = s.Close() }()
	active, err := s.Active()
	if err != nil {
		return rec, err
	}
	var fresh []store.Fact
	for _, f := range active {
		if seen != nil && seen[f.ID] != f.Version {
			// A version that only gave the fact search queries or new labels says nothing new: the dream judges the
			// text, and saw this one already.
			same, err := onlyLabelsSince(s, f.ID, seen[f.ID])
			if err != nil {
				return rec, err
			}
			if !same {
				fresh = append(fresh, f)
			}
			continue
		}
		if seen == nil && !f.Created.Before(from) {
			fresh = append(fresh, f)
		}
	}
	saw := versions(active) // what the next dream counts as seen: the base now, with this dream's own changes
	rec.Fresh = len(fresh)
	plan := dreamPlan{Start: start, Seen: versions(active)}
	if !list {
		// Splitting comes first: a part that repeats another fact is then grouped with it and retired as a duplicate.
		// A dry run keeps the originals and puts the splits in the plan.
		splits, err := splitStep(s, fresh, &rec, out)
		if err != nil {
			return rec, err
		}
		for _, sp := range splits {
			if dryRun {
				plan.Splits = append(plan.Splits, sp.plan)
				continue
			}
			active = replaceFact(active, sp.plan.ID, sp.parts)
			fresh = replaceFact(fresh, sp.plan.ID, sp.parts)
			saw[sp.plan.ID] = sp.plan.Version + 1
			for _, p := range sp.parts {
				saw[p.ID] = p.Version
			}
		}
		// Then every fresh fact without search queries gets them, the parts of a split included, so the groups and
		// recall find it by the words a user asks with. A dry run leaves out the facts it would split.
		splitIDs := map[string]bool{}
		for _, sp := range plan.Splits {
			splitIDs[sp.ID] = true
		}
		var bare []store.Fact
		for _, f := range fresh {
			if len(f.Queries) == 0 && !splitIDs[f.ID] {
				bare = append(bare, f)
			}
		}
		// Facts the dream has seen and will not see again get them too, up to expandBacklog a dream, the newest first:
		// an old base is expanded over a few dreams. They are not reviewed, only expanded.
		isFresh, backlog := freshSet(fresh), 0
		for i := len(active) - 1; i >= 0 && backlog < expandBacklog; i-- {
			if f := active[i]; !isFresh[f.ID] && len(f.Queries) == 0 && !splitIDs[f.ID] {
				bare = append(bare, f)
				backlog++
			}
		}
		expanded, err := expandStep(s, bare, &rec)
		if err != nil {
			return rec, err
		}
		for _, e := range expanded {
			if dryRun {
				plan.Expansions = append(plan.Expansions, e.plan)
				continue
			}
			active = replaceFact(active, e.fact.ID, []store.Fact{e.fact})
			fresh = replaceFact(fresh, e.fact.ID, []store.Fact{e.fact})
			saw[e.fact.ID] = e.fact.Version
		}
		if rec.Expanded > 0 {
			if _, err := fmt.Fprintf(out, "expanded %d facts\n", rec.Expanded); err != nil {
				return rec, err
			}
		}
	}
	groups, err := groupFacts(s, active, fresh)
	if err != nil {
		return rec, err
	}
	rec.Groups = len(groups)
	if list {
		_, err := io.WriteString(out, renderGroups(groups, freshSet(fresh)))
		return rec, err
	}
	// The facts this dream corrects are expanded after the groups are judged: the expansion step above runs before
	// them, and a corrected fact with the queries of its old text is what #297 filed.
	var corrected []store.Fact
	for batch := range slices.Chunk(groups, groupsPerCall) {
		r, err := dreamFn(renderGroups(batch, freshSet(fresh)))
		rec.CostUSD += r.CostUSD
		rec.Tokens.Add(r.Tokens)
		if err != nil {
			return rec, err
		}
		ok, refused := checkActions(batch, r.Actions)
		rec.Refused = append(rec.Refused, refused...)
		plan.Groups = append(plan.Groups, planGroups(batch, ok)...)
		// The model decided on the text of the groups as they were sent; a fact corrected or retired while it ran
		// makes them stale, so the whole group is refused, as a plan does. A dry run changes nothing and keeps the
		// versions for -apply to check then.
		stale := make([]string, len(batch))
		if !dryRun && len(ok) > 0 {
			for i, g := range batch {
				if stale[i], err = changedFact(s, g); err != nil {
					return rec, err
				}
			}
		}
		groupOf := groupsOf(batch)
		for _, a := range ok {
			if changed := stale[groupOf[a.ID]]; changed != "" {
				rec.Refused = append(rec.Refused, a.Op+" "+a.ID+": group changed since it was sent ("+changed+")")
				continue
			}
			if !dryRun {
				f, err := apply(s, a)
				if err != nil {
					return rec, err
				}
				saw[f.ID] = f.Version // a retired fact is not active, so its entry is never compared again
				if a.Op != "retire" {
					corrected = append(corrected, f)
				}
			}
			rec.Actions = append(rec.Actions, a)
			if _, err := fmt.Fprintf(out, "%s %s: %s\n", a.Op, a.ID, a.Reason); err != nil {
				return rec, err
			}
			if err := promote(s, a, dryRun, saw, &rec, out); err != nil {
				return rec, err
			}
		}
	}
	if !dryRun {
		// A corrected fact has new text, and the queries of the old one no longer describe it: they are asked for in
		// the same run, since the expansion step above goes before the groups are judged (#297). A failure leaves the
		// fact to the next dream.
		before := rec.Expanded
		if err := expandCorrected(s, corrected, saw, &rec); err != nil {
			return rec, err
		}
		if rec.Expanded > before {
			if _, err := fmt.Fprintf(out, "expanded %d corrected facts\n", rec.Expanded-before); err != nil {
				return rec, err
			}
		}
		err = errors.Join(writeDreamSeen(dir, saw), markDream(dir, start))
	}
	if dryRun && planPath != "" {
		rec.Plan = planPath
		err = writeJSONFile(planPath, plan)
	}
	return rec, err
}

// planGroups pairs the checked actions of a batch with their groups; a group without an action needs no entry.
func planGroups(batch [][]store.Fact, ok []extract.Action) []dreamPlanGroup {
	groupOf := groupsOf(batch)
	byGroup := map[int]*dreamPlanGroup{}
	var order []int
	for _, a := range ok {
		i := groupOf[a.ID]
		pg, seen := byGroup[i]
		if !seen {
			pg = &dreamPlanGroup{Facts: versions(batch[i])}
			byGroup[i] = pg
			order = append(order, i)
		}
		pg.Actions = append(pg.Actions, a)
	}
	out := make([]dreamPlanGroup, 0, len(order))
	for _, i := range order {
		out = append(out, *byGroup[i])
	}
	return out
}

// groupsOf maps each fact of a batch to its group's index.
func groupsOf(groups [][]store.Fact) map[string]int {
	m := make(map[string]int)
	for i, g := range groups {
		for _, f := range g {
			m[f.ID] = i
		}
	}
	return m
}

// changedFact re-reads the facts of a group sent to the model and returns the id of the first whose version is not the
// one sent: a fact corrected, retired or reopened while the model ran. The live dream refuses such a group whole, as a
// plan does; no fact with that id means the same.
func changedFact(s *store.Store, g []store.Fact) (string, error) {
	for _, f := range g {
		cur, err := s.Get(f.ID)
		if errors.Is(err, store.ErrNotFound) {
			return f.ID, nil
		}
		if err != nil {
			return "", err
		}
		if cur.Version != f.Version {
			return f.ID, nil
		}
	}
	return "", nil
}

// applyPlan carries out a plan written by a dry run. A group whose facts changed since then (a new version, or
// retired) is refused whole, since its actions were decided on the old text; the rest apply, and the dream is marked
// as of the dry run, with what it saw.
func applyPlan(dir, path string, out io.Writer) (dreamRecord, error) {
	rec := dreamRecord{Time: time.Now().UTC().Format(time.RFC3339), Plan: path}
	b, err := os.ReadFile(path)
	if err != nil {
		return rec, err
	}
	var plan dreamPlan
	if err := json.Unmarshal(b, &plan); err != nil {
		return rec, fmt.Errorf("plan %s: %w", path, err)
	}
	if plan.Seen == nil || plan.Start.IsZero() {
		return rec, fmt.Errorf("plan %s: not a dream plan", path)
	}
	rec.Since = plan.Start.UTC().Format(time.RFC3339)
	rec.Groups = len(plan.Groups)
	s, err := store.Open(factsPath())
	if err != nil {
		return rec, err
	}
	defer func() { _ = s.Close() }()
	saw := maps.Clone(plan.Seen)
	// Splits go first, so a group the model saw with the compound fact in it is refused below as changed. The parts
	// are not marked seen: the next dream groups them with their neighbours, which this plan never did.
	for _, sp := range plan.Splits {
		// The owner may have edited the parts: a split left with fewer than two is refused, not a reason to stop.
		var kept []string
		for _, p := range sp.Parts {
			if p = strings.TrimSpace(p); p != "" {
				kept = append(kept, p)
			}
		}
		if len(kept) < 2 {
			rec.Refused = append(rec.Refused, fmt.Sprintf("split %s: %d part(s) in the plan", sp.ID, len(kept)))
			continue
		}
		sp.Parts = kept
		parts, err := s.SplitFact(sp.ID, sp.Version, sp.Parts)
		switch {
		case errors.Is(err, store.ErrChanged) || errors.Is(err, store.ErrRetired) || errors.Is(err, store.ErrNotFound):
			rec.Refused = append(rec.Refused, "split "+sp.ID+": "+err.Error())
			continue
		case err != nil:
			return rec, err
		}
		saw[sp.ID] = sp.Version + 1
		rec.Splits = append(rec.Splits, extract.Split{ID: sp.ID, Parts: sp.Parts, Reason: sp.Reason})
		if err := printSplit(out, sp.ID, sp.Reason, parts); err != nil {
			return rec, err
		}
	}
	// Expansions next. The version they write is the plan's own change, so the groups below still count the fact as
	// unchanged at that version; any other change refuses its group.
	expandedFrom := map[string]int{}
	for _, e := range plan.Expansions {
		f, err := s.SetQueries(e.ID, e.Version, extract.CleanQueries(e.Queries))
		switch {
		case errors.Is(err, store.ErrChanged) || errors.Is(err, store.ErrRetired) || errors.Is(err, store.ErrNotFound):
			rec.Refused = append(rec.Refused, "expand "+e.ID+": "+err.Error())
			continue
		case err != nil:
			return rec, err
		}
		expandedFrom[e.ID] = e.Version
		saw[e.ID] = f.Version
		rec.Expanded++
	}
	if rec.Expanded > 0 {
		if _, err := fmt.Fprintf(out, "expanded %d facts\n", rec.Expanded); err != nil {
			return rec, err
		}
	}
	// The snapshot must name every fact of the plan, including provisional and retired ones: store.Active leaves
	// provisional facts out, and a group with a provisional neighbour would otherwise look changed and be refused
	// although nothing happened since the dry run.
	now := make(map[string]int)
	for _, g := range plan.Groups {
		for id := range g.Facts {
			if _, ok := now[id]; ok {
				continue
			}
			f, err := s.Get(id)
			if errors.Is(err, store.ErrNotFound) {
				now[id] = 0
				continue
			}
			if err != nil {
				return rec, err
			}
			now[id] = f.Version
		}
	}
	for _, g := range plan.Groups {
		changed := ""
		for id, v := range g.Facts {
			if now[id] != v && (expandedFrom[id] != v || now[id] != v+1) {
				changed = id
				break
			}
		}
		for _, a := range g.Actions {
			if changed != "" {
				rec.Refused = append(rec.Refused, a.Op+" "+a.ID+": group changed since the plan ("+changed+")")
				continue
			}
			f, err := apply(s, a)
			if err != nil {
				return rec, err
			}
			saw[f.ID] = f.Version
			rec.Actions = append(rec.Actions, a)
			if _, err := fmt.Fprintf(out, "%s %s: %s\n", a.Op, a.ID, a.Reason); err != nil {
				return rec, err
			}
			if err := promote(s, a, false, saw, &rec, out); err != nil {
				return rec, err
			}
		}
	}
	for _, r := range rec.Refused {
		if _, err := fmt.Fprintf(out, "refused %s\n", r); err != nil {
			return rec, err
		}
	}
	return rec, errors.Join(writeDreamSeen(dir, saw), markDream(dir, plan.Start))
}

// dreamSplit is one checked split: what a plan keeps of it and, once written, the parts.
type dreamSplit struct {
	plan  dreamPlanSplit
	parts []store.Fact
}

// splitStep asks the model which fresh facts hold several claims and, unless the record is a dry run, replaces them
// with their parts. A failed call leaves its facts as they are and is counted; an answer
// about a fact that was not sent, a second answer about one fact or fewer than two parts is refused. A fact that
// changed while the model ran is refused too, since the parts were read off its old text.
func splitStep(s *store.Store, fresh []store.Fact, rec *dreamRecord, out io.Writer) ([]dreamSplit, error) {
	batches := slices.Collect(slices.Chunk(fresh, splitBatch))
	res := make([]extract.SplitResult, len(batches))
	errs := make([]error, len(batches))
	sem := make(chan struct{}, splitParallel)
	var wg sync.WaitGroup
	for i, batch := range batches {
		input, err := renderSplitInput(batch)
		if err != nil {
			return nil, err
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			res[i], errs[i] = splitFn(input)
		}()
	}
	wg.Wait()
	byID := make(map[string]store.Fact, len(fresh))
	for _, f := range fresh {
		byID[f.ID] = f
	}
	var found []dreamSplit
	done := map[string]bool{}
	for i := range batches {
		rec.CostUSD += res[i].CostUSD
		rec.Tokens.Add(res[i].Tokens)
		if errs[i] != nil {
			rec.SplitErrors++
			continue
		}
		sent := map[string]bool{}
		for _, f := range batches[i] {
			sent[f.ID] = true
		}
		for _, sp := range res[i].Splits {
			switch {
			case !sent[sp.ID]:
				rec.Refused = append(rec.Refused, "split "+sp.ID+": not a fact of the batch")
				continue
			case done[sp.ID]:
				rec.Refused = append(rec.Refused, "split "+sp.ID+": second split of the fact")
				continue
			case len(sp.Parts) < 2:
				rec.Refused = append(rec.Refused, fmt.Sprintf("split %s: %d part(s)", sp.ID, len(sp.Parts)))
				continue
			}
			done[sp.ID] = true
			f := byID[sp.ID]
			ds := dreamSplit{plan: dreamPlanSplit{ID: f.ID, Version: f.Version, Parts: sp.Parts, Reason: sp.Reason}}
			if !rec.DryRun {
				parts, err := s.SplitFact(f.ID, f.Version, sp.Parts)
				if errors.Is(err, store.ErrChanged) || errors.Is(err, store.ErrRetired) || errors.Is(err, store.ErrNotFound) {
					rec.Refused = append(rec.Refused, "split "+f.ID+": "+err.Error())
					continue
				}
				if err != nil {
					return found, err
				}
				ds.parts = parts
			}
			rec.Splits = append(rec.Splits, sp)
			if err := printSplit(out, sp.ID, sp.Reason, ds.parts); err != nil {
				return found, err
			}
			if rec.DryRun {
				for _, p := range sp.Parts {
					if _, err := fmt.Fprintf(out, "  + %s\n", p); err != nil {
						return found, err
					}
				}
			}
			found = append(found, ds)
		}
	}
	return found, nil
}

// onlyLabelsSince reports whether every version of the fact after seen changed nothing but its search queries, kind,
// scope or expiry: what the queries step writes and the removed `memcue label` (#251) wrote, also on other machines
// that sync this base. A fact the last dream did not see at all (seen is 0) is new.
func onlyLabelsSince(s *store.Store, id string, seen int) (bool, error) {
	if seen <= 0 {
		return false, nil
	}
	hist, err := s.History(id)
	if err != nil {
		return false, err
	}
	var prev *store.Fact
	for i := range hist {
		f := &hist[i]
		if f.Version < seen {
			continue
		}
		if f.Version == seen {
			prev = f
			continue
		}
		if prev == nil || f.Content != prev.Content || !slices.Equal(f.Keywords, prev.Keywords) ||
			f.Origin != prev.Origin || f.Status != prev.Status || f.Source != prev.Source ||
			f.Evidence != prev.Evidence || f.Review != prev.Review || f.PromptVersion != prev.PromptVersion {
			return false, nil
		}
		prev = f
	}
	return prev != nil, nil
}

// dreamExpansion is one fact that got queries: what a plan keeps of it and, once written, the new version.
type dreamExpansion struct {
	plan dreamPlanExpansion
	fact store.Fact
}

// expandStep asks the main model for the search queries of facts that have none and, unless the record is a dry run,
// writes them. A failed call leaves its facts for the next dream and is counted, as a failed split call is; an answer
// about a fact that was not sent, or with no query, is ignored; a fact changed while the model ran is refused.
func expandStep(s *store.Store, facts []store.Fact, rec *dreamRecord) ([]dreamExpansion, error) {
	batches := slices.Collect(slices.Chunk(facts, expandBatch))
	res := make([]extract.ExpandResult, len(batches))
	errs := make([]error, len(batches))
	sem := make(chan struct{}, expandParallel)
	var wg sync.WaitGroup
	for i, batch := range batches {
		input, err := renderSplitInput(batch)
		if err != nil {
			return nil, err
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			res[i], errs[i] = expandFn(input)
		}()
	}
	wg.Wait()
	var found []dreamExpansion
	for i, batch := range batches {
		rec.CostUSD += res[i].CostUSD
		rec.Tokens.Add(res[i].Tokens)
		if errs[i] != nil {
			rec.ExpandErrors++
			continue
		}
		byID := make(map[string]store.Fact, len(batch))
		for _, f := range batch {
			byID[f.ID] = f
		}
		for _, e := range res[i].Facts {
			f, ok := byID[e.ID]
			if !ok || len(e.Queries) == 0 {
				continue
			}
			delete(byID, e.ID) // a second answer about the same fact is ignored
			x := dreamExpansion{plan: dreamPlanExpansion{ID: f.ID, Version: f.Version, Queries: e.Queries}, fact: f}
			if !rec.DryRun {
				nf, err := s.SetQueries(f.ID, f.Version, e.Queries)
				if errors.Is(err, store.ErrChanged) || errors.Is(err, store.ErrRetired) || errors.Is(err, store.ErrNotFound) {
					rec.Refused = append(rec.Refused, "expand "+f.ID+": "+err.Error())
					continue
				}
				if err != nil {
					return found, err
				}
				x.fact = nf
			}
			rec.Expanded++
			found = append(found, x)
		}
	}
	return found, nil
}

// expandCorrected gives the facts a dream just corrected their search queries in the same run. Only facts without
// queries are asked about, and a failed call leaves the fact to the next dream; the fact is corrected either way. The
// snapshot the next dream compares against takes the version the expansion wrote.
func expandCorrected(s *store.Store, corrected []store.Fact, saw map[string]int, rec *dreamRecord) error {
	var bare []store.Fact
	for _, f := range corrected {
		if len(f.Queries) == 0 {
			bare = append(bare, f)
		}
	}
	if len(bare) == 0 {
		return nil
	}
	expanded, err := expandStep(s, bare, rec)
	if err != nil {
		return err
	}
	for _, e := range expanded {
		saw[e.fact.ID] = e.fact.Version
	}
	return nil
}

// renderSplitInput is the JSON the split prompt reads: the id and text of each fact.
func renderSplitInput(facts []store.Fact) (string, error) {
	type item struct {
		ID   string `json:"id"`
		Text string `json:"text"`
	}
	items := make([]item, len(facts))
	for i, f := range facts {
		items[i] = item{ID: f.ID, Text: f.Content}
	}
	b, err := json.Marshal(map[string]any{"facts": items})
	return string(b), err
}

// printSplit reports a split and, once written, the parts with their new ids.
func printSplit(out io.Writer, id, reason string, parts []store.Fact) error {
	if _, err := fmt.Fprintf(out, "split %s: %s\n", id, reason); err != nil {
		return err
	}
	for _, p := range parts {
		if _, err := fmt.Fprintf(out, "  + %s %s\n", p.ID, p.Content); err != nil {
			return err
		}
	}
	return nil
}

// replaceFact puts parts in the place of the fact id.
func replaceFact(facts []store.Fact, id string, parts []store.Fact) []store.Fact {
	i := slices.IndexFunc(facts, func(f store.Fact) bool { return f.ID == id })
	if i < 0 {
		return facts
	}
	return slices.Concat(facts[:i:i], parts, facts[i+1:])
}

// writeJSONFile replaces path through a temporary file next to it.
func writeJSONFile(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// versions maps each fact to its version.
func versions(facts []store.Fact) map[string]int {
	m := make(map[string]int, len(facts))
	for _, f := range facts {
		m[f.ID] = f.Version
	}
	return m
}

// markBase marks a first dream on this machine: from now on, and with every fact the base holds already seen.
func markBase(dir string) error {
	s, err := store.Open(factsPath())
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()
	active, err := s.Active()
	if err != nil {
		return err
	}
	return errors.Join(writeDreamSeen(dir, versions(active)), markDream(dir, time.Now()))
}

// readDreamSeen reads the facts and versions the last dream saw, one "ID VERSION" a line; no file gives nil, and the dream
// then goes by when facts were written, as before the file existed.
func readDreamSeen(dir string) (map[string]int, error) {
	b, err := os.ReadFile(filepath.Join(dir, "dream.seen"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	seen := map[string]int{}
	for line := range strings.Lines(string(b)) {
		id, v, ok := strings.Cut(strings.TrimSpace(line), " ")
		n, err := strconv.Atoi(v)
		if !ok || err != nil {
			return nil, fmt.Errorf("dream.seen: bad line %q", strings.TrimSpace(line))
		}
		seen[id] = n
	}
	return seen, nil
}

// writeDreamSeen replaces dream.seen through a temporary file, so a crash leaves the old list or the new one.
func writeDreamSeen(dir string, seen map[string]int) error {
	ids := slices.Sorted(maps.Keys(seen))
	var b strings.Builder
	for _, id := range ids {
		fmt.Fprintf(&b, "%s %d\n", id, seen[id])
	}
	tmp := filepath.Join(dir, "dream.seen.tmp")
	if err := os.WriteFile(tmp, []byte(b.String()), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, "dream.seen"))
}

func freshSet(fresh []store.Fact) map[string]bool {
	m := map[string]bool{}
	for _, f := range fresh {
		m[f.ID] = true
	}
	return m
}

// groupFacts joins each fresh fact with its neighbours; groups that share a fact become one. A group is cut at
// maxGroup facts, and a fact with no neighbour makes no group.
func groupFacts(s *store.Store, active, fresh []store.Fact) ([][]store.Fact, error) {
	byID := map[string]store.Fact{}
	for _, f := range active {
		byID[f.ID] = f
	}
	parent := map[string]string{}
	size := map[string]int{}
	var find func(string) string
	find = func(id string) string {
		if _, ok := parent[id]; !ok {
			parent[id], size[id] = id, 1
		}
		if parent[id] != id {
			parent[id] = find(parent[id])
		}
		return parent[id]
	}
	union := func(a, b string) {
		ra, rb := find(a), find(b)
		if ra == rb || size[ra]+size[rb] > maxGroup {
			return
		}
		parent[rb] = ra
		size[ra] += size[rb]
	}
	for _, f := range fresh {
		p := dreamNeighbours
		p.Seen = map[string]bool{f.ID: true}
		hits, err := recall.Recall(s, f.Content+" "+strings.Join(f.Keywords, " "), nil, p)
		if err != nil {
			return nil, err
		}
		for _, h := range hits {
			// Recall returns provisional facts too: they are indexed and active, but store.Active leaves them out.
			// Take the hit's own fact, so a provisional neighbour is a real group member rather than a zero fact.
			if _, ok := byID[h.ID]; !ok {
				if h.Review != store.Provisional {
					continue
				}
				byID[h.ID] = h.Fact
			}
			union(f.ID, h.ID)
		}
	}
	members := map[string][]store.Fact{}
	for id := range parent {
		r := find(id)
		members[r] = append(members[r], byID[id])
	}
	var groups [][]store.Fact
	for _, g := range members {
		if len(g) < 2 {
			continue
		}
		slices.SortFunc(g, func(a, b store.Fact) int { return strings.Compare(a.ID, b.ID) })
		groups = append(groups, g)
	}
	slices.SortFunc(groups, func(a, b []store.Fact) int { return strings.Compare(a[0].ID, b[0].ID) })
	return groups, nil
}

func renderGroups(groups [][]store.Fact, fresh map[string]bool) string {
	var b strings.Builder
	for i, g := range groups {
		fmt.Fprintf(&b, "## Группа %d\n\n", i+1)
		for _, f := range g {
			mark := ""
			if fresh[f.ID] {
				mark = ", новый"
			}
			fmt.Fprintf(&b, "- id %s (origin: %s; записан %s%s; ключи: %s)\n  %s\n", f.ID, f.Origin,
				f.Created.Format(time.DateOnly), mark, strings.Join(f.Keywords, ", "),
				strings.Join(strings.Fields(f.Content), " "))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// checkActions keeps the actions that name a fact of the groups sent, at most one per fact, a correction with text,
// a retirement by another fact of its group that stays, a duplicate retired in favour of the smaller id, and leave
// every group at least one active fact; the rest come back with why they were refused.
func checkActions(groups [][]store.Fact, actions []extract.Action) (ok []extract.Action, refused []string) {
	groupOf := map[string]int{}
	left := make([]int, len(groups))
	for i, g := range groups {
		left[i] = len(g)
		for _, f := range g {
			groupOf[f.ID] = i
		}
	}
	done := map[string]bool{}
	retired := map[string]bool{}
	covers := map[string]bool{} // facts named as by: they stay
	for _, a := range actions {
		g, known := groupOf[a.ID]
		byGroup, byKnown := groupOf[a.By]
		switch {
		case !known:
			refused = append(refused, a.Op+" "+a.ID+": not a fact of the groups")
		case done[a.ID]:
			refused = append(refused, a.Op+" "+a.ID+": second action on the fact")
		case a.Op == "correct" && a.Content == "":
			refused = append(refused, "correct "+a.ID+": no content")
		case a.Op == "retire" && left[g] == 1:
			refused = append(refused, "retire "+a.ID+": last active fact of its group")
		case a.Op == "retire" && (!byKnown || byGroup != g || a.By == a.ID):
			refused = append(refused, "retire "+a.ID+": by "+a.By+" is not another fact of its group")
		case a.Op == "retire" && retired[a.By]:
			refused = append(refused, "retire "+a.ID+": by "+a.By+" is retired too")
		case a.Op == "retire" && covers[a.ID]:
			refused = append(refused, "retire "+a.ID+": it covers another retired fact")
		case a.Op == "retire" && a.Kind != "duplicate" && a.Kind != "outdated":
			refused = append(refused, "retire "+a.ID+": kind "+a.Kind+" is neither duplicate nor outdated")
		case a.Op == "retire" && a.Kind == "duplicate" && a.ID < a.By:
			// every machine keeps the smaller ID of a duplicate pair, so two dreams over one base cannot retire both
			refused = append(refused, "retire "+a.ID+": a duplicate keeps the smaller id, retire "+a.By+" instead")
		case a.Op != "retire" && a.Op != "correct":
			refused = append(refused, a.Op+" "+a.ID+": unknown op")
		default:
			done[a.ID] = true
			if a.Op == "retire" {
				left[g]--
				retired[a.ID], covers[a.By] = true, true
			}
			ok = append(ok, a)
		}
	}
	return ok, refused
}

// promote makes the fact a duplicate was retired for global when the two were learnt in different projects: a fact
// learnt twice, independently, in two projects holds in both, so it is no project's fact (#346). The code decides
// this, not the model, which only said that one fact covers the other. A dry run reports it without writing. The
// survivor is read now, not as the group showed it: an earlier action of the batch may have corrected it.
func promote(s *store.Store, a extract.Action, dryRun bool, saw map[string]int, rec *dreamRecord, out io.Writer) error {
	if a.Op != "retire" || a.Kind != "duplicate" || a.By == "" {
		return nil
	}
	gone, err := s.Get(a.ID)
	if err != nil {
		return err
	}
	by, err := s.Get(a.By)
	if err != nil {
		return err
	}
	from, to := projectOf(gone), projectOf(by)
	if from == "" || to == "" || from == to || by.Status != store.Active {
		return nil
	}
	if !dryRun {
		f, err := s.Relabel(by.ID, by.Version, by.Kind, "global", by.Expires)
		if err != nil {
			return err
		}
		saw[f.ID] = f.Version
	}
	rec.Promoted = append(rec.Promoted, by.ID)
	_, err = fmt.Fprintf(out, "global %s: a duplicate was learnt in %s, it in %s\n", by.ID, from, to)
	return err
}

// projectOf is the project a fact holds in: its repo: or dir: scope, or, with no scope, the project its origin names.
// A global fact, one of a host or a service, and one learnt nowhere in particular (the Claude config directory, an
// empty origin) have none.
func projectOf(f store.Fact) string {
	sc := strings.ToLower(f.Scope)
	switch {
	case strings.HasPrefix(sc, "repo:") || strings.HasPrefix(sc, "dir:"):
		return sc
	case sc != "":
		return ""
	}
	if k := originKey(f.Origin); k != "" && k != tier.Global {
		return tier.Scope(k)
	}
	return ""
}

// apply makes one checked action and returns the version it wrote.
func apply(s *store.Store, a extract.Action) (store.Fact, error) {
	if a.Op == "retire" {
		return s.Retire(a.ID)
	}
	keywords := a.Keywords
	if len(keywords) == 0 { // the model gave only the text: the words the fact is asked by stay
		cur, err := s.Get(a.ID)
		if err != nil {
			return store.Fact{}, err
		}
		keywords = cur.Keywords
	}
	return s.Correct(a.ID, a.Content, keywords)
}

func parseSince(v string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, nil
	}
	t, err := time.ParseInLocation(time.DateOnly, v, time.Local)
	if err != nil {
		return time.Time{}, fmt.Errorf("-since %q: want YYYY-MM-DD or an RFC 3339 time", v)
	}
	return t, nil
}

// lastDream reads when the last dream on this machine started.
func lastDream(dir string) (time.Time, bool) {
	b, err := os.ReadFile(filepath.Join(dir, "dream.at"))
	if err != nil {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(b)))
	return t, err == nil
}

func markDream(dir string, t time.Time) error {
	return os.WriteFile(filepath.Join(dir, "dream.at"), []byte(t.UTC().Format(time.RFC3339Nano)+"\n"), 0o600)
}

// lockDream keeps two dreams on one machine from deciding over the same facts; it does not wait.
func lockDream(dir string) (func(), error) {
	f, err := os.OpenFile(filepath.Join(dir, "dream.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, errors.New("another dream is running")
	}
	return func() { _ = f.Close() }, nil
}

func appendJSON(path string, v any) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	b, err := json.Marshal(v)
	if err != nil {
		_ = f.Close()
		return err
	}
	_, err = f.Write(append(b, '\n'))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}
