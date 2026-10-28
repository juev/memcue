package store

import (
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
// after the common ones are ordered by creation time (then author and content, so every machine gets the same
// order) and numbered anew; the latest of them is current, except that a correction wins over a retirement made
// without knowing it (see correctionLast). Nothing is lost: a correction made on one machine and a retirement made on
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
	for id, tv := range t {
		ov := o[id]
		common := 0
		for common < len(ov) && common < len(tv) && same(ov[common], tv[common]) {
			common++
		}
		oTail := ov[common:]
		// A version both tails hold is one write that the two sides number differently: a side that has not pulled
		// an earlier merge keeps it where it wrote it, the merged side after the version merged before it (#540).
		var tTail []Fact
		for _, f := range tv[common:] {
			if !holds(oTail, f) {
				tTail = append(tTail, f)
			}
		}
		if len(tTail) == 0 {
			continue
		}
		st.Added += len(tTail)
		if len(oTail) > 0 {
			st.Diverged++
		}
		tail := append(append([]Fact{}, oTail...), tTail...)
		sort.SliceStable(tail, func(i, j int) bool {
			a, b := tail[i], tail[j]
			if !a.Created.Equal(b.Created) {
				return a.Created.Before(b.Created)
			}
			if a.Author != b.Author {
				return a.Author < b.Author
			}
			return a.Content < b.Content
		})
		tail = correctionLast(tail, oTail, tv[common:])
		if _, err := tx.Exec(`DELETE FROM facts WHERE id = ? AND version > ?`, id, common); err != nil {
			return st, err
		}
		for i, f := range tail {
			f.Version = common + i + 1
			if err := insert(tx, f); err != nil {
				return st, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return st, err
	}
	return st, s.Rebuild()
}

// correctionLast puts the versions of a correction the retiring side never saw after everything else, so the fact
// stays current with the corrected text whatever the clocks of the two machines say (#544): a retirement judged the
// fact outdated, a correction brought new data, and losing the new data is worse than an extra active fact. It applies
// when one side's tail ends retired, the other's ends active with another text, and neither side holds the other's
// last version; a change of metadata only, or a retirement made after merging the correction, keeps tail as it is.
// a and b are the two sides' tails, tail the merged one in creation order.
func correctionLast(tail, a, b []Fact) []Fact {
	if len(a) == 0 || len(b) == 0 {
		return tail
	}
	retiring, correcting := a, b
	if retiring[len(retiring)-1].Status != Retired {
		retiring, correcting = b, a
	}
	r, c := retiring[len(retiring)-1], correcting[len(correcting)-1]
	if r.Status != Retired || c.Status != Active || c.Content == r.Content || holds(retiring, c) || holds(correcting, r) {
		return tail
	}
	var unseen []Fact
	for _, f := range correcting {
		if !holds(retiring, f) {
			unseen = append(unseen, f)
		}
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
