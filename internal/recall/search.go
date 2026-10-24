package recall

import (
	"sort"

	"github.com/juev/memcue/internal/store"
	"github.com/juev/memcue/internal/text"
)

// Search finds the facts for a query the agent wrote itself (#361): FTS5 over the query's terms in BM25 order, with
// none of the gates of Recall, because the agent reads what it asked for. Ties of equal BM25, such as the same text
// stored twice, go to a fact of this host, then one learnt in the session's project, then a global one, then the
// newer fact; the order never drops a fact.
func Search(s *store.Store, query string, project []string, host string, n int) ([]store.Hit, error) {
	if n <= 0 {
		return nil, nil
	}
	// Twice the room: the database cuts a run of equal scores in its own order, before the tie order could see it.
	hits, err := s.Search(text.Terms(query), 2*n)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].BM25 != hits[j].BM25 {
			return hits[i].BM25 < hits[j].BM25
		}
		if pi, pj := place(hits[i].Fact, project, host), place(hits[j].Fact, project, host); pi != pj {
			return pi < pj
		}
		return newer(hits[i].Fact, hits[j].Fact)
	})
	if len(hits) > n {
		hits = hits[:n]
	}
	return hits, nil
}
