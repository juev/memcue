package store

// Add stores a new fact as version 1: the short form the tests of this package write their fixtures with.
func (s *Store) Add(content string, keywords []string, origin string) (Fact, error) {
	return s.AddWith(content, keywords, origin, "", Meta{})
}

// AddExpiring is Add with the last day the fact is worth recalling ("YYYY-MM-DD"; empty never expires).
func (s *Store) AddExpiring(content string, keywords []string, origin, expires string) (Fact, error) {
	return s.AddWith(content, keywords, origin, expires, Meta{})
}
