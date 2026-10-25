package recall

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/juev/memcue/internal/store"
	"github.com/juev/memcue/internal/text"
	"github.com/juev/memcue/internal/tier"
)

// MinScore is the default floor of Search (#407): a fact whose BM25 is below this share of the best fact's does not
// fit the query, and n is only the most facts Search returns. On 35 queries agents wrote, it kept a useful fact for
// every query that had one in the top five and cut the unrelated facts from 19 to 12, the facts per search from 5 to
// 4.2; a query whose best fact stands far above the rest gets that fact alone.
const MinScore = 0.6

// Query is what the agent asks Search for (#409): the words, how many facts and how close to the best, and where and
// what to look in. Its zero value is no search; Defaults gives the measured defaults.
type Query struct {
	Text     string
	N        int     // the most facts to return
	MinScore float64 // leaves out a fact whose BM25 is below this share of the best one; 0 keeps every fact up to N
	Project  []string
	Host     string
	// In names where to look: "project" (facts of the session's repository, by any of its keys), "global", "host"
	// (this machine), "all", or a scope such as "repo:KEY", "host:NAME" or "service:NAME". Several are a union. Empty
	// is everything but facts bound to another repository: on agents' queries that kept 116 of 129 useful facts
	// and dropped 37 of 63 unrelated ones, where the session's project alone kept 41 useful facts.
	In       []string
	Kinds    []string // only facts of these kinds (rule, decision, pitfall, ...)
	Since    string   // only facts learnt on or after this day, "YYYY-MM-DD"
	Expired  bool     // keep facts whose expiry has passed; by default they are left out
	AllWords bool     // a fact must hold every word of the query
	Today    string   // the day expiry is judged by; empty is today
}

// Kinds are the kinds a fact can have, as the extraction writes them.
var Kinds = []string{"rule", "decision", "pitfall", "environment", "event", "procedure", "repo", "other"}

// Filter turns q into the store's filter, or says which part of q is wrong.
func (q Query) Filter() (store.Filter, error) {
	f := store.Filter{AllTerms: q.AllWords, Kinds: q.Kinds}
	for _, k := range q.Kinds {
		if !slices.Contains(Kinds, k) {
			return f, fmt.Errorf("unknown kind %q: one of %s", k, strings.Join(Kinds, ", "))
		}
	}
	if q.Since != "" {
		// A day of the user's calendar, as for expiry; facts carry their creation time in UTC.
		day, err := time.ParseInLocation(time.DateOnly, q.Since, time.Local)
		if err != nil {
			return f, fmt.Errorf("since %q: want YYYY-MM-DD", q.Since)
		}
		f.Since = day.UTC().Format("2006-01-02T15:04:05")
	}
	if !q.Expired {
		f.Today = q.Today
		if f.Today == "" {
			f.Today = store.Today()
		}
	}
	if len(q.In) == 0 {
		f.OtherRepos, f.Repos = true, q.Project
		return f, nil
	}
	for _, in := range q.In {
		switch in = strings.TrimSpace(in); {
		case in == "all":
			f.Scopes, f.Origins = nil, nil
			return f, nil
		case in == "project":
			for _, k := range q.Project {
				f.Scopes = append(f.Scopes, "repo:"+k)
				f.Origins = append(f.Origins, k)
			}
			if len(q.Project) == 0 {
				// No project: nothing is the project's, rather than everything.
				f.Scopes = append(f.Scopes, "repo:")
			}
		case in == "global":
			f.Scopes = append(f.Scopes, "global")
			f.Origins = append(f.Origins, tier.Global)
		case in == "host":
			f.Scopes = append(f.Scopes, "host:"+q.Host)
		case strings.HasPrefix(in, "repo:") && len(in) > len("repo:"):
			f.Scopes = append(f.Scopes, in)
			f.Origins = append(f.Origins, strings.TrimPrefix(in, "repo:"))
		case strings.Contains(in, ":") && !strings.HasSuffix(in, ":"):
			f.Scopes = append(f.Scopes, in)
		default:
			return f, fmt.Errorf("unknown place %q: project, global, host, all, or a scope such as repo:KEY", in)
		}
	}
	return f, nil
}

// Search finds the facts for a query the agent wrote itself (#361): FTS5 over the query's terms in BM25 order, with
// none of the gates of Recall, because the agent reads what it asked for. It returns at most q.N facts and leaves out
// those whose BM25 is below q.MinScore of the best one (#407); the filter of q runs before either (#409). Ties of
// equal BM25, such as the same text stored twice, go to a fact of this host, then one learnt in the session's project,
// then a global one, then the newer fact; the order never drops a fact.
func Search(s *store.Store, q Query) ([]store.Hit, error) {
	if q.N <= 0 {
		return nil, nil
	}
	f, err := q.Filter()
	if err != nil {
		return nil, err
	}
	// Twice the room: the database cuts a run of equal scores in its own order, before the tie order could see it.
	hits, err := s.SearchFiltered(text.Terms(q.Text), 2*q.N, f)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].BM25 != hits[j].BM25 {
			return hits[i].BM25 < hits[j].BM25
		}
		if pi, pj := place(hits[i].Fact, q.Project, q.Host), place(hits[j].Fact, q.Project, q.Host); pi != pj {
			return pi < pj
		}
		return newer(hits[i].Fact, hits[j].Fact)
	})
	// BM25 of FTS5 is negative, the best fact the lowest: a fact keeps its place while its score is at least
	// MinScore of the best one.
	if q.MinScore > 0 && len(hits) > 0 && hits[0].BM25 < 0 {
		best := hits[0].BM25
		kept := hits[:0]
		for _, h := range hits {
			if h.BM25/best >= q.MinScore {
				kept = append(kept, h)
			}
		}
		hits = kept
	}
	if len(hits) > q.N {
		hits = hits[:q.N]
	}
	return hits, nil
}
