package store

import (
	"fmt"
	"io"
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
// order) and numbered anew; the latest of them is current. Nothing is lost: a correction made on one machine and a
// retirement made on another both stay in the history.
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
		oTail, tTail := ov[common:], tv[common:]
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
