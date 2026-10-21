package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildBinary builds memcue once for the tests that drive it through git.
func buildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "memcue")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

func run(t *testing.T, dir string, env []string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
	return string(out)
}

func gitEnv(home string) []string {
	return []string{"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null", "HOME=" + home,
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t"}
}

// twoClones makes a bare origin with facts.db and .gitattributes, and two clones of it ("mac" and "server").
func twoClones(t *testing.T, bin string) (env []string, mac, server string) {
	t.Helper()
	dir := t.TempDir()
	env = gitEnv(dir)
	origin := filepath.Join(dir, "origin.git")
	run(t, dir, env, "git", "init", "-q", "--bare", "-b", "main", origin)
	seed := filepath.Join(dir, "seed")
	run(t, dir, env, "git", "clone", "-q", origin, seed)
	if err := os.MkdirAll(filepath.Join(seed, "memory"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seed, ".gitattributes"), []byte("memory/facts.db merge=memcue diff=memcue\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, seed, append(env, "MEMCUE_FACTS="+filepath.Join(seed, "memory", "facts.db")), bin, "add", "-origin", "seed",
		"Hermes работает на модели deepseek-flash")
	run(t, seed, env, "git", "add", ".gitattributes", "memory/facts.db")
	run(t, seed, env, "git", "commit", "-q", "-m", "seed")
	run(t, seed, env, "git", "push", "-q", "origin", "main")
	mac, server = filepath.Join(dir, "mac"), filepath.Join(dir, "server")
	run(t, dir, env, "git", "clone", "-q", origin, mac)
	run(t, dir, env, "git", "clone", "-q", origin, server)
	return env, mac, server
}

func addAndCommit(t *testing.T, bin, repo string, env []string, fact string) {
	t.Helper()
	run(t, repo, append(env, "MEMCUE_FACTS="+filepath.Join(repo, "memory", "facts.db")), bin, "add", fact)
	run(t, repo, env, "git", "commit", "-q", "-am", "add fact")
}

func TestGitMergesFactBasesWithTheDriver(t *testing.T) {
	bin := buildBinary(t)
	env, mac, server := twoClones(t, bin)
	run(t, mac, append(env, "MEMCUE_FACTS="+filepath.Join(mac, "memory", "facts.db")), bin, "install")

	addAndCommit(t, bin, server, env, "Updater держит по два rollback-образа на сервис")
	run(t, server, env, "git", "push", "-q", "origin", "main")
	addAndCommit(t, bin, mac, env, "Caddy-private монтирует файл, после замены пересоздать контейнер")
	run(t, mac, env, "git", "pull", "-q", "--no-rebase", "origin", "main")

	facts := run(t, mac, append(env, "MEMCUE_FACTS="+filepath.Join(mac, "memory", "facts.db")), bin, "facts")
	for _, want := range []string{"deepseek-flash", "rollback-образа", "Caddy-private"} {
		if !strings.Contains(facts, want) {
			t.Errorf("merged base lacks %q:\n%s", want, facts)
		}
	}
	if st := run(t, mac, env, "git", "status", "--porcelain"); st != "" {
		t.Errorf("merge left changes: %s", st)
	}
	diff := run(t, mac, env, "git", "log", "-p", "-1", "--format=", "HEAD^2", "--", "memory/facts.db")
	if !strings.Contains(diff, "+") || !strings.Contains(diff, "rollback-образа") {
		t.Errorf("textconv diff not readable:\n%s", diff)
	}
}

func TestGitWithoutTheDriverStopsOnConflict(t *testing.T) {
	bin := buildBinary(t)
	env, mac, server := twoClones(t, bin)

	addAndCommit(t, bin, server, env, "Updater держит по два rollback-образа на сервис")
	run(t, server, env, "git", "push", "-q", "origin", "main")
	addAndCommit(t, bin, mac, env, "Caddy-private монтирует файл, после замены пересоздать контейнер")
	cmd := exec.Command("git", "pull", "-q", "--no-rebase", "origin", "main")
	cmd.Dir = mac
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "CONFLICT") {
		t.Errorf("want a visible conflict, got err=%v\n%s", err, out)
	}
}

func TestInstallAddsGitattributesOnce(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	env := gitEnv(dir)
	repo := filepath.Join(dir, "claude")
	run(t, dir, env, "git", "init", "-q", repo)
	env = append(env, "MEMCUE_FACTS="+filepath.Join(repo, "memory", "facts.db"))
	run(t, repo, env, bin, "install")
	run(t, repo, env, bin, "install")
	b, err := os.ReadFile(filepath.Join(repo, ".gitattributes"))
	if err != nil || string(b) != "memory/facts.db merge=memcue diff=memcue\n" {
		t.Errorf(".gitattributes %q %v", b, err)
	}
	if drv := run(t, repo, env, "git", "config", "merge.memcue.driver"); !strings.Contains(drv, " merge %O %A %B") {
		t.Errorf("driver %q", drv)
	}
}

func TestInstallOutsideGitDoesNothing(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	env := append(gitEnv(dir), "MEMCUE_FACTS="+filepath.Join(dir, "plain", "memory", "facts.db"))
	if out := run(t, dir, env, bin, "install"); !strings.Contains(out, "not in a git repository") {
		t.Errorf("got %q", out)
	}
}

// install must not pin the binary it happens to run from: a driver script in .git finds it again, so an upgrade that
// moves the binary does not turn the next merge into a binary conflict.
func TestInstallWritesStableDriverScript(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	env := gitEnv(dir)
	repo := filepath.Join(dir, "repo")
	if err := os.MkdirAll(filepath.Join(repo, "memory"), 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, dir, env, "git", "init", "-q", "-b", "main", repo)
	base := filepath.Join(repo, "memory", "facts.db")
	run(t, repo, append(env, "MEMCUE_FACTS="+base), bin, "add", "-origin", "x 2026-10-19", "Fact for install.")
	run(t, repo, append(env, "MEMCUE_FACTS="+base), bin, "install")

	driver := run(t, repo, env, "git", "config", "--get", "merge.memcue.driver")
	script := filepath.Join(repo, ".git", "memcue-driver.sh")
	if !strings.Contains(driver, script) {
		t.Fatalf("driver %q does not call %s", driver, script)
	}
	if strings.Contains(driver, bin) {
		t.Fatalf("driver pins the running binary: %s", driver)
	}
	fi, err := os.Stat(script)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&0o111 == 0 {
		t.Fatalf("driver script mode %v is not executable", fi.Mode())
	}
	body, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "command -v memcue") || !strings.Contains(string(body), bin) {
		t.Fatalf("driver script does not look the binary up again:\n%s", body)
	}

	// MEMCUE_BIN wins and the arguments reach memcue unchanged.
	fake := filepath.Join(dir, "fake-memcue")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := run(t, repo, []string{"MEMCUE_BIN=" + fake}, script, "merge", "base", "ours", "theirs")
	if strings.TrimSpace(out) != "merge base ours theirs" {
		t.Fatalf("driver passed %q", out)
	}

	// PATH is enough when MEMCUE_BIN is not set.
	pathDir := filepath.Join(dir, "path-bin")
	if err := os.MkdirAll(pathDir, 0o755); err != nil {
		t.Fatal(err)
	}
	onPath := filepath.Join(pathDir, "memcue")
	if err := os.WriteFile(onPath, []byte("#!/bin/sh\nprintf 'path:%s\\n' \"$*\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out = run(t, repo, []string{"MEMCUE_BIN=", "PATH=" + pathDir}, script, "dump", "facts.db")
	if strings.TrimSpace(out) != "path:dump facts.db" {
		t.Fatalf("driver with PATH passed %q", out)
	}

	// With no binary anywhere the script says what to do instead of git failing to exec a missing path.
	moved := bin + ".moved"
	if err := os.Rename(bin, moved); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(script, "merge", "base", "ours", "theirs")
	cmd.Dir = repo
	cmd.Env = []string{"MEMCUE_BIN=", "PATH=" + filepath.Join(dir, "empty"), "HOME=" + dir}
	failed, _ := cmd.CombinedOutput()
	if !strings.Contains(string(failed), "no memcue binary found") {
		t.Fatalf("driver without a binary said %q", failed)
	}
}
