// Package tier names the memory tier of a working directory the way ~/.claude/hooks/memory-sync.sh does: the
// basename of the main worktree's directory, "<parent>-<name>" for a dot-named one (forgekeep/.github →
// forgekeep-github), and "_global" for the Claude config directory itself. It reads .git instead of running git.
package tier

import (
	"os"
	"path/filepath"
	"strings"
)

// Global is the tier of notes every session sees.
const Global = "_global"

// Of returns the tier of dir, or "" when dir is not inside a git repository. claudeDir is the Claude config
// directory (~/.claude), whose own repository is the global tier.
func Of(dir, claudeDir string) string {
	root := mainRoot(dir)
	if root == "" {
		return ""
	}
	if claudeDir != "" && sameDir(root, claudeDir) {
		return Global
	}
	base := filepath.Base(root)
	if strings.HasPrefix(base, ".") {
		return filepath.Base(filepath.Dir(root)) + "-" + strings.TrimPrefix(base, ".")
	}
	return base
}

// mainRoot finds the nearest .git above dir and returns the directory of the main worktree: the parent of .git
// when it is a directory, the parent of the common git directory when it is a linked worktree's file.
func mainRoot(dir string) string {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for {
		gitPath := filepath.Join(dir, ".git")
		fi, err := os.Stat(gitPath)
		if err == nil {
			if fi.IsDir() {
				return dir
			}
			return worktreeMain(gitPath, dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// worktreeMain reads "gitdir: <main>/.git/worktrees/<name>" from a linked worktree's .git file. A submodule's
// gitdir (<super>/.git/modules/<name>) has no commondir file; the worktree itself is the root then.
func worktreeMain(gitFile, worktree string) string {
	b, err := os.ReadFile(gitFile)
	if err != nil {
		return worktree
	}
	gitdir := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(b)), "gitdir:"))
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(worktree, gitdir)
	}
	c, err := os.ReadFile(filepath.Join(gitdir, "commondir"))
	if err != nil {
		return worktree
	}
	common := strings.TrimSpace(string(c))
	if !filepath.IsAbs(common) {
		common = filepath.Join(gitdir, common)
	}
	return filepath.Dir(filepath.Clean(common))
}

func sameDir(a, b string) bool {
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	if err1 != nil || err2 != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return ra == rb
}
