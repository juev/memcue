//go:build research

package store

import (
	"context"
	"errors"
)

// Research helpers for the daemon prototype of #703. They keep a local SQLite base as a replica of another source:
// a replica holds the latest version of each fact under its own id and version, which AddBatch cannot do. Built
// only with -tags research; not part of memcue.

// ResearchPut makes each fact the only stored version of its id and refreshes the search index and its stamp.
func ResearchPut(s *Store, facts []Fact) error {
	if s.db == nil {
		return errors.New("research replica must be a local base")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, f := range facts {
		if _, err := tx.Exec(`DELETE FROM facts WHERE id = ?`, f.ID); err != nil {
			return err
		}
		if err := insert(tx, f); err != nil {
			return err
		}
		if err := reindex(tx, f.ID); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return s.ensureIndex()
}

// ResearchVersions is the stored version of every id in a local base, retired ones included.
func ResearchVersions(s *Store) (map[string]int, error) {
	if s.db == nil {
		return nil, errors.New("research replica must be a local base")
	}
	rows, err := s.db.Query(`SELECT id, max(version) FROM facts GROUP BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]int{}
	for rows.Next() {
		var id string
		var v int
		if err := rows.Scan(&id, &v); err != nil {
			return nil, err
		}
		out[id] = v
	}
	return out, rows.Err()
}

// ResearchDecode turns a PostgreSQL payload into a fact, as the remote reads do.
func ResearchDecode(payload []byte) (Fact, error) {
	var f Fact
	err := unmarshalFact(context.Background(), payload, &f)
	return f, err
}
