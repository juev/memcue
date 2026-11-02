package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

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

// unpromotedFile marks a machine whose dream log was walked for the facts the old promotion made global (#607).
const unpromotedFile = "dream.unpromoted"

// unpromote takes back what the promotion of #346 did, once a machine: every fact a dream of this machine made global
// goes back to the project it held in, keeping its creation time, and the duplicate retired for it comes back when it
// was learnt in another project, since a fact of two repositories is two facts (#607). dream.log of the machine is
// the record of what was promoted, and gets a line of what was taken back. A fact no longer global, or one that had no
// project scope before it became global, is left as it is.
func unpromote(dir string, s *store.Store) error {
	if _, err := os.Stat(filepath.Join(dir, unpromotedFile)); err == nil {
		return nil
	}
	log := filepath.Join(dir, "dream.log")
	pairs, err := promotions(log)
	if err != nil {
		return err
	}
	aliases := &projectAliases{}
	rec := dreamRecord{Time: time.Now().UTC().Format(time.RFC3339)}
	for _, pr := range pairs {
		back, err := demote(s, aliases, pr.by)
		if err != nil {
			return err
		}
		if back != "" {
			rec.Unpromoted = append(rec.Unpromoted, pr.by+" "+back)
		}
		by, err := s.Get(pr.by)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		ok, err := reopenDuplicate(s, aliases, pr.gone, projectOf(by))
		if err != nil {
			return err
		}
		if ok {
			rec.Reopened = append(rec.Reopened, pr.gone)
		}
	}
	if len(rec.Unpromoted)+len(rec.Reopened) > 0 {
		if err := appendJSON(log, rec); err != nil {
			return err
		}
	}
	return os.WriteFile(filepath.Join(dir, unpromotedFile), []byte(rec.Time+"\n"), 0o600)
}

type promotion struct{ gone, by string }

// promotions reads the duplicate retirements of dream.log whose survivor a dream made global, in the log's order.
func promotions(path string) ([]promotion, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []promotion
	for line := range strings.Lines(string(b)) {
		var r struct {
			Actions  []extract.Action `json:"actions"`
			Promoted []string         `json:"promoted"`
		}
		if json.Unmarshal([]byte(line), &r) != nil || len(r.Promoted) == 0 {
			continue
		}
		for _, a := range r.Actions {
			if a.Op == "retire" && a.Kind == "duplicate" && slices.Contains(r.Promoted, a.By) {
				out = append(out, promotion{gone: a.ID, by: a.By})
			}
		}
	}
	return out, nil
}

// demote moves a fact made global back to the project scope it had right before, and returns that scope, or "" when
// it leaves the fact alone: retired, not global now, or global with no project scope before. When the scope before was
// a name memcue gave a clone before #343 and an earlier version had a key of the same repository, the key is the one
// restored.
func demote(s *store.Store, aliases *projectAliases, id string) (string, error) {
	cur, err := s.Get(id)
	if errors.Is(err, store.ErrNotFound) {
		return "", nil
	}
	if err != nil || cur.Status != store.Active || !strings.EqualFold(cur.Scope, "global") {
		return "", err
	}
	hist, err := s.History(id)
	if err != nil {
		return "", err
	}
	i := len(hist) - 1
	for i > 0 && strings.EqualFold(hist[i-1].Scope, "global") {
		i--
	}
	if i == 0 {
		return "", nil
	}
	back := hist[i-1].Scope
	p := projectOf(store.Fact{Scope: back})
	if p == "" {
		return "", nil
	}
	if !isKey(strings.TrimPrefix(p, "repo:")) {
		for j := i - 2; j >= 0; j-- {
			k := projectOf(store.Fact{Scope: hist[j].Scope})
			if k != "" && isKey(strings.TrimPrefix(k, "repo:")) && aliases.same(p, k) {
				back = hist[j].Scope
				break
			}
		}
	}
	_, err = s.Relabel(cur.ID, cur.Version, cur.Kind, back, cur.Expires)
	return back, err
}

