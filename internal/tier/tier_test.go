package tier

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func repo(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, path, "init", "-q", "-b", "main")
	git(t, path, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "init")
	return path
}

func TestRepositoryAndSubdirectoryGiveBasename(t *testing.T) {
	root := repo(t, filepath.Join(t.TempDir(), "freshgo"))
	sub := filepath.Join(root, "internal", "web")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{root, sub} {
		if got := Of(dir, ""); got != "freshgo" {
			t.Errorf("Of(%s) = %q, want freshgo", dir, got)
		}
	}
}

func TestLinkedWorktreeGivesMainRepository(t *testing.T) {
	base := t.TempDir()
	root := repo(t, filepath.Join(base, "shop-backend"))
	wt := filepath.Join(base, "shop-backend-TASK-1848")
	git(t, root, "worktree", "add", "-q", wt, "-b", "TASK-1848")
	if got := Of(wt, ""); got != "shop-backend" {
		t.Errorf("worktree tier = %q, want shop-backend", got)
	}
}

func TestDotNamedRepositoryGetsParentPrefix(t *testing.T) {
	root := repo(t, filepath.Join(t.TempDir(), "forgekeep", ".github"))
	if got := Of(root, ""); got != "forgekeep-github" {
		t.Errorf("got %q", got)
	}
}

func TestClaudeDirectoryIsGlobal(t *testing.T) {
	root := repo(t, filepath.Join(t.TempDir(), ".claude"))
	if got := Of(filepath.Join(root), root); got != Global {
		t.Errorf("got %q", got)
	}
}

func TestOutsideRepositoryGivesNothing(t *testing.T) {
	if got := Of(t.TempDir(), ""); got != "" {
		t.Errorf("got %q", got)
	}
}
