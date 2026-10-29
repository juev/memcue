package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/juev/memcue/internal/extract"
	"github.com/juev/memcue/internal/store"
)

func env(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// Outside a repository: search's default place depends on the working directory's repository (#409), and the
	// suite must not depend on where it runs.
	t.Chdir(dir)
	t.Setenv("MEMCUE_FACTS", filepath.Join(dir, "facts.db"))
	// The gateway's own environment carries these; a test that asks whether a log is off by default must not
	// inherit the flag of the shell that runs it (the same reason the suite is run with env -u MEMCUE_*).
	t.Setenv("MEMCUE_SEARCH_LOG", "")
	t.Setenv("MEMCUE_USAGE_LOG", "")
	t.Setenv("MEMCUE_NUMBER_TERMS", "")
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(dir, "claude"))
	// The warning about Claude Code's built-in memory (#494) has its own test; elsewhere it would be one more line
	// after the card.
	t.Setenv("CLAUDE_CODE_DISABLE_AUTO_MEMORY", "1")
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	// The tests pin the historical layout; the per-base subdirectory has its own test.
	t.Setenv("MEMCUE_STATE_DIR", filepath.Join(dir, "state", "memcue"))
	// A dream asks the model to split facts before it groups them; a test that is not about splitting gets no split
	// and no network call.
	oldSplit := splitFn
	splitFn = func(string) (splitReply, error) { return splitReply{}, nil }
	t.Cleanup(func() { splitFn = oldSplit })
	oldExpand := expandFn
	expandFn = func(string) (extract.ExpandResult, error) { return extract.ExpandResult{}, nil }
	t.Cleanup(func() { expandFn = oldExpand })
	// A background dream (#569) has its own tests; elsewhere the machine's claude must not decide whether the session
	// start says a dream is due, and a session start must not start a process.
	t.Setenv("MEMCUE_DREAM_BACKGROUND", "")
	oldLook, oldSpawn := lookClaude, spawnDream
	lookClaude = func() bool { return false }
	spawnDream = func(string) error { return nil }
	t.Cleanup(func() { lookClaude, spawnDream = oldLook, oldSpawn })
	return dir
}

func seed(t *testing.T) {
	t.Helper()
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if _, err := s.AddWith("Gradle вне make требует JAVA_HOME=/opt/homebrew/opt/openjdk@21, иначе сборка не находит JDK.",
		[]string{"gradle", "jdk"}, "lists 2026-10-09 (lists/android.md)", "", store.Meta{}); err != nil {
		t.Fatal(err)
	}
}

const gradlePrompt = "Как запустить gradle без make, какой JAVA_HOME нужен для JDK?"

