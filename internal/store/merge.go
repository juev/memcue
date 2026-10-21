package store

import (
	"database/sql"
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
// Vectors are derived, but copying the ones whose text hash still matches the merged content saves a full embed pass
// after a sync. A vector made from another version is not copied: the reader would skip it anyway, and MissingVectors
// keeps the fact in the next embed run.
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
	vectors, err := other.vectorRows()
	_ = other.Close()
	if err != nil {
		return st, err
	}
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
	if err := copyMatchingVectors(tx, vectors); err != nil {
		return st, err
	}
	if err := tx.Commit(); err != nil {
		return st, err
	}
	return st, s.Rebuild()
}

// vectorRow is one stored vector as Merge reads it from the other copy.
type vectorRow struct {
	id, tag, hash string
	dim           int
	vec           []byte
}

// vectorRows reads every vector of the base, without the fact join: Merge decides which of them still match.
func (s *Store) vectorRows() ([]vectorRow, error) {
	rows, err := s.db.Query(`SELECT id, tag, dim, text_hash, vec FROM fact_vectors`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []vectorRow
	for rows.Next() {
		var v vectorRow
		if err := rows.Scan(&v.id, &v.tag, &v.dim, &v.hash, &v.vec); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// copyMatchingVectors copies the vectors whose text hash matches the merged current content, replacing a stale vector
// of the same id and tag. A vector of another version is left behind; the reader skips it by hash anyway.
func copyMatchingVectors(tx *sql.Tx, vectors []vectorRow) error {
	if len(vectors) == 0 {
		return nil
	}
	rows, err := tx.Query(`SELECT f.id, f.content FROM facts f
		WHERE f.version = (SELECT max(version) FROM facts WHERE id = f.id)`)
	if err != nil {
		return err
	}
	current := make(map[string]string, len(vectors))
	for rows.Next() {
		var id, content string
		if err := rows.Scan(&id, &content); err != nil {
			_ = rows.Close()
			return err
		}
		current[id] = TextHash(content)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, v := range vectors {
		if current[v.id] != v.hash {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO fact_vectors (id, tag, dim, text_hash, vec) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(id, tag) DO UPDATE SET dim = excluded.dim, text_hash = excluded.text_hash, vec = excluded.vec
			WHERE fact_vectors.text_hash != excluded.text_hash`, v.id, v.tag, v.dim, v.hash, v.vec); err != nil {
			return err
		}
	}
	return nil
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
		a.Expires == b.Expires && a.Meta == b.Meta
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
		if _, err := fmt.Fprintf(w, "%s v%d %s %s %s [%s] %s\n", f.ID, f.Version, f.Status,
			created, f.Origin, strings.Join(f.Keywords, ", "),
			strings.Join(strings.Fields(f.Content), " ")); err != nil {
			return err
		}
	}
	return nil
}
