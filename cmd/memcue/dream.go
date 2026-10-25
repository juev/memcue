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
	"github.com/juev/memcue/internal/tier"
)

// A dream is a pass over the facts written since the last one: each new fact is grouped with the stored facts close
// to it in words, and the session's own model says which of them to retire as duplicates or contradicted, and which
// to correct. memcue starts no model for it: -prepare hands the work to the model of the session that runs it (a Claude
// Code skill, a Hermes cron job), and -apply checks and carries out the answer.
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

// groupNeighbours is the search a dream groups by: dreamNeighbours, and also the facts whose own terms the fact holds
// nearly all of. A restatement in other words is grouped with the fact it restates whichever of the two is the
// fresh one; coverage alone groups a short copy only when the short one is fresh (#377). It stays a share of the
// short fact's terms, so two facts that each add their own detail to one topic do not pass by it.
var groupNeighbours = func() recall.Params {
	p := dreamNeighbours
	p.MinContained = 0.8
	return p
}()

const (
	maxGroup   = 6         // facts in one group: a chain of neighbours longer than that is cut
	dreamEvery = time.Hour // default of MEMCUE_DREAM_EVERY
)

// The bounds of one packet keep the context a session spends on a dream small: the fresh facts it reviews, the groups
// it holds and the facts without search queries it gives them to. What is left waits for the next packet: a base
// written before extraction wrote queries is expanded over a few dreams (#276).
var (
	packetFresh  = 40
	packetGroups = 30
	packetExpand = 50
	// packetTranslate is the facts a dream translates (#396): 40 kept every answer in one call when #395 measured
	// a whole base, and a base of 3 000 facts is done in about 75 dreams.
	packetTranslate = 40
)

// The merge sweep groups facts the dream has already seen, so a duplicate pair that predates the last dream is
// reviewed too (#377): each dream takes up to mergeSweep of them after the cursor in dream.merge, and adds at most
// mergeGroups groups of its own, so a large base costs a few groups a dream and is walked over many.
var (
	mergeSweep  = 50
	mergeGroups = 20
)

// dreamRecord is one line of dream.log: what a dream changed and why, the record its decisions are checked by.
type dreamRecord struct {
	Time    string           `json:"time"`
	Since   string           `json:"since"`
	Fresh   int              `json:"fresh"`
	Groups  int              `json:"groups"`
	Splits  []extract.Split  `json:"splits,omitempty"` // compound facts replaced by their parts
	Actions []extract.Action `json:"actions,omitempty"`
	Refused []string         `json:"refused,omitempty"` // actions dropped by the checks, with why
	// Promoted are the facts that became global: a duplicate of them was learnt in another project (#346).
	Promoted []string `json:"promoted,omitempty"`
	// Expanded counts the facts that got search queries.
	Expanded int `json:"expanded,omitempty"`
	// Translated counts the facts that got their translation (#396).
	Translated int `json:"translated,omitempty"`
	// Swept counts the facts seen before that the merge sweep grouped with their neighbours.
	Swept int `json:"swept,omitempty"`
	// Grouped is what the dream grouped and what came of each group, by id only: it answers whether two facts were
	// grouped in a run without the model.
	Grouped []dreamGroupRecord `json:"grouped,omitempty"`
	Packet  string             `json:"packet,omitempty"` // the packet -prepare wrote, or -apply applied
	Error   string             `json:"error,omitempty"`
}

// dreamGroupRecord is one group of a dream in dream.log. Outcome is "acted" when an action on it was applied,
// "refused" when the model proposed actions and none was applied, "kept" when the model proposed none, and
// "deferred" when the packet bounds left the group for a later dream.
type dreamGroupRecord struct {
	Facts   []string `json:"facts"`
	Fresh   []string `json:"fresh,omitempty"` // the facts of the group the dream reviewed as new, not swept
	Outcome string   `json:"outcome"`
	Acted   []string `json:"acted,omitempty"` // the facts retired or corrected
}

