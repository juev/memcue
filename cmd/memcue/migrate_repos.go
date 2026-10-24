package main

import (
	"cmp"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/juev/memcue/internal/store"
	"github.com/juev/memcue/internal/tier"
	"github.com/juev/memcue/internal/transcript"
)

// rootDepth is how deep migrate -repos looks for clones under a -root: ~/src/<host>/<owner>/<name> is three levels.
const rootDepth = 4

// repoMap is what this machine knows about the names facts were written under before #343: each old directory name
// with the project keys its clones have now, and every key, for scopes written as owner/name.
type repoMap struct {
	legacy map[string][]string
	keys   []string
	pairs  map[string]string // old=new given on the command line, which win
}

// runMigrateRepos moves the facts written under directory names onto project keys (#343): the origin "<name> <date>"
// becomes "<key> <date>" and the scope repo:<name> becomes the key's scope. The map comes from this machine's clones:
// the working directories of the Claude Code transcripts and the repositories under each root, plus old=new pairs
// for keys that changed, such as a renamed remote. A name it cannot map stays as it is and is reported. Without apply
// it only reports; with apply it backs the base up first and writes one new version per fact it changes.
func runMigrateRepos(roots, pairs []string, apply bool, out io.Writer) error {
	m := repoMap{legacy: map[string][]string{}, pairs: map[string]string{}}
	for _, p := range pairs {
		old, key, ok := strings.Cut(p, "=")
		if !ok || old == "" || key == "" {
			return fmt.Errorf("migrate -repos: %q is not old=new", p)
		}
		m.pairs[old] = key
	}
	for _, dir := range repoDirs(roots) {
		m.learn(dir)
	}
	s, err := store.Open(factsPath())
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()
	facts, err := s.Current()
	if err != nil {
		return err
	}
	type change struct {
		f             store.Fact
		origin, scope string
	}
	var changes []change
	unmapped, ambiguous, orphans := map[string]int{}, map[string]int{}, map[string]int{}
	note := func(name string, why map[string]int) {
		if name != "" {
			why[name]++
		}
	}
	for _, f := range facts {
		origin, scope := f.Origin, f.Scope
		if name := originKey(f.Origin); name != "" {
			switch key, state := m.resolve(name); state {
			case mapped:
				origin = key + strings.TrimPrefix(f.Origin, name)
			case unknown:
				note(name, unmapped)
			case several:
				note(name, ambiguous)
			}
		}
		if name, ok := strings.CutPrefix(f.Scope, "repo:"); ok {
			switch key, state := m.resolve(name); state {
			case mapped:
				scope = tier.Scope(key)
			case unknown:
				note(name, unmapped)
			case several:
				note(name, ambiguous)
			}
		}
		if origin != f.Origin || scope != f.Scope {
			changes = append(changes, change{f, origin, scope})
		}
		// A key no clone here has is either a repository of another machine or one whose remote changed; once per
		// fact, whether its origin, its scope or both name it.
		seen := map[string]bool{}
		for _, k := range []string{originKey(origin), strings.TrimPrefix(scope, "repo:")} {
			if !seen[k] && m.orphan(k) {
				seen[k] = true
				orphans[k]++
			}
		}
	}
	if _, err := fmt.Fprintf(out, "facts %d, to change %d; names known on this machine %d\n", len(facts), len(changes),
		len(m.legacy)); err != nil {
		return err
	}
	if err := report(out, "unmapped", unmapped); err != nil {
		return err
	}
	if err := report(out, "ambiguous (several keys; pass old=new)", ambiguous); err != nil {
		return err
	}
	if err := m.reportOrphans(out, orphans); err != nil {
		return err
	}
	if !apply || len(changes) == 0 {
		return nil
	}
	dir, err := stateDir()
	if err != nil {
		return err
	}
	backup := filepath.Join(dir, "backup", "facts.db.pre-repos-"+time.Now().UTC().Format("20060102T150405Z"))
	if err := os.MkdirAll(filepath.Dir(backup), 0o700); err != nil {
		return err
	}
	if err := s.Backup(backup); err != nil {
		return fmt.Errorf("migrate -repos: backup: %w", err)
	}
	if _, err := fmt.Fprintln(out, "backup", backup); err != nil {
		return err
	}
	for _, c := range changes {
		if _, err := s.Retag(c.f.ID, c.f.Version, c.origin, c.scope); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintf(out, "changed %d\n", len(changes))
	return err
}

// learn records the old name and the current keys of the clone at dir.
func (m *repoMap) learn(dir string) {
	keys := tier.Keys(dir, claudeDir())
	if len(keys) == 0 {
		return
	}
	for _, k := range keys {
		if !slices.Contains(m.keys, k) {
			m.keys = append(m.keys, k)
		}
	}
	old := tier.Legacy(dir, claudeDir())
	if old == "" || old == keys[0] || slices.Contains(m.legacy[old], keys[0]) {
		return
	}
	m.legacy[old] = append(m.legacy[old], keys[0])
}

type resolved int

const (
	current resolved = iota // already a key, or nothing to do
	mapped
	unknown
	several
)

// resolve maps a name a fact was written under to the project key it means now.
func (m *repoMap) resolve(name string) (string, resolved) {
	if key, ok := m.pairs[name]; ok {
		return key, mapped
	}
	if isKey(name) {
		return name, current
	}
	if keys := m.legacy[name]; len(keys) > 0 {
		if len(keys) > 1 {
			return "", several
		}
		return keys[0], mapped
	}
	if strings.Contains(name, "/") { // owner/name, as a model wrote it
		var hits []string
		for _, k := range m.keys {
			if strings.HasSuffix(k, "/"+strings.ToLower(name)) {
				hits = append(hits, k)
			}
		}
		switch len(hits) {
		case 1:
			return hits[0], mapped
		case 0:
			return "", unknown
		}
		return "", several
	}
	return "", unknown
}

// orphan reports whether k is a remote key that no clone on this machine has. A path is not checked: dir: keys belong
// to the machine that wrote them.
func (m *repoMap) orphan(k string) bool {
	return k != "" && k != tier.Global && !strings.HasPrefix(k, tier.DirPrefix) && isKey(k) && !slices.Contains(m.keys, k)
}

// candidates are the keys of this machine's clones a key with no clone may have become: the same host and owner with
// another name (a renamed repository), or the same name under another host or owner (a moved one). At most three.
func (m *repoMap) candidates(k string) []string {
	dir, name := path.Split(k)
	var out []string
	for _, c := range m.keys {
		cdir, cname := path.Split(c)
		if c != k && (cdir == dir || cname == name) {
			out = append(out, c)
		}
	}
	slices.Sort(out)
	return out[:min(len(out), 3)]
}

// reportOrphans prints the keys facts hold that no clone here has, most facts first, with the candidates they may
// have become. The pair to move them is the user's call: a key of a repository cloned only on another machine is
// listed too.
func (m *repoMap) reportOrphans(out io.Writer, orphans map[string]int) error {
	if len(orphans) == 0 {
		return nil
	}
	list := make([]string, 0, len(orphans))
	for k := range orphans {
		list = append(list, k)
	}
	slices.SortFunc(list, func(a, b string) int { return cmp.Or(cmp.Compare(orphans[b], orphans[a]), cmp.Compare(a, b)) })
	if _, err := fmt.Fprintf(out, "keys with no clone on this machine %d (another machine's, or a changed remote: pass old=new):\n",
		len(list)); err != nil {
		return err
	}
	for _, k := range list {
		line := fmt.Sprintf("  %s %d", k, orphans[k])
		if c := m.candidates(k); len(c) > 0 {
			line += "   maybe: " + strings.Join(c, ", ")
		}
		if _, err := fmt.Fprintln(out, line); err != nil {
			return err
		}
	}
	return nil
}

// isKey reports whether name is already a project key: the global tier, a path, or a host/path whose host has a dot.
func isKey(name string) bool {
	if name == tier.Global || strings.HasPrefix(name, tier.DirPrefix) {
		return true
	}
	host, _, ok := strings.Cut(name, "/")
	return ok && strings.Contains(host, ".")
}

// repoDirs lists the working directories the migration learns names from: the cwd of every Claude Code transcript,
// then every repository under the roots, each once.
func repoDirs(roots []string) []string {
	var dirs []string
	add := func(d string) {
		if d != "" && !slices.Contains(dirs, d) {
			dirs = append(dirs, d)
		}
	}
	transcripts, _ := filepath.Glob(filepath.Join(claudeDir(), "projects", "*", "*.jsonl"))
	for _, t := range transcripts {
		f, err := os.Open(t)
		if err != nil {
			continue
		}
		sess, err := transcript.Read(io.LimitReader(f, 1<<20), 0)
		_ = f.Close()
		if err == nil {
			add(sess.CWD)
		}
	}
	for _, root := range roots {
		root = expandHome(root)
		base := strings.Count(filepath.Clean(root), string(filepath.Separator))
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || !d.IsDir() {
				return nil
			}
			if strings.Count(p, string(filepath.Separator))-base > rootDepth || d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			if _, err := os.Stat(filepath.Join(p, ".git")); err == nil {
				add(p)
				return filepath.SkipDir
			}
			return nil
		})
	}
	return dirs
}

// report prints the names of one outcome, most facts first. The names stay on this machine: it is a local command.
func report(out io.Writer, what string, names map[string]int) error {
	if len(names) == 0 {
		return nil
	}
	list := make([]string, 0, len(names))
	for n := range names {
		list = append(list, n)
	}
	slices.SortFunc(list, func(a, b string) int { return cmp.Or(cmp.Compare(names[b], names[a]), cmp.Compare(a, b)) })
	if _, err := fmt.Fprintf(out, "%s %d:\n", what, len(list)); err != nil {
		return err
	}
	for _, n := range list {
		if _, err := fmt.Fprintf(out, "  %s %d\n", n, names[n]); err != nil {
			return err
		}
	}
	return nil
}

// multiFlag is a flag that may be given several times.
type multiFlag []string

func (f *multiFlag) String() string     { return strings.Join(*f, ",") }
func (f *multiFlag) Set(v string) error { *f = append(*f, v); return nil }
