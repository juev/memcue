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
	"syscall"
	"time"

	"github.com/juev/memcue/internal/extract"
	"github.com/juev/memcue/internal/recall"
	"github.com/juev/memcue/internal/store"
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
	maxGroup      = 6  // facts in one group: a chain of neighbours longer than that is cut
	groupsPerCall = 10 // groups the model sees at once
	dreamEvery    = 24 * time.Hour
)

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
	Actions []extract.Action `json:"actions,omitempty"`
	Refused []string         `json:"refused,omitempty"` // actions dropped by the checks, with why
	CostUSD float64          `json:"cost_usd"`
	Tokens  extract.Tokens   `json:"tokens"`
	Plan    string           `json:"plan,omitempty"` // the plan file a dry run wrote, or -apply applied
	Error   string           `json:"error,omitempty"`
}

// dreamPlan is what a dry run decided, for -apply to carry out later without the model: the actions of each group
// with the versions of the facts they were decided on, and what the dream would have counted as seen.
type dreamPlan struct {
	Start  time.Time        `json:"start"`
	Seen   map[string]int   `json:"seen"`
	Groups []dreamPlanGroup `json:"groups"`
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

// maybeDream runs a dream after an extraction when MEMCUE_DREAM=1 and a day has passed since the last one. The first
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
		if seen != nil && seen[f.ID] != f.Version || seen == nil && !f.Created.Before(from) {
			fresh = append(fresh, f)
		}
	}
	saw := versions(active) // what the next dream counts as seen: the base now, with this dream's own changes
	rec.Fresh = len(fresh)
	groups, err := groupFacts(s, active, fresh)
	if err != nil {
		return rec, err
	}
	rec.Groups = len(groups)
	if list {
		_, err := io.WriteString(out, renderGroups(groups, freshSet(fresh)))
		return rec, err
	}
	plan := dreamPlan{Start: start, Seen: versions(active)}
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
		for _, a := range ok {
			if !dryRun {
				f, err := apply(s, a)
				if err != nil {
					return rec, err
				}
				saw[f.ID] = f.Version // a retired fact is not active, so its entry is never compared again
			}
			rec.Actions = append(rec.Actions, a)
			if _, err := fmt.Fprintf(out, "%s %s: %s\n", a.Op, a.ID, a.Reason); err != nil {
				return rec, err
			}
		}
	}
	if !dryRun {
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
	groupOf := map[string]int{}
	for i, g := range batch {
		for _, f := range g {
			groupOf[f.ID] = i
		}
	}
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
	active, err := s.Active()
	if err != nil {
		return rec, err
	}
	now := versions(active)
	saw := maps.Clone(plan.Seen)
	for _, g := range plan.Groups {
		changed := ""
		for id, v := range g.Facts {
			if now[id] != v {
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
		}
	}
	for _, r := range rec.Refused {
		if _, err := fmt.Fprintf(out, "refused %s\n", r); err != nil {
			return rec, err
		}
	}
	return rec, errors.Join(writeDreamSeen(dir, saw), markDream(dir, plan.Start))
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
		hits, err := recall.Recall(s, f.Content+" "+strings.Join(f.Keywords, " "), "", p)
		if err != nil {
			return nil, err
		}
		for _, h := range hits {
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
// and leave every group at least one active fact; the rest come back with why they were refused.
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
	for _, a := range actions {
		g, known := groupOf[a.ID]
		switch {
		case !known:
			refused = append(refused, a.Op+" "+a.ID+": not a fact of the groups")
		case done[a.ID]:
			refused = append(refused, a.Op+" "+a.ID+": second action on the fact")
		case a.Op == "correct" && a.Content == "":
			refused = append(refused, "correct "+a.ID+": no content")
		case a.Op == "retire" && left[g] == 1:
			refused = append(refused, "retire "+a.ID+": last active fact of its group")
		case a.Op != "retire" && a.Op != "correct":
			refused = append(refused, a.Op+" "+a.ID+": unknown op")
		default:
			done[a.ID] = true
			if a.Op == "retire" {
				left[g]--
			}
			ok = append(ok, a)
		}
	}
	return ok, refused
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
