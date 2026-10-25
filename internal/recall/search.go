package recall

import (
	"sort"

	"github.com/juev/memcue/internal/store"
	"github.com/juev/memcue/internal/text"
)

// MinScore is the default floor of Search (#407): a fact whose BM25 is below this share of the best fact's does not
// fit the query, and n is only the most facts Search returns. On 35 queries agents wrote, it kept a useful fact for
// every query that had one in the top five and cut the unrelated facts from 19 to 12, the facts per search from 5 to
// 4.2; a query whose best fact stands far above the rest gets that fact alone.
const MinScore = 0.6

// Search finds the facts for a query the agent wrote itself (#361): FTS5 over the query's terms in BM25 order, with
// none of the gates of Recall, because the agent reads what it asked for. It returns at most n facts and leaves out
// those whose BM25 is below minScore of the best one (#407); minScore 0 keeps them. Ties of equal BM25, such as the
// same text stored twice, go to a fact of this host, then one learnt in the session's project, then a global one, then
// the newer fact; the order never drops a fact.
func Search(s *store.Store, query string, project []string, host string, n int, minScore float64) ([]store.Hit, error) {
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
	// BM25 of FTS5 is negative, the best fact the lowest: a fact keeps its place while its score is at least
	// minScore of the best one.
	if minScore > 0 && len(hits) > 0 && hits[0].BM25 < 0 {
		best := hits[0].BM25
		kept := hits[:0]
		for _, h := range hits {
			if h.BM25/best >= minScore {
				kept = append(kept, h)
			}
		}
		hits = kept
	}
	if len(hits) > n {
		hits = hits[:n]
	}
	return hits, nil
}
