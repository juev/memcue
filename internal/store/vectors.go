package store

// dropVectors deletes the vectors an older memcue stored and compacts the file, once: nothing reads them since #232,
// and they took about 40 % of a base that every sync carries (#260). The table stays, empty, so the schema is unchanged
// and an older binary still opens the base. Compacting is best effort: a busy file keeps its free pages, which later
// writes reuse.
func (s *Store) dropVectors() error {
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM fact_vectors`).Scan(&n); err != nil || n == 0 {
		return err
	}
	if _, err := s.db.Exec(`DELETE FROM fact_vectors`); err != nil {
		return err
	}
	_, _ = s.db.Exec(`VACUUM`)
	return nil
}
