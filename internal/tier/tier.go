// Package tier names the project a working directory belongs to (#343). A git repository is named by its remotes,
// normalized to host/path (git@example.com:owner/name.git → example.com/owner/name), read from the repository's
// config without running git. A repository with no remote is named by the path of its main worktree, a directory
// outside git by the module of the nearest go.mod or else by its own path, and the Claude config directory is the
// global tier. A temporary directory names nothing.
package tier

import (
	"bufio"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Global is the tier of notes every session sees.
const Global = "_global"

// DirPrefix marks a key that is a path rather than a remote.
const DirPrefix = "dir:"

// Of returns the key facts learnt in dir are written with: the key of the remote named origin, or of the only
// remote; the path of the main worktree for a repository without one; the go.mod module or the path itself outside
// git; Global for the Claude config directory; "" for a temporary directory. claudeDir is the Claude config directory
// (~/.claude).
func Of(dir, claudeDir string) string {
	keys := Keys(dir, claudeDir)
	if len(keys) == 0 {
		return ""
	}
	return keys[0]
}

// Keys returns every key a fact may hold in a session in dir: the key of Of first, then the keys of the other
// remotes (upstream, mirrors) in name order. A fact written in a fork's upstream or on a mirror's other host holds in
// the fork and on every host of the mirror. An empty dir is a session with no working directory, such as a Hermes chat
// or a Hermes session outside git (#347): it has no project, rather than the process's.
func Keys(dir, claudeDir string) []string {
	if dir == "" {
		return nil
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil
	}
	repo, ok := locate(dir)
	if !ok {
		return outside(dir)
	}
	if claudeDir != "" && sameDir(repo.main, claudeDir) {
		return []string{Global}
	}
	remotes := repo.remotes()
	names := make([]string, 0, len(remotes))
	for name := range remotes {
		names = append(names, name)
	}
	slices.Sort(names)
	first := ""
	switch {
	case remotes["origin"] != "":
		first = "origin"
	case len(names) > 0:
		first = names[0]
	}
	var keys []string
	add := func(url string) {
		if k := Normalize(url); k != "" && !slices.Contains(keys, k) {
			keys = append(keys, k)
		}
	}
	if first != "" {
		add(remotes[first])
	}
	for _, name := range names {
		add(remotes[name])
	}
	if len(keys) == 0 {
		if temporary(repo.main) {
			return nil
		}
		return []string{DirPrefix + repo.main}
	}
	return keys
}

// Scope is the scope a fact that holds only in the project of key carries: repo:<key> for a remote, the key itself
// for a path, global for the global tier.
func Scope(key string) string {
	switch {
	case key == "":
		return ""
	case key == Global:
		return "global"
	case strings.HasPrefix(key, DirPrefix):
		return key
	}
	return "repo:" + key
}

// Legacy returns the name memcue gave dir before #343: the basename of the main worktree, "<parent>-<name>" for a
// dot-named one (forgekeep/.github → forgekeep-github), Global for the Claude config directory, "" outside git. The
// migration maps the facts written under those names onto the keys.
func Legacy(dir, claudeDir string) string {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	repo, ok := locate(dir)
	if !ok {
		return ""
	}
	if claudeDir != "" && sameDir(repo.main, claudeDir) {
		return Global
	}
	base := filepath.Base(repo.main)
	if strings.HasPrefix(base, ".") {
		return filepath.Base(filepath.Dir(repo.main)) + "-" + strings.TrimPrefix(base, ".")
	}
	return base
}

// Normalize turns a remote URL into a key: host/path, lowercased, without the scheme, the user, the port and a
// trailing .git. A local path or a file:// URL is not a key and gives "".
func Normalize(url string) string {
	u := strings.TrimSpace(url)
	var host, path string
	if i := strings.Index(u, "://"); i >= 0 {
		scheme := strings.ToLower(u[:i])
		if scheme == "file" {
			return ""
		}
		rest := u[i+3:]
		slash := strings.IndexByte(rest, '/')
		if slash < 0 {
			return ""
		}
		host, path = rest[:slash], rest[slash+1:]
		if at := strings.LastIndexByte(host, '@'); at >= 0 {
			host = host[at+1:]
		}
		if c := strings.LastIndexByte(host, ':'); c >= 0 && !strings.HasSuffix(host, "]") {
			host = host[:c]
		}
	} else {
		// scp-like: [user@]host:path. A colon after the first slash, or none, is a local path.
		c := strings.IndexByte(u, ':')
		if c <= 0 || strings.Contains(u[:c], "/") {
			return ""
		}
		host, path = u[:c], u[c+1:]
		if at := strings.LastIndexByte(host, '@'); at >= 0 {
			host = host[at+1:]
		}
	}
	path = strings.Trim(path, "/")
	path = strings.TrimSuffix(path, ".git")
	path = strings.Trim(path, "/")
	if host == "" || path == "" {
		return ""
	}
	return strings.ToLower(host + "/" + path)
}

// outside names a directory that is not in a git repository: the module of the nearest go.mod up the tree, else the
// directory itself, unless it is temporary.
func outside(dir string) []string {
	if temporary(dir) {
		return nil
	}
	for d := dir; ; {
		if m := module(filepath.Join(d, "go.mod")); m != "" {
			return []string{strings.ToLower(m)}
		}
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	return []string{DirPrefix + dir}
}

// module reads the module path of a go.mod, or "" when there is none.
func module(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if rest, ok := strings.CutPrefix(line, "module"); ok && (rest == "" || rest[0] == ' ' || rest[0] == '\t') {
			return strings.Trim(strings.TrimSpace(rest), `"`)
		}
	}
	return ""
}

// tempRoots are the directories whose subtrees are temporary; a variable so that tests, which live in one, can name
// their own.
var tempRoots = func() []string { return []string{os.TempDir(), "/tmp", "/var/tmp"} }

// temporary reports whether dir is a temporary directory or inside one.
func temporary(dir string) bool {
	d := resolve(dir)
	for _, root := range tempRoots() {
		r := resolve(root)
		if d == r || strings.HasPrefix(d, r+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// repository is a git repository as memcue sees it: the main worktree, the git directory of the worktree at hand and
// the common git directory that holds the config.
type repository struct {
	main, gitDir, common string
}

// locate finds the nearest .git above dir. A .git directory is its own common directory. A .git file of a linked
// worktree names its git directory, whose commondir names the main repository's; a submodule's has no commondir, and
// its git directory holds its own config.
func locate(dir string) (repository, bool) {
	for {
		gitPath := filepath.Join(dir, ".git")
		fi, err := os.Stat(gitPath)
		if err == nil {
			if fi.IsDir() {
				return repository{main: dir, gitDir: gitPath, common: gitPath}, true
			}
			return linked(gitPath, dir), true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return repository{}, false
		}
		dir = parent
	}
}

// linked reads "gitdir: <main>/.git/worktrees/<name>" from a .git file. When the git directory is gone the worktree
// itself is the main one and has no config to read.
func linked(gitFile, worktree string) repository {
	b, err := os.ReadFile(gitFile)
	if err != nil {
		return repository{main: worktree}
	}
	gitdir := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(b)), "gitdir:"))
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(worktree, gitdir)
	}
	c, err := os.ReadFile(filepath.Join(gitdir, "commondir"))
	if err != nil {
		return repository{main: worktree, gitDir: gitdir, common: gitdir}
	}
	common := strings.TrimSpace(string(c))
	if !filepath.IsAbs(common) {
		common = filepath.Join(gitdir, common)
	}
	common = filepath.Clean(common)
	return repository{main: filepath.Dir(common), gitDir: gitdir, common: common}
}