// gitRepo makes path a repository whose origin is git@example.com:team/<basename>.git, so its project key is
// example.com/team/<basename> (#343): the key comes from the remote, not from the directory.
func gitRepo(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(path, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "[remote \"origin\"]\n\turl = git@example.com:team/" + filepath.Base(path) + ".git\n"
	if err := os.WriteFile(filepath.Join(path, ".git", "config"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return "example.com/team/" + filepath.Base(path)
}

func TestAddNamesWorkingRepositoryByDefault(t *testing.T) {
	dir := env(t)
	repo := filepath.Join(dir, "shop")
	key := gitRepo(t, repo)
	t.Chdir(repo)
	var out bytes.Buffer
	if err := runAdd([]string{"-keywords", "k", "факт без origin"}, &out); err != nil {
		t.Fatal(err)
	}
	if err := runAdd([]string{"-origin", "x 2026-01-01", "факт с origin"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	f, err := s.Get(strings.TrimSpace(out.String()))
	if err != nil {
		t.Fatal(err)
	}
	if want := key + " " + time.Now().Format(time.DateOnly); f.Origin != want {
		t.Errorf("origin %q, want %q", f.Origin, want)
	}
	all, _ := s.Active()
	for _, a := range all {
		if a.Content == "факт с origin" && a.Origin != "x 2026-01-01" {
			t.Errorf("explicit origin replaced: %q", a.Origin)
		}
	}
}

func TestAddUsesCwdForDefaultOrigin(t *testing.T) {
	dir := env(t)
	repo := filepath.Join(dir, "shop")
	key := gitRepo(t, repo)
	var out bytes.Buffer
	if err := runAdd([]string{"-cwd", repo, "-source", "agent", "--", "факт из сессии"}, &out); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	f, err := s.Get(strings.TrimSpace(out.String()))
	if err != nil {
		t.Fatal(err)
	}
	if want := key + " " + time.Now().Format(time.DateOnly); f.Origin != want {
		t.Errorf("origin %q, want %q", f.Origin, want)
	}
	if f.Source != "agent" {
		t.Errorf("source %q, want agent", f.Source)
	}
}

func TestSearchJSONCarriesIDAndContent(t *testing.T) {
	env(t)
	seed(t)
	var out bytes.Buffer
	if err := runSearch([]string{"-json", "gradle jdk"}, &out); err != nil {
		t.Fatal(err)
	}
	var facts []searchFact
	if err := unmarshalFacts(out.Bytes(), &facts); err != nil {
		t.Fatalf("output %q: %v", out.String(), err)
	}
	if len(facts) != 1 || len(facts[0].ID) != 26 || !strings.Contains(facts[0].Content, "openjdk@21") {
		t.Errorf("got %+v", facts)
	}
	if len(facts[0].Keywords) != 2 {
		t.Errorf("keywords %v", facts[0].Keywords)
	}
}

func TestSearchLogsTheQueryAndTheFacts(t *testing.T) {
	dir := env(t)
	seed(t)
	t.Setenv("MEMCUE_SEARCH_LOG", "1")
	for _, q := range []string{"gradle jdk", "kubernetes"} {
		if err := runSearch([]string{"-session", "s1", "-cwd", dir, q}, &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(filepath.Join(dir, "state", "memcue", "search.log"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 2 {
		t.Fatalf("log %q: want a line per search, the empty one included", b)
	}
	var rec searchRecord
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Session != "s1" || rec.Query != "gradle jdk" || len(rec.Facts) != 1 || rec.Host == "" {
		t.Errorf("got %+v", rec)
	}
}

func TestSearchLogOffByDefault(t *testing.T) {
	dir := env(t)
	seed(t)
	if err := runSearch([]string{"gradle"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "state", "memcue", "search.log")); !os.IsNotExist(err) {
		t.Errorf("search.log written without MEMCUE_SEARCH_LOG: %v", err)
	}
}

func TestAddExpiresAndFactsShowsIt(t *testing.T) {
	env(t)
	var out bytes.Buffer
	if err := runAdd([]string{"-expires", "2026-10-21", "Проверить работу dream 21 октября."}, &out); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runFacts(&out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "expires=2026-10-21") {
		t.Errorf("facts output lacks the expiry: %q", out.String())
	}
	if err := runAdd([]string{"-expires", "21.10.2026", "Плохая дата."}, &out); err == nil {
		t.Error("bad date accepted")
	}
}

// A ~ in MEMCUE_FACTS is expanded rather than becoming a directory named "~" in the working directory: a YAML profile
// or a launcher sets the variable without a shell.
func TestFactsPathExpandsHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, tc := range []struct{ in, want string }{
		{"~/memory/facts.db", filepath.Join(home, "memory", "facts.db")},
		{"$HOME/memory/facts.db", filepath.Join(home, "memory", "facts.db")},
		{"~", home},
		{"/absolute/facts.db", "/absolute/facts.db"},
		{"relative/facts.db", "relative/facts.db"},
	} {
		t.Setenv("MEMCUE_FACTS", tc.in)
		if got := factsPath(); got != tc.want {
			t.Errorf("factsPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// In a temporary directory a fact has no origin: a scratch directory says nothing about where the fact came from and
// hides that nobody knows it (#287). A working directory outside git is named by its path (#343, internal/tier).
func TestOriginIsEmptyInATemporaryDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := defaultOriginAt(dir); got != "" {
		t.Errorf("defaultOriginAt(%q) = %q, want empty", dir, got)
	}
	if got := originAt(dir, "2026-10-19"); got != "" {
		t.Errorf("originAt(%q) = %q, want empty", dir, got)
	}
	if got := originAt("", "2026-10-19"); got != "" {
		t.Errorf("originAt(\"\") = %q, want empty", got)
	}
}

// Inside a repository the origin names the project key and the date, which is what the tie-break reads. The key is
// the remote's, wherever the clone lives.
func TestOriginNamesTheRepository(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "repo")
	key := gitRepo(t, dir)
	if got, want := originAt(dir, "2026-10-19"), key+" 2026-10-19"; got != want {
		t.Errorf("originAt(%q) = %q, want %q", dir, got, want)
	}
}

func TestVersionMinimum(t *testing.T) {
	old := version
	defer func() { version = old }()
	version = "0.4.0"
	var out bytes.Buffer
	if err := runVersion([]string{"-json"}, &out); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(out.String()), `{"version":"0.4.0"}`; got != want {
		t.Errorf("version -json = %q, want %q", got, want)
	}
	if err := runVersion([]string{"-minimum", "0.4.0"}, &bytes.Buffer{}); err != nil {
		t.Errorf("0.4.0 is not older than 0.4.0: %v", err)
	}
	err := runVersion([]string{"-minimum", "0.5.0"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "older than the required 0.5.0") {
		t.Errorf("0.4.0 against 0.5.0 = %v, want an older-than error", err)
	}
	version = "dev"
	if err := runVersion([]string{"-minimum", "99.0.0"}, &bytes.Buffer{}); err != nil {
		t.Errorf("a dev build is not blocked: %v", err)
	}
}

func TestVersionAtLeast(t *testing.T) {
	for _, tc := range []struct {
		current, minimum string
		want             bool
	}{
		{"0.4.0", "0.4.0", true},
		{"v0.4.1", "0.4.0", true},
		{"0.4.0-dirty", "0.4.0", true},
		{"0.10.0", "0.9.9", true},
		{"0.3.9", "0.4.0", false},
		{"dev", "99.0.0", true},
		{"garbage", "0.4.0", true},
	} {
		if got := versionAtLeast(tc.current, tc.minimum); got != tc.want {
			t.Errorf("versionAtLeast(%q, %q) = %v, want %v", tc.current, tc.minimum, got, tc.want)
		}
	}
}

// The model never names a repository: a repo scope, bare or with any name, becomes the scope of the origin's project,
// and without a project it has nothing to hold in (#343).
func TestRepoScopeComesFromTheOrigin(t *testing.T) {
	for _, tc := range []struct{ scope, origin, want string }{
		{"repo", "example.com/team/shop 2026-10-24", "repo:example.com/team/shop"},
		{"repo:shop", "example.com/team/shop 2026-10-24", "repo:example.com/team/shop"},
		{"Repo:other/name", "example.com/team/shop 2026-10-24", "repo:example.com/team/shop"},
		{"repo", "dir:/srv/work/notes 2026-10-24", "dir:/srv/work/notes"},
		{"repo", "", ""},
		{"repo", "explicit origin without a date", ""},
		{"global", "example.com/team/shop 2026-10-24", "global"},
		{"host:db1", "", "host:db1"},
		{"service:mail", "example.com/team/shop 2026-10-24", "service:mail"},
	} {
		if got := repoScope(tc.scope, tc.origin); got != tc.want {
			t.Errorf("repoScope(%q, %q) = %q, want %q", tc.scope, tc.origin, got, tc.want)
		}
	}
}

// add -scope repo takes the working directory's project, and refuses where there is none.
func TestAddScopeRepoTakesTheProject(t *testing.T) {
	dir := env(t)
	repo := filepath.Join(dir, "shop")
	key := gitRepo(t, repo)
	var out bytes.Buffer
	if err := runAdd([]string{"-cwd", repo, "-kind", "decision", "-scope", "repo", "--", "мержить squash"}, &out); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	f, err := s.Get(strings.TrimSpace(out.String()))
	_ = s.Close()
	if err != nil || f.Scope != "repo:"+key {
		t.Errorf("scope %q %v, want repo:%s", f.Scope, err, key)
	}
	if err := runAdd([]string{"-cwd", t.TempDir(), "-scope", "repo", "--", "без проекта"}, &bytes.Buffer{}); err == nil {
		t.Error("add -scope repo outside a project succeeded")
	}
}

// add -cwd "" is a session with no project, even when memcue runs inside a repository; an unset -cwd is still the
// process's working directory (#347).
func TestAddEmptyCwdHasNoProject(t *testing.T) {
	dir := env(t)
	repo := filepath.Join(dir, "shop")
	key := gitRepo(t, repo)
	t.Chdir(repo)
	origin := func(args ...string) string {
		t.Helper()
		var out bytes.Buffer
		if err := runAdd(append(args, "--", "факт "+strings.Join(args, " ")), &out); err != nil {
			t.Fatal(err)
		}
		s, err := store.Open(factsPath())
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = s.Close() }()
		f, err := s.Get(strings.TrimSpace(out.String()))
		if err != nil {
			t.Fatal(err)
		}
		return f.Origin
	}
	if got := origin("-cwd", ""); got != "" {
		t.Errorf("-cwd \"\": origin %q, want none", got)
	}
	if got := origin("-source", "agent"); !strings.HasPrefix(got, key+" ") {
		t.Errorf("no -cwd: origin %q, want the working directory's project", got)
	}
	if err := runAdd([]string{"-cwd", "", "-scope", "repo", "--", "без проекта"}, &bytes.Buffer{}); err == nil {
		t.Error("add -cwd \"\" -scope repo succeeded")
	}
}

// unmarshalFacts reads the facts of a search -json answer, {"facts": [...], "hints": [...]} (#453), into facts.
func unmarshalFacts(data []byte, facts any) error {
	var a struct {
		Facts json.RawMessage `json:"facts"`
	}
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	return json.Unmarshal(a.Facts, facts)
}

// storedFact reads the latest version of a fact from the test base.
func storedFact(t *testing.T, id string) store.Fact {
	t.Helper()
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	f, err := s.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