// dreamPlan is a checked answer: the actions of each group with the versions of the facts they were decided on, and
// what the dream counts as seen.
type dreamPlan struct {
	Start  time.Time        `json:"start"`
	Seen   map[string]int   `json:"seen"`
	Splits []dreamPlanSplit `json:"splits,omitempty"`
	// Expansions are the search queries for facts that had none, with the version the model read.
	Expansions []dreamPlanExpansion `json:"expansions,omitempty"`
	// Translations are the facts' text in the other language, with the version the model read (#396).
	Translations []dreamPlanTranslation `json:"translations,omitempty"`
	Groups       []dreamPlanGroup       `json:"groups"`
	Merge        string                 `json:"merge,omitempty"` // the sweep cursor to keep once the plan is carried out
}

type dreamPlanExpansion struct {
	ID      string   `json:"id"`
	Version int      `json:"version"`
	Queries []string `json:"queries"`
}

type dreamPlanTranslation struct {
	ID          string `json:"id"`
	Version     int    `json:"version"`
	Translation string `json:"translation"`
}

// dreamPlanSplit is a compound fact to split, with the version the model read.
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
	prep := fs.Bool("prepare", false, "print the work of the dream for the session's model and keep it for -apply")
	list := fs.Bool("list", false, "print the groups and stop")
	applyFile := fs.String("apply", "", "check and carry out the model's answer to the packet -prepare printed")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *applyFile != "" && (*since != "" || *all || *prep || *list) {
		return errors.New("-apply takes no other flag")
	}
	if *applyFile == "" && !*prep && !*list {
		return errors.New("memcue dream needs -prepare, -list or -apply FILE")
	}
	dir, err := stateDir()
	if err != nil {
		return err
	}
	unlock, err := lockDream(dir)
	if err != nil {
		return err
	}
	defer unlock()
	if *applyFile != "" {
		rec, err := applyAnswer(dir, *applyFile, out)
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
	rec, err := prepare(dir, from, seen, *list, out)
	switch {
	case *list:
		return err
	case err != nil:
		rec.Error = err.Error()
	case rec.Packet != "":
		return nil // the dream is logged when its answer is applied
	}
	return errors.Join(err, appendJSON(filepath.Join(dir, "dream.log"), rec))
}

// dreamEveryFromEnv reads MEMCUE_DREAM_EVERY, how long after a dream the session start says the next one is due: a Go
// duration of at least a minute, or def.
func dreamEveryFromEnv(def time.Duration) time.Duration {
	d, err := time.ParseDuration(strings.TrimSpace(os.Getenv("MEMCUE_DREAM_EVERY")))
	if err != nil || d < time.Minute {
		return def
	}
	return d
}