// remotes reads the URL of every remote, with the url.<base>.insteadOf rewrites of the repository's and the global
// config applied the way git applies them: the longest matching prefix wins.
func (r repository) remotes() map[string]string {
	var local config
	if r.common != "" {
		local.read(filepath.Join(r.common, "config"), 0)
		if local.worktreeConfig && r.gitDir != "" {
			local.read(filepath.Join(r.gitDir, "config.worktree"), 0)
		}
	}
	var global config
	for _, p := range globalConfigs() {
		global.read(p, 0)
	}
	rewrites := append(global.insteadOf, local.insteadOf...)
	out := map[string]string{}
	for name, url := range local.remotes {
		out[name] = rewrite(url, rewrites)
	}
	return out
}

// globalConfigs are the global git config files, as git finds them: GIT_CONFIG_GLOBAL alone when it is set, else
// the XDG one and ~/.gitconfig.
func globalConfigs() []string {
	if p, ok := os.LookupEnv("GIT_CONFIG_GLOBAL"); ok {
		return []string{p}
	}
	var out []string
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		out = append(out, filepath.Join(x, "git", "config"))
	} else if home, err := os.UserHomeDir(); err == nil {
		out = append(out, filepath.Join(home, ".config", "git", "config"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		out = append(out, filepath.Join(home, ".gitconfig"))
	}
	return out
}

type insteadOf struct{ prefix, base string }

func rewrite(url string, rules []insteadOf) string {
	best := -1
	for i, r := range rules {
		if strings.HasPrefix(url, r.prefix) && (best < 0 || len(r.prefix) > len(rules[best].prefix)) {
			best = i
		}
	}
	if best < 0 {
		return url
	}
	return rules[best].base + strings.TrimPrefix(url, rules[best].prefix)
}

// config is what memcue needs from a git config file: the remotes' URLs, the insteadOf rewrites, and whether the
// worktree has its own config. [include] path is followed; includeIf is not.
type config struct {
	remotes        map[string]string
	insteadOf      []insteadOf
	worktreeConfig bool
}

// maxIncludeDepth stops an include cycle, as git does.
const maxIncludeDepth = 10

func (c *config) read(path string, depth int) {
	if depth > maxIncludeDepth {
		return
	}
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	if c.remotes == nil {
		c.remotes = map[string]string{}
	}
	var section, sub string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if line[0] == '[' {
			end := strings.IndexByte(line, ']')
			if end < 0 {
				continue
			}
			head := strings.TrimSpace(line[1:end])
			section, sub = strings.ToLower(head), ""
			if q := strings.IndexByte(head, '"'); q >= 0 {
				section = strings.ToLower(strings.TrimSpace(head[:q]))
				sub = strings.TrimSuffix(head[q+1:], `"`)
			} else if dot := strings.IndexByte(head, '.'); dot >= 0 { // old [section.sub] form
				section, sub = strings.ToLower(head[:dot]), head[dot+1:]
			}
			line = strings.TrimSpace(line[end+1:])
			if line == "" {
				continue
			}
		}
		key, value, _ := strings.Cut(line, "=")
		key, value = strings.ToLower(strings.TrimSpace(key)), unquote(strings.TrimSpace(value))
		switch {
		case section == "remote" && key == "url":
			if _, seen := c.remotes[sub]; !seen {
				c.remotes[sub] = value
			}
		case section == "url" && key == "insteadof":
			c.insteadOf = append(c.insteadOf, insteadOf{prefix: value, base: sub})
		case section == "extensions" && key == "worktreeconfig":
			c.worktreeConfig = value == "true" || value == "1" || value == "yes" || value == "on"
		case section == "include" && key == "path" && value != "":
			p := value
			if rest, ok := strings.CutPrefix(p, "~/"); ok {
				if home, err := os.UserHomeDir(); err == nil {
					p = filepath.Join(home, rest)
				}
			} else if !filepath.IsAbs(p) {
				p = filepath.Join(filepath.Dir(path), p)
			}
			c.read(p, depth+1)
		}
	}
}

// unquote drops a trailing comment and the quotes around a git config value.
func unquote(v string) string {
	if !strings.HasPrefix(v, `"`) {
		if i := strings.IndexAny(v, "#;"); i >= 0 {
			v = strings.TrimSpace(v[:i])
		}
		return v
	}
	if end := strings.IndexByte(v[1:], '"'); end >= 0 {
		return v[1 : end+1]
	}
	return strings.Trim(v, `"`)
}

func resolve(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

func sameDir(a, b string) bool { return resolve(a) == resolve(b) }
