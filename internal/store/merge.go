package store

import (
	"database/sql"
	"fmt"
	"io"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// MergeStats says what Merge took from the other copy.
type MergeStats struct {
	Added    int // versions of the other copy this one lacked
	Diverged int // facts changed on both sides since they last agreed
}

// Merge takes the facts of the copy at otherPath into s, the way git's merge driver needs: the result is the union
// of the versions of both. Where both copies added versions to the same fact since they last agreed, the versions
// after the common ones are ordered by creation time (then author, content and the rest of the version, so every
// machine gets the same order; see mergedBefore) and numbered anew; the latest of them is current, except that a
// correction wins over a retirement made without knowing it (see correctionLast). Nothing is lost: a correction made on one machine and a retirement made on
// another both stay in the history.
//
// Vectors are not merged: nothing reads them, and Open deletes the ones an older binary stored (#260).
func (s *Store) Merge(otherPath string) (MergeStats, error) {
	var st MergeStats
	other, err := Open(otherPath)
	if err != nil {
		return st, fmt.Errorf("open %s: %w", otherPath, err)
	}
	theirs, err := other.Rows()
	if err != nil {
		_ = other.Close()
		return st, err
	}
	_ = other.Close()
	ours, err := s.Rows()
	if err != nil {
		return st, err
	}
	o, t := byID(ours), byID(theirs)

	tx, err := s.db.Begin()
	if err != nil {
		return st, err
	}
	defer func() { _ = tx.Rollback() }()
	var changed []string
	for id, tv := range t {
		h := MergeHistories(o[id], tv)
		if !h.Changed() {
			continue
		}
		st.Added += h.Added
		if h.Diverged {
			st.Diverged++
		}
		if err := replaceTail(tx, id, h); err != nil {
			return st, err
		}
		changed = append(changed, id)
	}
	if err := tx.Commit(); err != nil {
		return st, err
	}
	if err := s.textRewritten(changed); err != nil {
		return st, err
	}
	return st, s.Rebuild()
}

// MergedHistory is the merge of two histories of one fact.
type MergedHistory struct {
	Versions []Fact // the merged history, numbered from 1
	Common   int    // how many leading versions both sides shared; Versions after them are new or renumbered
	Added    int    // versions of the other side this one lacked
	Diverged bool   // both sides added versions since they last agreed
	// Reordered: the other side lacked nothing new, but this side's versions after the common ones change order; with
	// Added 0 and Reordered false this side stays as it is.
	Reordered bool
}

// Changed says whether the merge changes this side's history.
func (h MergedHistory) Changed() bool { return h.Added > 0 || h.Reordered }

// MergeHistories merges the versions of one fact as this side (ours) and the other side (theirs) hold them, each in
// version order: the rule of Merge, for the base and for the text history alike.
func MergeHistories(ours, theirs []Fact) MergedHistory {
	common := 0
	for common < len(ours) && common < len(theirs) && same(ours[common], theirs[common]) {
		common++
	}
	oTail := ours[common:]
	// A version both tails hold is one write that the two sides number differently: a side that has not pulled an
	// earlier merge keeps it where it wrote it, the merged side after the version merged before it (#540).
	var tTail []Fact
	for _, f := range theirs[common:] {
		if !holds(oTail, f) {
			tTail = append(tTail, f)
		}
	}
	h := MergedHistory{Versions: ours, Common: common, Added: len(tTail), Diverged: len(oTail) > 0 && len(tTail) > 0}
	if len(theirs) == common {
		return h
	}
	tail := append(append([]Fact{}, oTail...), tTail...)
	sort.SliceStable(tail, func(i, j int) bool { return mergedBefore(tail[i], tail[j]) })
	base := ""
	if common > 0 {
		base = ours[common-1].Content
	}
	tail = correctionLast(tail, base, oTail, theirs[common:])
	if len(tTail) == 0 && slices.EqualFunc(tail, oTail, same) {
		return h
	}
	// Both sides hold the same versions in another order, as two machines merged them before the order was total: the
	// order of the rule replaces this side's, so every side ends with the same history.
	h.Reordered = len(tTail) == 0
	h.Versions = append(append([]Fact{}, ours[:common]...), tail...)
	for i := common; i < len(h.Versions); i++ {
		h.Versions[i].Version = i + 1
	}
	return h
}

// mergedBefore orders the versions after the common ones, the same on every side: by creation time, author and
// content, and between versions that agree on all three, as a label written later (queries, a relabelled source)
// keeps the creation time of the version it labels, the fuller version last, so the current one keeps every label
// either side wrote: a version without a source before one with it, then fewer queries before more, then by the
// written line, so no two writes tie.
func mergedBefore(a, b Fact) bool {
	if !a.Created.Equal(b.Created) {
		return a.Created.Before(b.Created)
	}
	if a.Author != b.Author {
		return a.Author < b.Author
	}
	if a.Content != b.Content {
		return a.Content < b.Content
	}
	if (a.Source == "") != (b.Source == "") {
		return a.Source == ""
	}
	if len(a.Queries) != len(b.Queries) {
		return len(a.Queries) < len(b.Queries)
	}
	a.Version, b.Version = 0, 0
	la, _ := encodeLine(a)
	lb, _ := encodeLine(b)
	return string(la) < string(lb)
}

// replaceTail rewrites the versions of id after the common ones with the merged ones.
func replaceTail(tx *sql.Tx, id string, h MergedHistory) error {
	if _, err := tx.Exec(`DELETE FROM facts WHERE id = ? AND version > ?`, id, h.Common); err != nil {
		return err
	}
	for _, f := range h.Versions[h.Common:] {
		if err := insert(tx, f); err != nil {
			return err
		}
	}
	return nil
}

// correctionLast puts after a retirement every correction its writer never saw, so the fact stays current with the
// corrected text whatever the clocks of the two machines say (#544): a retirement judged the fact outdated, a
// correction brought new data, and losing the new data is worse than an extra active fact. tail is the merged tail in
// creation order, a and b the two sides' tails, base the text of the last version both sides share. When tail ends
// with a retirement, the active versions before it that no side holds before it were never seen by its writer: a side
// that holds one before the retirement merged it and retired the fact afterwards. They go last when one of them
// corrects the text, that is changes it from the version before it on its side to something else than the retired
// text; a change of keywords or labels only, or a reopen, leaves the retirement current. The test looks at the versions
// themselves, not at which side ends how, so a side that merges an earlier merge's result gets that result again (#549).
func correctionLast(tail []Fact, base string, a, b []Fact) []Fact {
	if len(tail) == 0 || tail[len(tail)-1].Status != Retired {
		return tail
	}
	r := tail[len(tail)-1]
	index := func(side []Fact, f Fact) int { return slices.IndexFunc(side, func(g Fact) bool { return same(f, g) }) }
	before := func(side []Fact, f Fact) bool {
		fi, ri := index(side, f), index(side, r)
		return fi >= 0 && ri >= 0 && fi < ri
	}
	corrects := func(f Fact) bool {
		if f.Content == r.Content {
			return false
		}
		for _, side := range [][]Fact{a, b} {
			if i := index(side, f); i == 0 && f.Content != base || i > 0 && f.Content != side[i-1].Content {
				return true
			}
		}
		return false
	}
	var unseen []Fact
	correction := false
	for _, f := range tail[:len(tail)-1] {
		if f.Status == Active && !before(a, f) && !before(b, f) {
			unseen = append(unseen, f)
			correction = correction || corrects(f)
		}
	}
	if !correction {
		return tail
	}
	out := make([]Fact, 0, len(tail))
	for _, f := range tail {
		if !holds(unseen, f) {
			out = append(out, f)
		}
	}
	return append(out, unseen...)
}

// holds reports whether vs has a version that is the same write as f.
func holds(vs []Fact, f Fact) bool {
	return slices.ContainsFunc(vs, func(g Fact) bool { return same(f, g) })
}

func byID(rows []Fact) map[string][]Fact {
	m := map[string][]Fact{}
	for _, f := range rows { // Rows orders by id, version
		m[f.ID] = append(m[f.ID], f)
	}
	return m
}

func same(a, b Fact) bool {
	return a.Content == b.Content && strings.Join(a.Keywords, ", ") == strings.Join(b.Keywords, ", ") &&
		a.Origin == b.Origin && a.Created.Equal(b.Created) && a.Status == b.Status && a.Author == b.Author &&
		a.Expires == b.Expires && a.Meta == b.Meta && strings.Join(a.Queries, ", ") == strings.Join(b.Queries, ", ")
}

// Dump writes every version of every fact, one per line, for git diff.
func (s *Store) Dump(w io.Writer) error {
	rows, err := s.Rows()
	if err != nil {
		return err
	}
	for _, f := range rows {
		created := f.Created.Format(time.RFC3339)
		if f.Expires != "" {
			created += " expires=" + f.Expires
		}
		for _, kv := range [][2]string{{"kind", f.Kind}, {"scope", f.Scope}, {"source", f.Source}, {"review", f.Review}} {
			if kv[1] != "" {
				created += " " + kv[0] + "=" + kv[1]
			}
		}
		if f.PromptVersion != 0 {
			created += " prompt=" + strconv.Itoa(f.PromptVersion)
		}
		if len(f.Queries) > 0 {
			created += " queries=[" + strings.Join(f.Queries, ", ") + "]"
		}
		if _, err := fmt.Fprintf(w, "%s v%d %s %s %s [%s] %s\n", f.ID, f.Version, f.Status,
			created, f.Origin, strings.Join(f.Keywords, ", "),
			strings.Join(strings.Fields(f.Content), " ")); err != nil {
			return err
		}
	}
	return nil
}