// freshFacts are the active facts the last dream did not see: those of seen, the facts and versions it saw, missing. A
// fact pulled from another machine is new whenever it was written. Without seen, they are the facts written since
// from; the zero time means every active fact.
func freshFacts(s *store.Store, active []store.Fact, from time.Time, seen map[string]int) ([]store.Fact, error) {
	var fresh []store.Fact
	for _, f := range active {
		if seen != nil && seen[f.ID] != f.Version {
			// A version that only gave the fact search queries or new labels says nothing new: the dream judges the
			// text, and saw this one already.
			same, err := onlyLabelsSince(s, f.ID, seen[f.ID])
			if err != nil {
				return nil, err
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
	return fresh, nil
}

// dreamDue says, when a dream is due, how many facts are fresh and how many are owed a translation (#396): the
// interval since the last one has passed and some fact is either. The first call on a machine only marks the base, so
// the facts it already had are not swept by surprise (memcue dream -prepare -all does that).
func dreamDue(dir string, every time.Duration) (fresh, owed int, last time.Time, err error) {
	last, ok := lastDream(dir)
	if !ok {
		unlock, err := lockDream(dir)
		if err != nil {
			return 0, 0, time.Time{}, nil // a dream is running: it marks the base itself
		}
		defer unlock()
		if _, ok := lastDream(dir); ok {
			return 0, 0, time.Time{}, nil
		}
		return 0, 0, time.Time{}, markBase(dir)
	}
	if time.Since(last) < every {
		return 0, 0, last, nil
	}
	seen, err := readDreamSeen(dir)
	if err != nil {
		return 0, 0, last, err
	}
	s, err := store.Open(factsPath())
	if err != nil {
		return 0, 0, last, err
	}
	defer func() { _ = s.Close() }()
	active, err := s.Active()
	if err != nil {
		return 0, 0, last, err
	}
	for _, f := range active {
		if owesTranslation(f) {
			owed++
		}
	}
	fr, err := freshFacts(s, active, last, seen)
	return len(fr), owed, last, err
}

// owesTranslation reports whether a fact has no translation among its queries yet: stored before #395, or corrected
// since, which drops the queries of the old text. The dream gives it one, packetTranslate a dream.
func owesTranslation(f store.Fact) bool {
	_, ok := extract.Translation(f.Queries)
	return !ok
}

// dreamPacket is what -prepare handed to the model, kept in the state directory for -apply: the facts and versions
// each part of the work was read at, and what the dream counts as seen once the answer is applied.
type dreamPacket struct {
	ID     string         `json:"id"`
	Start  time.Time      `json:"start"`
	Seen   map[string]int `json:"seen"`
	Split  map[string]int `json:"split"`
	Expand map[string]int `json:"expand"`
	// Translate are the facts owed a translation (#396), which no other part of the packet changes.
	Translate map[string]int   `json:"translate,omitempty"`
	Groups    []map[string]int `json:"groups"`
	// Deferred are the groups the packet bounds left out, kept for the record only.
	Deferred []dreamGroupRecord `json:"deferred,omitempty"`
	Merge    string             `json:"merge,omitempty"` // the sweep cursor once the answer is applied
	Swept    int                `json:"swept,omitempty"`
}

// dreamAnswer is what the model gives back for a packet.
type dreamAnswer struct {
	Packet       string               `json:"packet"`
	Splits       []extract.Split      `json:"splits"`
	Expansions   []extract.Expansion  `json:"expansions"`
	Actions      []extract.Action     `json:"actions"`
	Translations []extract.Translated `json:"translations"`
}

const packetFile = "dream.packet.json"

// prepare writes the work of a dream for the session's model: the fresh facts to split, the facts without search
// queries to expand, and the groups of similar facts to review, with the instructions for each. It keeps the packet
// for -apply and prints it. Fresh facts left out by the packet bounds are not counted as seen, so the next packet
// takes them.
func prepare(dir string, from time.Time, seen map[string]int, list bool, out io.Writer) (dreamRecord, error) {
	start := time.Now()
	rec := dreamRecord{Time: start.UTC().Format(time.RFC3339), Since: from.UTC().Format(time.RFC3339)}
	s, err := store.Open(factsPath())
	if err != nil {
		return rec, err
	}
	defer func() { _ = s.Close() }()
	active, err := s.Active()
	if err != nil {
		return rec, err
	}
	fresh, err := freshFacts(s, active, from, seen)
	if err != nil {
		return rec, err
	}
	rec.Fresh = len(fresh)
	batch := fresh
	if len(batch) > packetFresh {
		batch = batch[:packetFresh]
	}
	isFresh := freshSet(fresh)
	gr := newGrouper(active)
	for _, f := range batch {
		if err := gr.add(s, f); err != nil {
			return rec, err
		}
	}
	// The sweep takes the room the fresh groups leave in the packet, so it never pushes a fresh fact to the next one.
	room := min(mergeGroups, packetGroups-len(gr.groups()))
	cursor, err := readMergeCursor(dir)
	if err != nil {
		return rec, err
	}
	swept := 0
	isBatch := freshSet(batch)
	for _, f := range sweepFacts(active, isFresh, cursor) {
		if swept == mergeSweep || gr.groupsWithout(isBatch) >= room {
			break
		}
		if err := gr.add(s, f); err != nil {
			return rec, err
		}
		cursor = f.ID
		swept++
	}
	rec.Swept = swept
	// Groups with a fresh fact come first, so the packet bound below leaves out sweep groups before them.
	var groups, sweepGroups [][]store.Fact
	for _, g := range gr.groups() {
		if slices.ContainsFunc(g, func(f store.Fact) bool { return isBatch[f.ID] }) {
			groups = append(groups, g)
		} else {
			sweepGroups = append(sweepGroups, g)
		}
	}
	groups = append(groups, sweepGroups...)
	var deferred []dreamGroupRecord
	if len(groups) > packetGroups {
		// A fresh fact of a group left out waits for the next packet with the rest of its group.
		left := map[string]bool{}
		for _, g := range groups[packetGroups:] {
			r := dreamGroupRecord{Outcome: "deferred"}
			for _, f := range g {
				left[f.ID] = true
				r.Facts = append(r.Facts, f.ID)
				if isBatch[f.ID] {
					r.Fresh = append(r.Fresh, f.ID)
				}
			}
			deferred = append(deferred, r)
		}
		groups = groups[:packetGroups]
		batch = slices.DeleteFunc(slices.Clone(batch), func(f store.Fact) bool { return left[f.ID] })
		isBatch = freshSet(batch)
	}
	rec.Groups = len(groups)
	if list {
		_, err := io.WriteString(out, renderGroups(groups, isBatch))
		return rec, err
	}
	// Every fact without search queries gets them, the fresh ones first, then the facts the dream has seen and will
	// not see again, the newest first: an old base is expanded over a few dreams (#276).
	// A translation alone (#395) is not search queries: a fact that has only it still gets them.
	var bare []store.Fact
	for _, f := range batch {
		if len(extract.SearchQueries(f.Queries)) == 0 {
			bare = append(bare, f)
		}
	}
	for i := len(active) - 1; i >= 0 && len(bare) < packetExpand; i-- {
		if f := active[i]; !isFresh[f.ID] && len(extract.SearchQueries(f.Queries)) == 0 {
			bare = append(bare, f)
		}
	}
	if len(bare) > packetExpand {
		bare = bare[:packetExpand]
	}
	// Facts owed a translation get it a packet at a time, the newest first (#396). A fact another part of the packet
	// may change waits: a split retires it, and an expansion or a group action writes a version the translation would
	// race. A fresh fact the packet leaves for later is translated now: a base whose dream fell behind would otherwise
	// translate nothing until it caught up.
	busy := freshSet(bare)
	for _, g := range groups {
		for _, f := range g {
			busy[f.ID] = true
		}
	}
	var owed []store.Fact
	for i := len(active) - 1; i >= 0 && len(owed) < packetTranslate; i-- {
		if f := active[i]; !isBatch[f.ID] && !busy[f.ID] && owesTranslation(f) {
			owed = append(owed, f)
		}
	}
	p := dreamPacket{ID: start.UTC().Format(time.RFC3339Nano), Start: start, Seen: versions(active),
		Split: versions(batch), Expand: versions(bare), Translate: versions(owed), Deferred: deferred, Merge: cursor,
		Swept: swept}
	for _, f := range fresh {
		if !isBatch[f.ID] {
			if v, ok := seen[f.ID]; ok {
				p.Seen[f.ID] = v
			} else {
				delete(p.Seen, f.ID)
			}
		}
	}
	for _, g := range groups {
		p.Groups = append(p.Groups, versions(g))
	}
	if len(batch) == 0 && len(bare) == 0 && len(groups) == 0 && len(owed) == 0 {
		// Nothing to ask: the dream is done as of now.
		_, err := fmt.Fprintln(out, "memcue: nothing for the dream")
		return rec, errors.Join(err, writeDreamSeen(dir, p.Seen), writeMergeCursor(dir, cursor), markDream(dir, start))
	}
	if err := writeJSONFile(filepath.Join(dir, packetFile), p); err != nil {
		return rec, err
	}
	rec.Packet = p.ID
	text, err := renderPacket(p.ID, batch, bare, owed, groups, isBatch)
	if err != nil {
		return rec, err
	}
	_, err = io.WriteString(out, text)
	return rec, err
}

// renderPacket is the text the model reads: the instructions, the groups, and the facts to split, expand and translate.
func renderPacket(id string, split, expand, translate []store.Fact, groups [][]store.Fact, fresh map[string]bool) (string, error) {
	splitIn, err := renderSplitInput(split)
	if err != nil {
		return "", err
	}
	expandIn, err := renderSplitInput(expand)
	if err != nil {
		return "", err
	}
	translateIn, err := renderSplitInput(translate)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(extract.DreamInstructions(id))
	b.WriteString("\n## Данные: разделение\n\n```json\n" + splitIn + "\n```\n")
	b.WriteString("\n## Данные: поисковые запросы\n\n```json\n" + expandIn + "\n```\n")
	b.WriteString("\n## Данные: перевод\n\n```json\n" + translateIn + "\n```\n")
	b.WriteString("\n## Данные: группы\n\n")
	if len(groups) == 0 {
		b.WriteString("Групп нет.\n")
	}
	b.WriteString(renderGroups(groups, fresh))
	return b.String(), nil
}

// applyAnswer checks the model's answer against the packet it was given and carries it out the way a plan is. An
// answer to another packet is refused whole; an answer about a fact the packet did not give for that part, a second
// answer about one fact or a split into fewer than two parts is refused alone.
func applyAnswer(dir, path string, out io.Writer) (dreamRecord, error) {
	rec := dreamRecord{Time: time.Now().UTC().Format(time.RFC3339)}
	var p dreamPacket
	b, err := os.ReadFile(filepath.Join(dir, packetFile))
	if errors.Is(err, os.ErrNotExist) {
		return rec, errors.New("no dream packet: run memcue dream -prepare first")
	}
	if err != nil {
		return rec, err
	}
	if err := json.Unmarshal(b, &p); err != nil {
		return rec, fmt.Errorf("%s: %w", packetFile, err)
	}
	rec.Packet = p.ID
	b, err = os.ReadFile(path)
	if err != nil {
		return rec, err
	}
	var a dreamAnswer
	if err := json.Unmarshal(b, &a); err != nil {
		return rec, fmt.Errorf("answer %s: %w", path, err)
	}
	if a.Packet != p.ID {
		return rec, fmt.Errorf("answer %s is for packet %q, the current one is %q: run -prepare again", path, a.Packet, p.ID)
	}
	plan := dreamPlan{Start: p.Start, Seen: p.Seen, Merge: p.Merge}
	rec.Swept = p.Swept
	done := map[string]bool{}
	for _, sp := range a.Splits {
		var parts []string
		for _, part := range sp.Parts {
			if part = strings.TrimSpace(part); part != "" {
				parts = append(parts, part)
			}
		}
		v, ok := p.Split[sp.ID]
		switch {
		case !ok:
			rec.Refused = append(rec.Refused, "split "+sp.ID+": not a fact of the packet to split")
		case done[sp.ID]:
			rec.Refused = append(rec.Refused, "split "+sp.ID+": second split of the fact")
		case len(parts) < 2:
			rec.Refused = append(rec.Refused, fmt.Sprintf("split %s: %d part(s)", sp.ID, len(parts)))
		default:
			done[sp.ID] = true
			plan.Splits = append(plan.Splits, dreamPlanSplit{ID: sp.ID, Version: v, Parts: parts,
				Reason: strings.TrimSpace(sp.Reason)})
		}
	}
	expanded := map[string]bool{}
	for _, e := range a.Expansions {
		v, ok := p.Expand[e.ID]
		q := extract.CleanQueries(e.Queries)
		if !ok || expanded[e.ID] || done[e.ID] || len(q) == 0 {
			continue // a fact the packet did not give, a second answer, a split fact, or nothing to write
		}
		expanded[e.ID] = true
		plan.Expansions = append(plan.Expansions, dreamPlanExpansion{ID: e.ID, Version: v, Queries: q})
	}
	translated := map[string]bool{}
	for _, tr := range a.Translations {
		v, ok := p.Translate[tr.ID]
		t := strings.TrimSpace(tr.Translation)
		if !ok || translated[tr.ID] || t == "" {
			continue // a fact the packet did not give to translate, a second answer, or nothing to write
		}
		translated[tr.ID] = true
		plan.Translations = append(plan.Translations, dreamPlanTranslation{ID: tr.ID, Version: v, Translation: t})
	}
	groups := make([][]store.Fact, len(p.Groups))
	for i, g := range p.Groups {
		for _, id := range slices.Sorted(maps.Keys(g)) {
			groups[i] = append(groups[i], store.Fact{ID: id, Version: g[id]})
		}
	}
	for i := range a.Actions {
		// The store trims the text of a correction and refuses an empty one mid-apply; the check must see what the
		// store will.
		a.Actions[i].Content = strings.TrimSpace(a.Actions[i].Content)
	}
	ok, refused := checkActions(groups, a.Actions)
	rec.Refused = append(rec.Refused, refused...)
	plan.Groups = planGroups(groups, ok)
	rec.Fresh = len(p.Split)
	applied, err := applyPlan(dir, plan, rec, out)
	applied.Groups = len(p.Groups) // the groups the model reviewed, not only those it acted on
	applied.Grouped = groupRecords(p, a.Actions, applied.Actions)
	if err == nil {
		_, err = fmt.Fprintf(out, "dream: %d split, %d expanded, %d translated, %d retired or corrected, %d promoted, "+
			"%d refused\n", len(applied.Splits), applied.Expanded, applied.Translated, len(applied.Actions),
			len(applied.Promoted), len(applied.Refused))
	}
	if err == nil {
		err = os.Remove(filepath.Join(dir, packetFile))
	}
	return applied, err
}

// groupRecords says what came of each group of the packet: the ids of its facts, which of them were fresh, and what the
// answer and the checks did with it.
func groupRecords(p dreamPacket, proposed, applied []extract.Action) []dreamGroupRecord {
	out := make([]dreamGroupRecord, 0, len(p.Groups)+len(p.Deferred))
	for _, g := range p.Groups {
		r := dreamGroupRecord{Facts: slices.Sorted(maps.Keys(g)), Outcome: "kept"}
		for _, id := range r.Facts {
			if _, ok := p.Split[id]; ok {
				r.Fresh = append(r.Fresh, id)
			}
		}
		if slices.ContainsFunc(proposed, func(a extract.Action) bool { _, ok := g[a.ID]; return ok }) {
			r.Outcome = "refused"
		}
		for _, a := range applied {
			if _, ok := g[a.ID]; ok {
				r.Acted = append(r.Acted, a.ID)
				r.Outcome = "acted"
			}
		}
		out = append(out, r)
	}
	return append(out, p.Deferred...)
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

// applyPlan carries out a checked answer. A group whose facts changed since the packet (a new version, or retired) is
// refused whole, since its actions were decided on the old text; the rest apply, and the dream is marked as of the
// packet, with what it saw. rec carries what the checks before already refused.
func applyPlan(dir string, plan dreamPlan, rec dreamRecord, out io.Writer) (dreamRecord, error) {
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
		q := extract.CleanQueries(e.Queries)
		if cur, err := s.Get(e.ID); err == nil && cur.Version == e.Version {
			// The queries are written whole: the translation the fact already has stays with them.
			tr, _ := extract.Translation(cur.Queries)
			q = extract.WithTranslation(q, tr)
		}
		f, err := s.SetQueries(e.ID, e.Version, q)
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
	// Translations write only the translation query; the rest of the queries stay. The packet gives them facts no
	// other part touches, so a changed fact here was changed since the packet, and its translation may describe the
	// old text.
	for _, tr := range plan.Translations {
		cur, err := s.Get(tr.ID)
		switch {
		case errors.Is(err, store.ErrNotFound):
			rec.Refused = append(rec.Refused, "translate "+tr.ID+": "+err.Error())
			continue
		case err != nil:
			return rec, err
		}
		f, err := s.SetQueries(tr.ID, tr.Version, extract.WithTranslation(cur.Queries, tr.Translation))
		switch {
		case errors.Is(err, store.ErrChanged) || errors.Is(err, store.ErrRetired):
			rec.Refused = append(rec.Refused, "translate "+tr.ID+": "+err.Error())
			continue
		case err != nil:
			return rec, err
		}
		// A fact the dream counts as seen stays seen at its new version; a fresh one the packet left for later stays
		// fresh, so the next packet still reviews it.
		if saw[tr.ID] == tr.Version {
			saw[tr.ID] = f.Version
		}
		rec.Translated++
	}
	if rec.Translated > 0 {
		if _, err := fmt.Fprintf(out, "translated %d facts\n", rec.Translated); err != nil {
			return rec, err
		}
	}
	// The snapshot must name every fact of the plan, including provisional and retired ones: store.Active leaves
	// provisional facts out, and a group with a provisional neighbour would otherwise look changed and be refused
	// although nothing happened since the packet.
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
				rec.Refused = append(rec.Refused, a.Op+" "+a.ID+": group changed since the packet ("+changed+")")
				continue
			}
			f, err := apply(s, a)
			if err != nil {
				return rec, err
			}
			saw[f.ID] = f.Version
			if a.Op == "correct" && f.Status == store.Active && len(f.Queries) > 0 {
				rec.Expanded++
			}
			rec.Actions = append(rec.Actions, a)
			if _, err := fmt.Fprintf(out, "%s %s: %s\n", a.Op, a.ID, a.Reason); err != nil {
				return rec, err
			}
			if err := promote(s, a, saw, &rec, out); err != nil {
				return rec, err
			}
		}
	}
	for _, r := range rec.Refused {
		if _, err := fmt.Fprintf(out, "refused %s\n", r); err != nil {
			return rec, err
		}
	}
	return rec, errors.Join(writeDreamSeen(dir, saw), writeMergeCursor(dir, plan.Merge), markDream(dir, plan.Start))
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
	gr := newGrouper(active)
	for _, f := range fresh {
		if err := gr.add(s, f); err != nil {
			return nil, err
		}
	}
	return gr.groups(), nil
}

// grouper joins facts with their neighbours one fact at a time, so the sweep can stop once it has made enough groups.
type grouper struct {
	byID   map[string]store.Fact
	parent map[string]string
	size   map[string]int
}

func newGrouper(active []store.Fact) *grouper {
	g := &grouper{byID: map[string]store.Fact{}, parent: map[string]string{}, size: map[string]int{}}
	for _, f := range active {
		g.byID[f.ID] = f
	}
	return g
}

func (g *grouper) find(id string) string {
	if _, ok := g.parent[id]; !ok {
		g.parent[id], g.size[id] = id, 1
	}
	if g.parent[id] != id {
		g.parent[id] = g.find(g.parent[id])
	}
	return g.parent[id]
}

func (g *grouper) union(a, b string) {
	ra, rb := g.find(a), g.find(b)
	if ra == rb || g.size[ra]+g.size[rb] > maxGroup {
		return
	}
	g.parent[rb] = ra
	g.size[ra] += g.size[rb]
}

// add joins f with its neighbours.
func (g *grouper) add(s *store.Store, f store.Fact) error {
	p := groupNeighbours
	p.Seen = map[string]bool{f.ID: true}
	hits, err := recall.Recall(s, f.Content+" "+strings.Join(f.Keywords, " "), p)
	if err != nil {
		return err
	}
	for _, h := range hits {
		// Recall returns provisional facts too: they are indexed and active, but store.Active leaves them out.
		// Take the hit's own fact, so a provisional neighbour is a real group member rather than a zero fact.
		if _, ok := g.byID[h.ID]; !ok {
			if h.Review != store.Provisional {
				continue
			}
			g.byID[h.ID] = h.Fact
		}
		g.union(f.ID, h.ID)
	}
	return nil
}

// groupsWithout counts the groups with no fact of these.
func (g *grouper) groupsWithout(these map[string]bool) int {
	n := 0
	for _, m := range g.members() {
		if len(m) > 1 && !slices.ContainsFunc(m, func(f store.Fact) bool { return these[f.ID] }) {
			n++
		}
	}
	return n
}

func (g *grouper) members() map[string][]store.Fact {
	members := map[string][]store.Fact{}
	for id := range g.parent {
		r := g.find(id)
		members[r] = append(members[r], g.byID[id])
	}
	return members
}

// groups are the groups of two facts or more, each in id order, ordered by their first fact.
func (g *grouper) groups() [][]store.Fact {
	var groups [][]store.Fact
	for _, m := range g.members() {
		if len(m) < 2 {
			continue
		}
		slices.SortFunc(m, func(a, b store.Fact) int { return strings.Compare(a.ID, b.ID) })
		groups = append(groups, m)
	}
	slices.SortFunc(groups, func(a, b []store.Fact) int { return strings.Compare(a[0].ID, b[0].ID) })
	return groups
}

// sweepFacts are the facts the merge sweep takes next: the active facts that are not fresh, in id order from the
// first one after cursor, then from the oldest again. store.Active returns them in id order, oldest first.
func sweepFacts(active []store.Fact, fresh map[string]bool, cursor string) []store.Fact {
	var old []store.Fact
	for _, f := range active {
		if !fresh[f.ID] {
			old = append(old, f)
		}
	}
	i, _ := slices.BinarySearchFunc(old, cursor, func(f store.Fact, id string) int {
		if f.ID <= id {
			return -1
		}
		return 1
	})
	return append(old[i:len(old):len(old)], old[:i]...)
}

// readMergeCursor reads the id of the last fact the merge sweep took; no file is the start of the base.
func readMergeCursor(dir string) (string, error) {
	b, err := os.ReadFile(filepath.Join(dir, "dream.merge"))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	return strings.TrimSpace(string(b)), err
}

func writeMergeCursor(dir, id string) error {
	if id == "" {
		return nil
	}
	return os.WriteFile(filepath.Join(dir, "dream.merge"), []byte(id+"\n"), 0o600)
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
// this, not the model, which only said that one fact covers the other. The
// survivor is read now, not as the group showed it: an earlier action of the batch may have corrected it.
func promote(s *store.Store, a extract.Action, saw map[string]int, rec *dreamRecord, out io.Writer) error {
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
	f, err := s.Relabel(by.ID, by.Version, by.Kind, "global", by.Expires)
	if err != nil {
		return err
	}
	saw[f.ID] = f.Version
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

// apply makes one checked action and returns the version it wrote. A correction drops the queries of the old text;
// the ones the model wrote for the new text are set in a version of their own, and without them the next packet asks
// for them (#297).
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
	f, err := s.Correct(a.ID, a.Content, keywords)
	if err != nil {
		return f, err
	}
	if q := extract.CleanQueries(a.Queries); len(q) > 0 {
		return s.SetQueries(f.ID, f.Version, q)
	}
	return f, nil
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
