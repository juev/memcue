package main

import (
	"fmt"
	"strings"

	"github.com/juev/memcue/internal/extract"
	"github.com/juev/memcue/internal/store"
	"github.com/juev/memcue/internal/tier"
)

// projectOf is the project a fact holds in: its repo: or dir: scope, or, with no scope, the project its origin names.
// A global fact, one of a host or a service, and one learnt nowhere in particular (the Claude config directory, an
// empty origin) have none. A repo: key is lowercased as tier.Normalize writes it; a dir: path keeps its case.
func projectOf(f store.Fact) string {
	low := strings.ToLower(f.Scope)
	switch {
	case strings.HasPrefix(low, "repo:"):
		return low
	case strings.HasPrefix(low, tier.DirPrefix):
		return tier.DirPrefix + f.Scope[len(tier.DirPrefix):]
	case f.Scope != "":
		return ""
	}
	if k := originKey(f.Origin); k != "" && k != tier.Global {
		return projectScope(k)
	}
	return ""
}

// projectScope is tier.Scope with a repo: key lowercased, the form projectOf compares.
func projectScope(key string) string {
	sc := tier.Scope(key)
	if strings.HasPrefix(sc, "repo:") {
		return strings.ToLower(sc)
	}
	return sc
}

// projectAliases says whether two project scopes name one repository (#607): every remote key of one clone (a mirror,
// a fork's upstream), the name memcue gave the clone before #343 when no other clone has it, and the dir: path of a
// checkout that has a remote now. It learns them from this machine's clones, the way migrate -repos does, once and
// only when a dream meets two different scopes; a name it cannot place is a project of its own.
type projectAliases struct {
	parent map[string]string
}

// same reports whether the project scopes a and b name one repository.
func (p *projectAliases) same(a, b string) bool {
	if a == b {
		return true
	}
	if p.parent == nil {
		p.load()
	}
	p.dir(a)
	p.dir(b)
	return p.find(a) == p.find(b)
}

func (p *projectAliases) load() {
	p.parent = map[string]string{}
	legacy := map[string]map[string]bool{} // an old name → the clones that had it, by their first key
	for _, d := range repoDirs(nil) {
		keys := tier.Keys(d, claudeDir())
		if len(keys) == 0 || keys[0] == tier.Global {
			continue
		}
		first := projectScope(keys[0])
		for _, k := range keys[1:] {
			p.union(projectScope(k), first)
		}
		if old := tier.Legacy(d, claudeDir()); old != "" && old != tier.Global {
			name := "repo:" + strings.ToLower(old)
			if legacy[name] == nil {
				legacy[name] = map[string]bool{}
			}
			legacy[name][first] = true
		}
	}
	for name, clones := range legacy {
		roots := map[string]bool{}
		for c := range clones {
			roots[p.find(c)] = true
		}
		if len(roots) == 1 { // a name two repositories shared says nothing about which one a fact meant
			for r := range roots {
				p.union(name, r)
			}
		}
	}
}

// dir joins a dir: scope with the keys its checkout has now: a repository named by its path before it had a remote.
func (p *projectAliases) dir(sc string) {
	path, ok := strings.CutPrefix(sc, tier.DirPrefix)
	if !ok {
		return
	}
	for _, k := range tier.Keys(path, claudeDir()) {
		if k != tier.Global {
			p.union(sc, projectScope(k))
		}
	}
}

func (p *projectAliases) find(x string) string {
	for {
		up, ok := p.parent[x]
		if !ok || up == x {
			return x
		}
		x = up
	}
}

func (p *projectAliases) union(a, b string) {
	if ra, rb := p.find(a), p.find(b); ra != rb {
		p.parent[ra] = rb
	}
}

// crossing says why a duplicate retirement must not be applied, or "": the duplicate was learnt in another project
// than the fact it was retired for. A fact that holds in two repositories is two facts, one in each (#607); before,
// the dream retired one and made the other global (#346), and none of the nine facts it promoted held everywhere.
func (p *projectAliases) crossing(s *store.Store, a extract.Action) (string, error) {
	if a.Op != "retire" || a.Kind != "duplicate" || a.By == "" {
		return "", nil
	}
	gone, err := s.Get(a.ID)
	if err != nil {
		return "", err
	}
	by, err := s.Get(a.By)
	if err != nil {
		return "", err
	}
	from, to := projectOf(gone), projectOf(by)
	if from == "" || to == "" || p.same(from, to) {
		return "", nil
	}
	return fmt.Sprintf("a duplicate learnt in %s, %s in %s: each project keeps its copy", from, a.By, to), nil
}