// reopenDuplicate brings back a duplicate the promotion retired when it is still retired and was learnt in another
// project than project, where the fact it was retired for holds now.
func reopenDuplicate(s *store.Store, aliases *projectAliases, id, project string) (bool, error) {
	cur, err := s.Get(id)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil || cur.Status != store.Retired {
		return false, err
	}
	from := projectOf(cur)
	if from == "" || project == "" || aliases.same(from, project) {
		return false, nil
	}
	_, err = s.Restore(id)
	return err == nil, err
}

// knownProjects are the projects a dream may move a global fact to (#607): the scopes projectOf gives the active
// facts, a key of a remote or a dir: path, sorted. A name memcue gave a clone before #343 is left out: migrate -repos
// moves its facts onto the key.
func knownProjects(active []store.Fact) []string {
	seen := map[string]bool{}
	for _, f := range active {
		if p := projectOf(f); p != "" && isKey(strings.TrimPrefix(p, "repo:")) {
			seen[p] = true
		}
	}
	return slices.Sorted(maps.Keys(seen))
}

// renderScopeInput is the JSON the scope task reads: the projects of the base, and the id, kind, origin and text of
// each global fact.
func renderScopeInput(facts []store.Fact, projects []string) (string, error) {
	type item struct {
		ID     string `json:"id"`
		Kind   string `json:"kind,omitempty"`
		Origin string `json:"origin,omitempty"`
		Text   string `json:"text"`
	}
	items := make([]item, len(facts))
	for i, f := range facts {
		items[i] = item{ID: f.ID, Kind: f.Kind, Origin: f.Origin, Text: f.Content}
	}
	if projects == nil {
		projects = []string{}
	}
	b, err := json.Marshal(map[string]any{"projects": projects, "facts": items})
	return string(b), err
}

// applyRescopes moves the global facts of a checked answer to their project, keeping the creation time. A fact retired
// since the packet, changed in more than its labels, or no longer global is refused: the model judged another text or
// another scope.
func applyRescopes(s *store.Store, rescopes []dreamPlanRescope, saw map[string]int, rec *dreamRecord, out io.Writer) error {
	for _, r := range rescopes {
		cur, err := s.Get(r.ID)
		if errors.Is(err, store.ErrNotFound) {
			rec.Refused = append(rec.Refused, "scope "+r.ID+": "+err.Error())
			continue
		}
		if err != nil {
			return err
		}
		same := cur.Version == r.Version
		if !same && cur.Status == store.Active {
			if same, err = onlyLabelsSince(s, r.ID, r.Version); err != nil {
				return err
			}
		}
		switch {
		case cur.Status != store.Active:
			rec.Refused = append(rec.Refused, "scope "+r.ID+": retired since the packet")
			continue
		case !same:
			rec.Refused = append(rec.Refused, "scope "+r.ID+": changed since the packet")
			continue
		case !strings.EqualFold(cur.Scope, "global"):
			rec.Refused = append(rec.Refused, "scope "+r.ID+": no longer global, "+cur.Scope)
			continue
		}
		f, err := s.Relabel(cur.ID, cur.Version, cur.Kind, r.Scope, cur.Expires)
		if err != nil {
			return err
		}
		if saw[f.ID] == cur.Version {
			saw[f.ID] = f.Version
		}
		rec.Rescoped = append(rec.Rescoped, f.ID+" "+r.Scope)
		if _, err := fmt.Fprintf(out, "project %s %s: %s\n", f.ID, r.Scope, r.Reason); err != nil {
			return err
		}
	}
	return nil
}

// readScopeCursor reads the id of the last global fact a dream gave to check the scope of; no file is the start.
func readScopeCursor(dir string) (string, error) {
	b, err := os.ReadFile(filepath.Join(dir, "dream.scope"))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	return strings.TrimSpace(string(b)), err
}

func writeScopeCursor(dir, id string) error {
	if id == "" {
		return nil
	}
	return os.WriteFile(filepath.Join(dir, "dream.scope"), []byte(id+"\n"), 0o600)
}
