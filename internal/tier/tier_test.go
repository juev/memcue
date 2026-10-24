package tier

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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

// isolate keeps the developer's global git config out of the test and lets t.TempDir() name projects: tests live in
// a temporary directory, which otherwise names nothing.
func isolate(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	old := tempRoots
	tempRoots = func() []string { return nil }
	t.Cleanup(func() { tempRoots = old })
}

func repo(t *testing.T, path string, remotes ...string) string {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, path, "init", "-q", "-b", "main")
	git(t, path, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "init")
	for i := 0; i+1 < len(remotes); i += 2 {
		git(t, path, "remote", "add", remotes[i], remotes[i+1])
	}
	return path
}

func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{
		"git@example.com:owner/name.git":                 "example.com/owner/name",
		"ssh://git@example.org:443/owner/name.git":       "example.org/owner/name",
		"ssh://example.org/owner/name":                   "example.org/owner/name",
		"https://example.com/owner/name.git":             "example.com/owner/name",
		"https://user:token@example.com/Owner/Name.git/": "example.com/owner/name",
		"git@gitlab.example.net:group/sub/name.git":      "gitlab.example.net/group/sub/name",
		"https://gitlab.example.net/group/sub/name":      "gitlab.example.net/group/sub/name",
		"/srv/repos/name.git":                            "",
		"file:///srv/repos/name.git":                     "",
		"../name":                                        "",
		"":                                               "",
	} {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

// The directory's name says nothing: a clone named differently from its remote gets the remote's key, from the root
// and from a subdirectory alike.
func TestRepositoryIsNamedByOrigin(t *testing.T) {
	isolate(t)
	root := repo(t, filepath.Join(t.TempDir(), "local-name"), "origin", "git@example.com:owner/remote-name.git")
	sub := filepath.Join(root, "internal", "web")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{root, sub} {
		if got := Of(dir, ""); got != "example.com/owner/remote-name" {
			t.Errorf("Of(%s) = %q", dir, got)
		}
	}
	if got := Legacy(sub, ""); got != "local-name" {
		t.Errorf("Legacy = %q, want the directory's name", got)
	}
}

// A session knows every remote: origin first, then upstream and a mirror, so their facts hold in it.
func TestKeysHoldEveryRemote(t *testing.T) {
	isolate(t)
	root := repo(t, filepath.Join(t.TempDir(), "fork"),
		"origin", "git@example.com:me/fork.git",
		"upstream", "https://example.com/them/project.git",
		"mirror", "ssh://git@example.org:443/me/fork.git")
	want := []string{"example.com/me/fork", "example.org/me/fork", "example.com/them/project"}
	if got := Keys(root, ""); !slices.Equal(got, want) {
		t.Errorf("Keys = %v, want %v", got, want)
	}
}

// Without origin the only remote names the repository; with several and no origin, the first by name.
func TestRemoteOtherThanOrigin(t *testing.T) {
	isolate(t)
	one := repo(t, filepath.Join(t.TempDir(), "a"), "github", "git@example.com:me/a.git")
	if got := Of(one, ""); got != "example.com/me/a" {
		t.Errorf("one remote: %q", got)
	}
	two := repo(t, filepath.Join(t.TempDir(), "b"), "zeta", "git@example.org:me/b.git", "alpha", "git@example.com:me/b.git")
	if got := Of(two, ""); got != "example.com/me/b" {
		t.Errorf("two remotes: %q", got)
	}
}

func TestLinkedWorktreeReadsTheMainRepositorysRemotes(t *testing.T) {
	isolate(t)
	base := t.TempDir()
	root := repo(t, filepath.Join(base, "shop"), "origin", "git@example.com:team/shop.git")
	wt := filepath.Join(base, "shop-TASK-1848")
	git(t, root, "worktree", "add", "-q", wt, "-b", "TASK-1848")
	if got := Of(wt, ""); got != "example.com/team/shop" {
		t.Errorf("worktree key = %q", got)
	}
	if got := Legacy(wt, ""); got != "shop" {
		t.Errorf("worktree legacy name = %q", got)
	}
}

// With extensions.worktreeConfig a worktree can carry a remote of its own in config.worktree.
func TestWorktreeConfigAddsItsRemotes(t *testing.T) {
	isolate(t)
	base := t.TempDir()
	root := repo(t, filepath.Join(base, "shop"), "origin", "git@example.com:team/shop.git")
	git(t, root, "config", "extensions.worktreeConfig", "true")
	wt := filepath.Join(base, "shop-wt")
	git(t, root, "worktree", "add", "-q", wt, "-b", "wt")
	git(t, wt, "config", "--worktree", "remote.extra.url", "git@example.org:team/extra.git")
	if got := Keys(wt, ""); !slices.Contains(got, "example.org/team/extra") {
		t.Errorf("Keys = %v, want the worktree's own remote too", got)
	}
}

// insteadOf rewrites from the global config apply before the key is taken, so a machine's shorthand does not change
// the key.
func TestInsteadOfIsApplied(t *testing.T) {
	isolate(t)
	global := os.Getenv("GIT_CONFIG_GLOBAL")
	if err := os.WriteFile(global, []byte("[url \"git@example.com:\"]\n\tinsteadOf = ex:\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := repo(t, filepath.Join(t.TempDir(), "x"), "origin", "ex:owner/name.git")
	if got := Of(root, ""); got != "example.com/owner/name" {
		t.Errorf("Of = %q", got)
	}
}

// [include] path is followed relative to the including file.
func TestIncludePathIsFollowed(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	global := os.Getenv("GIT_CONFIG_GLOBAL")
	if err := os.WriteFile(global, []byte("[include]\n\tpath = more.conf\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	more := filepath.Join(filepath.Dir(global), "more.conf")
	if err := os.WriteFile(more, []byte("[url \"https://example.org/\"]\n\tinsteadOf = org:\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := repo(t, filepath.Join(dir, "y"), "origin", "org:owner/y")
	if got := Of(root, ""); got != "example.org/owner/y" {
		t.Errorf("Of = %q", got)
	}
}

func TestRepositoryWithoutRemoteIsItsMainWorktreePath(t *testing.T) {
	isolate(t)
	base := t.TempDir()
	root := repo(t, filepath.Join(base, "local-only"))
	wt := filepath.Join(base, "local-only-wt")
	git(t, root, "worktree", "add", "-q", wt, "-b", "wt")
	want := DirPrefix + resolve(root)
	for _, dir := range []string{root, wt} {
		if got := Of(dir, ""); resolveKey(got) != want {
			t.Errorf("Of(%s) = %q, want %q", dir, got, want)
		}
	}
}

func TestOutsideGitIsTheGoModuleOrTheDirectory(t *testing.T) {
	isolate(t)
	mod := filepath.Join(t.TempDir(), "tool")
	sub := filepath.Join(mod, "cmd")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "go.mod"), []byte("// x\nmodule example.com/Owner/Tool\n\ngo 1.27\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Of(sub, ""); got != "example.com/owner/tool" {
		t.Errorf("go.mod: %q", got)
	}
	plain := t.TempDir()
	if got := Of(plain, ""); resolveKey(got) != DirPrefix+resolve(plain) {
		t.Errorf("plain directory: %q", got)
	}
}

// A temporary directory is never a project, inside git or not.
func TestTemporaryDirectoryNamesNothing(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	if got := Of(t.TempDir(), ""); got != "" {
		t.Errorf("plain temporary directory: %q", got)
	}
	if got := Of(repo(t, filepath.Join(t.TempDir(), "scratch")), ""); got != "" {
		t.Errorf("temporary repository without a remote: %q", got)
	}
	if got := Of(repo(t, filepath.Join(t.TempDir(), "r"), "origin", "git@example.com:o/r.git"), ""); got != "example.com/o/r" {
		t.Errorf("a remote names the repository wherever it is cloned: %q", got)
	}
}

func TestClaudeDirectoryIsGlobal(t *testing.T) {
	isolate(t)
	root := repo(t, filepath.Join(t.TempDir(), ".claude"), "origin", "git@example.com:me/claude.git")
	if got := Of(root, root); got != Global {
		t.Errorf("got %q", got)
	}
	if got := Legacy(root, root); got != Global {
		t.Errorf("legacy %q", got)
	}
}

func TestDotNamedRepositoryLegacyName(t *testing.T) {
	isolate(t)
	root := repo(t, filepath.Join(t.TempDir(), "forgekeep", ".github"))
	if got := Legacy(root, ""); got != "forgekeep-github" {
		t.Errorf("got %q", got)
	}
}

func TestScope(t *testing.T) {
	for key, want := range map[string]string{"example.com/o/n": "repo:example.com/o/n", "dir:/srv/x": "dir:/srv/x",
		Global: "global", "": ""} {
		if got := Scope(key); got != want {
			t.Errorf("Scope(%q) = %q, want %q", key, got, want)
		}
	}
}

// resolveKey resolves the path of a dir: key, so a test compares it with a resolved path on macOS, where the
// temporary directory is behind a symlink.
func resolveKey(key string) string {
	if p, ok := cutDir(key); ok {
		return DirPrefix + resolve(p)
	}
	return key
}

func cutDir(key string) (string, bool) {
	if len(key) > len(DirPrefix) && key[:len(DirPrefix)] == DirPrefix {
		return key[len(DirPrefix):], true
	}
	return "", false
}
