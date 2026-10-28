package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/juev/memcue/internal/tier"
)

// upkeepHint names what an upgrade still owes this machine and memcue cannot do itself, because it depends on the
// machine's clones or its git setup (#396): facts under the old name of the session's repository, and a base in git
// without the merge driver. Each check is a few stats or one query, so a session start pays nothing noticeable, and a
// check that fails says nothing: the session must start either way.
func upkeepHint(cwd string) string {
	if !factsBase().exists() {
		return "" // no base yet: the hint must not create one
	}
	var lines []string
	if l := oldNameHint(cwd); l != "" {
		lines = append(lines, l)
	}
	if l := driverHint(); l != "" {
		lines = append(lines, l)
	}
	return strings.Join(lines, "\n")
}

// oldNameHint is the line for facts written under the directory name of the session's repository before #343. Only
// the session's repository is checked: its old name and its key give the exact pair, while a base-wide check would
// repeat every name no clone here has on every card.
func oldNameHint(cwd string) string {
	if cwd == "" {
		return ""
	}
	keys := tier.Keys(cwd, claudeDir())
	old := tier.Legacy(cwd, claudeDir())
	if len(keys) == 0 || old == "" || old == keys[0] {
		return ""
	}
	s, err := openBase()
	if err != nil {
		return ""
	}
	defer func() { _ = s.Close() }()
	n, err := s.CountNamed(old)
	if err != nil || n == 0 {
		return ""
	}
	return fmt.Sprintf("memcue: %d facts still carry %q, the old name of this repository, and a session here does not "+
		"see them. `memcue migrate -repos` lists them; `memcue migrate -repos -apply %s=%s` moves them after a "+
		"backup. Ask the user first: another repository may share the name.", n, old, old, keys[0])
}

// driverHint is the line for a base inside a git repository that is not set up to merge it: no merge driver in the
// config, no driver script (an install older than 0.4.0 pinned the binary), or no route in .gitattributes. Without
// them a sync conflict on the base is a binary conflict. Only the files are read; git runs only when one is missing,
// to tell an ignored base, which needs no driver, from a synced one.
func driverHint() string {
	base, err := filepath.Abs(factsPath())
	if err != nil {
		return ""
	}
	top := resolveDir(base)
	for {
		if _, err := os.Stat(filepath.Join(top, ".git")); err == nil {
			break
		}
		parent := filepath.Dir(top)
		if parent == top {
			return "" // not in git: nothing to sync
		}
		top = parent
	}
	gitDir := filepath.Join(top, ".git")
	if fi, err := os.Stat(gitDir); err != nil || !fi.IsDir() {
		return "" // a worktree or a submodule: its git directory is elsewhere, so the files cannot be read here
	}
	config, _ := os.ReadFile(filepath.Join(gitDir, "config"))
	attrs, _ := os.ReadFile(filepath.Join(top, ".gitattributes"))
	_, script := os.Stat(filepath.Join(gitDir, "memcue-driver.sh"))
	if bytes.Contains(config, []byte("memcue-driver.sh")) && script == nil && bytes.Contains(attrs, []byte("merge=memcue")) {
		return ""
	}
	if exec.Command("git", "-C", top, "check-ignore", "-q", base).Run() == nil {
		return "" // the base is ignored, so git never merges it
	}
	return "memcue: the fact base is in the git repository " + top + " without memcue's merge driver, so a sync " +
		"conflict on it is a binary conflict. `memcue install` sets it up on this machine."
}
