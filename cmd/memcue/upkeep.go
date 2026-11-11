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
func (inv *invocation) upkeepHint(cwd string) string {
	if !factsBase().exists() {
		return "" // no base yet: the hint must not create one
	}
	var lines []string
	if l := inv.oldNameHint(cwd); l != "" {
		lines = append(lines, l)
	}
	if l := inv.driverHint(); l != "" {
		lines = append(lines, l)
	}
	if l := legacyBaseHint(); l != "" {
		lines = append(lines, l)
	}
	return strings.Join(lines, "\n")
}

// legacyBaseHint is the line for a base at the old default in the Claude config directory next to the shared one
// (#718). The default opens the shared base, so facts that a memcue of 0.35 or older on this machine keeps writing at
// the old place reach no search. A base named by MEMCUE_FACTS, PostgreSQL and server mode do not use the default.
func legacyBaseHint() string {
	if os.Getenv("MEMCUE_FACTS") != "" || os.Getenv("MEMCUE_POSTGRES_DSN") != "" || serverName() != "" {
		return ""
	}
	shared, legacy := sharedFactsPath(), legacyFactsPath()
	if !baseWritten(shared) || !baseWritten(legacy) {
		return ""
	}
	return fmt.Sprintf("memcue: there is a fact base at %s besides this machine's base in %s; facts written there, "+
		"for example by an older memcue, are not searched. Tell the user to update every memcue of this machine and "+
		"to merge or remove that base.", filepath.Dir(legacy), filepath.Dir(shared))
}

// oldNameHint is the line for facts written under the directory name of the session's repository before #343. Only
// the session's repository is checked: its old name and its key give the exact pair, while a base-wide check would
// repeat every name no clone here has on every card.
func (inv *invocation) oldNameHint(cwd string) string {
	if cwd == "" {
		return ""
	}
	keys := tier.Keys(cwd, claudeDir(), inv.ctx)
	old := tier.Legacy(cwd, claudeDir(), inv.ctx)
	if len(keys) == 0 || old == "" || old == keys[0] {
		return ""
	}
	s, err := inv.openBase()
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
// to tell an ignored base, which needs no driver, from a synced one. A base of the old format that is set up, and so
// synced, gets the line about the move to text instead (#530).
func (inv *invocation) driverHint() string {
	b := factsBase()
	base, err := filepath.Abs(b.path)
	if err != nil {
		return ""
	}
	resolved := resolveDir(base)
	top := resolved
	for {
		if inv.ctx.Err() != nil {
			return ""
		}
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
	rel, err := filepath.Rel(top, resolved)
	if err != nil {
		return ""
	}
	attrsPath, _ := attributesOf(b, top, rel)
	config, _ := inv.readFile(filepath.Join(gitDir, "config"))
	attrs, _ := inv.readFile(attrsPath)
	// The config must name the script of this repository: memcue install writes its absolute path, so a clone moved
	// elsewhere still names the old one, where git finds no driver (#718).
	driver := filepath.Join(gitDir, "memcue-driver.sh")
	_, script := os.Stat(driver)
	if bytes.Contains(config, []byte(driver)) && script == nil && bytes.Contains(attrs, []byte("merge=memcue")) {
		if b.text {
			return ""
		}
		return "memcue: the fact base " + base + " is one SQLite file in git, so every change stores the whole " +
			"file again. `memcue migrate -text` moves it to a text history git merges fact by fact (README, " +
			"\"Moving to the text history\"). Ask the user first: every machine that syncs the base needs this " +
			"memcue version before the move."
	}
	if exec.CommandContext(inv.ctx, "git", "-C", top, "check-ignore", "-q", base).Run() == nil {
		return "" // the base is ignored, so git never merges it
	}
	return "memcue: the fact base is in the git repository " + top + " without memcue's merge driver, so a sync " +
		"conflict on it is a binary conflict. `memcue install` sets it up on this machine."
}
