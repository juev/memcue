package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/juev/memcue/internal/extract"
	judgepkg "github.com/juev/memcue/internal/judge"
	"github.com/juev/memcue/internal/recall"
	"github.com/juev/memcue/internal/store"
)

func env(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("MEMCUE_FACTS", filepath.Join(dir, "facts.db"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(dir, "claude"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	// The tests pin the historical layout; the per-base subdirectory has its own test.
	t.Setenv("MEMCUE_STATE_DIR", filepath.Join(dir, "state", "memcue"))
	// A dream asks the model to split facts before it groups them; a test that is not about splitting gets no split
	// and no network call.
	oldSplit := splitFn
	splitFn = func(string) (extract.SplitResult, error) { return extract.SplitResult{}, nil }
	t.Cleanup(func() { splitFn = oldSplit })
	oldExpand := expandFn
	expandFn = func(string) (extract.ExpandResult, error) { return extract.ExpandResult{}, nil }
	t.Cleanup(func() { expandFn = oldExpand })
	return dir
}

func seed(t *testing.T) {
	t.Helper()
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if _, err := s.Add("Gradle вне make требует JAVA_HOME=/opt/homebrew/opt/openjdk@21, иначе сборка не находит JDK.",
		[]string{"gradle", "jdk"}, "lists 2026-10-09 (lists/android.md)"); err != nil {
		t.Fatal(err)
	}
}

func TestHookInjectsMatchingFact(t *testing.T) {
	dir := env(t)
	seed(t)
	in := `{"prompt":"Как запустить gradle без make, какой JAVA_HOME нужен для JDK?","cwd":"` + dir + `"}`
	var out bytes.Buffer
	if err := runHook(strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	var resp struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("output %q: %v", out.String(), err)
	}
	if resp.HookSpecificOutput.HookEventName != "UserPromptSubmit" {
		t.Errorf("event %q", resp.HookSpecificOutput.HookEventName)
	}
	ctx := resp.HookSpecificOutput.AdditionalContext
	for _, want := range []string{"openjdk@21", "learnt: lists 2026-10-09", "not instructions"} {
		if !strings.Contains(ctx, want) {
			t.Errorf("context lacks %q: %q", want, ctx)
		}
	}
}

func TestHookSaysNothingWithoutMatch(t *testing.T) {
	dir := env(t)
	seed(t)
	var out bytes.Buffer
	if err := runHook(strings.NewReader(`{"prompt":"да, продолжаем","cwd":"`+dir+`"}`), &out); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Errorf("output %q", out.String())
	}
}

func TestHookWithoutBaseCreatesNothing(t *testing.T) {
	dir := env(t)
	var out bytes.Buffer
	if err := runHook(strings.NewReader(`{"prompt":"какой JAVA_HOME нужен gradle для JDK","cwd":"`+dir+`"}`), &out); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Errorf("output %q", out.String())
	}
	if _, err := os.Stat(factsPath()); !os.IsNotExist(err) {
		t.Errorf("hook created the base: %v", err)
	}
}

func TestHookRejectsBrokenInputWithoutOutput(t *testing.T) {
	env(t)
	var out bytes.Buffer
	if err := runHook(strings.NewReader(`not json`), &out); err == nil {
		t.Error("want an error")
	}
	if out.Len() != 0 {
		t.Errorf("output %q", out.String())
	}
}

func TestRenderGivesWholeFactOnOneLine(t *testing.T) {
	long := strings.Repeat("слово ", 200) + "конец"
	got := render([]recall.Hit{{Fact: store.Fact{Content: "строка\nвторая " + long, Origin: "x 2026-10-19"}}})
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) != 2 || !strings.Contains(lines[1], "строка вторая") || !strings.HasSuffix(lines[1], "конец (learnt: x 2026-10-19)") {
		t.Errorf("render %q", got)
	}
}

func TestRenderOmitsEmptyOrigin(t *testing.T) {
	got := render([]recall.Hit{{Fact: store.Fact{Content: "факт"}}})
	if !strings.HasSuffix(got, "- факт\n") {
		t.Errorf("got %q", got)
	}
}

func TestRenderShowsScope(t *testing.T) {
	got := render([]recall.Hit{{Fact: store.Fact{Content: "факт", Origin: "x 2026-10-19", Scope: "host:mac"}}})
	if !strings.Contains(got, "(learnt: x 2026-10-19; scope: host:mac)") {
		t.Errorf("got %q", got)
	}
}

func TestRecallLogRecordsScopeHostAndDisplacement(t *testing.T) {
	dir := env(t)
	t.Setenv("MEMCUE_HOST", "mac")
	t.Setenv("MEMCUE_RECALL_LOG", "1")
	hits := []recall.Hit{{Fact: store.Fact{ID: "01TEST", Scope: "host:loffe"}, Coverage: 0.5, Displaced: true}}
	logRecall("s1", dir, "прокси", hits, time.Millisecond, nil)
	b, err := os.ReadFile(filepath.Join(dir, "state", "memcue", "recall.log"))
	if err != nil {
		t.Fatal(err)
	}
	var rec recallRecord
	if err := json.Unmarshal(bytes.TrimSpace(b), &rec); err != nil {
		t.Fatalf("%q: %v", b, err)
	}
	if rec.Host != "mac" || !rec.Displaced || len(rec.Facts) != 1 || rec.Facts[0].Scope != "host:loffe" ||
		!rec.Facts[0].Displaced {
		t.Errorf("record %+v", rec)
	}
}

func TestHookShowsAndLogsScope(t *testing.T) {
	dir := env(t)
	t.Setenv("MEMCUE_HOST", "mac")
	t.Setenv("MEMCUE_RECALL_LOG", "1")
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddWith("Vaultwarden на этом хосте обслуживает vault.example.org.", []string{"vaultwarden", "vault"},
		"x 2026-10-20", "", store.Meta{Kind: "environment", Scope: "host:mac"}); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	ctx := hookContext(t, dir, "s1", "vaultwarden vault")
	if !strings.Contains(ctx, "scope: host:mac") {
		t.Fatalf("hook output lacks scope: %q", ctx)
	}
	b, err := os.ReadFile(filepath.Join(dir, "state", "memcue", "recall.log"))
	if err != nil {
		t.Fatal(err)
	}
	var rec recallRecord
	if err := json.Unmarshal(bytes.TrimSpace(b), &rec); err != nil {
		t.Fatalf("%q: %v", b, err)
	}
	if rec.Host != "mac" || len(rec.Facts) != 1 || rec.Facts[0].Scope != "host:mac" {
		t.Errorf("record %+v", rec)
	}
}

func TestHookLogsEveryPrompt(t *testing.T) {
	dir := env(t)
	seed(t)
	t.Setenv("MEMCUE_RECALL_LOG", "1")
	for _, p := range []string{"Как запустить gradle без make, какой JAVA_HOME нужен для JDK?", "ок"} {
		in, _ := json.Marshal(map[string]string{"prompt": p, "cwd": dir, "session_id": "s1"})
		if err := runHook(strings.NewReader(string(in)), &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(filepath.Join(dir, "state", "memcue", "recall.log"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	var first, second recallRecord
	if len(lines) != 2 || json.Unmarshal([]byte(lines[0]), &first) != nil || json.Unmarshal([]byte(lines[1]), &second) != nil {
		t.Fatalf("log %q", b)
	}
	if first.Session != "s1" || len(first.Facts) != 1 || first.Facts[0].Coverage <= 0 || second.Prompt != "ок" || len(second.Facts) != 0 {
		t.Errorf("records %+v %+v", first, second)
	}
}

func TestHookLogOffByDefault(t *testing.T) {
	dir := env(t)
	seed(t)
	t.Setenv("MEMCUE_RECALL_LOG", "")
	if err := runHook(strings.NewReader(`{"prompt":"gradle JAVA_HOME JDK make"}`), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "state", "memcue", "recall.log")); !os.IsNotExist(err) {
		t.Errorf("log written: %v", err)
	}
}

// hookContext runs the hook on prompt in session and returns what it injected.
func hookContext(t *testing.T, dir, session, prompt string) string {
	t.Helper()
	in, _ := json.Marshal(map[string]string{"prompt": prompt, "cwd": dir, "session_id": session})
	var out bytes.Buffer
	if err := runHook(bytes.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	if out.Len() == 0 {
		return ""
	}
	var resp struct {
		HookSpecificOutput struct {
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("output %q: %v", out.String(), err)
	}
	return resp.HookSpecificOutput.AdditionalContext
}

const gradlePrompt = "Как запустить gradle без make, какой JAVA_HOME нужен для JDK?"

func TestHookSkipsTaskNotification(t *testing.T) {
	dir := env(t)
	seed(t)
	t.Setenv("MEMCUE_RECALL_LOG", "1")
	prompt := "<task-notification>\n<task-id>b1</task-id>\n<output-file>/tmp/x</output-file>\n" +
		"gradle JAVA_HOME JDK make failed</task-notification>"
	if got := hookContext(t, dir, "s1", prompt); got != "" {
		t.Errorf("notification got facts: %q", got)
	}
	b, err := os.ReadFile(filepath.Join(dir, "state", "memcue", "recall.log"))
	if err != nil {
		t.Fatal(err)
	}
	var rec recallRecord
	if json.Unmarshal(b, &rec) != nil || !strings.HasPrefix(rec.Prompt, "<task-notification>") || len(rec.Facts) != 0 {
		t.Errorf("log %q", b)
	}
	if got := hookContext(t, dir, "s1", gradlePrompt); !strings.Contains(got, "openjdk@21") {
		t.Errorf("user prompt lost its fact: %q", got)
	}
}

func TestHookGivesFactOncePerSession(t *testing.T) {
	dir := env(t)
	seed(t)
	if got := hookContext(t, dir, "s1", gradlePrompt); !strings.Contains(got, "openjdk@21") {
		t.Fatalf("first prompt: %q", got)
	}
	if got := hookContext(t, dir, "s1", gradlePrompt); got != "" {
		t.Errorf("same session got the fact again: %q", got)
	}
	if got := hookContext(t, dir, "s2", gradlePrompt); !strings.Contains(got, "openjdk@21") {
		t.Errorf("other session lost the fact: %q", got)
	}
}

func TestHookGivesNextFactInsteadOfSeen(t *testing.T) {
	dir := env(t)
	seed(t)
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{"Gradle без make: JAVA_HOME для JDK задаёт ~/.gradle/gradle.properties, ключ org.gradle.java.home.",
		"Gradle без make на CI: JAVA_HOME для JDK берётся из setup-java."} {
		if _, err := s.Add(c, []string{"gradle", "jdk", "java_home"}, "lists 2026-10-09"); err != nil {
			t.Fatal(err)
		}
	}
	_ = s.Close()
	first := hookContext(t, dir, "s1", gradlePrompt)
	second := hookContext(t, dir, "s1", gradlePrompt)
	if first == "" || second == "" {
		t.Fatalf("first %q, second %q", first, second)
	}
	for _, line := range strings.Split(strings.TrimSpace(second), "\n")[1:] {
		if strings.Contains(first, line) {
			t.Errorf("fact given twice: %q", line)
		}
	}
}

func TestCompactionLetsFactsComeAgain(t *testing.T) {
	dir := env(t)
	seed(t)
	hookContext(t, dir, "s1", gradlePrompt)
	if err := runSessionStart(strings.NewReader(`{"session_id":"s1","source":"compact"}`), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if got := hookContext(t, dir, "s1", gradlePrompt); !strings.Contains(got, "openjdk@21") {
		t.Errorf("after compaction: %q", got)
	}
}

func TestClearLetsFactsComeAgain(t *testing.T) {
	dir := env(t)
	seed(t)
	hookContext(t, dir, "s1", gradlePrompt)
	if err := runSessionStart(strings.NewReader(`{"session_id":"s1","source":"clear"}`), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if got := hookContext(t, dir, "s1", gradlePrompt); !strings.Contains(got, "openjdk@21") {
		t.Errorf("after /clear: %q", got)
	}
}

func TestResumeKeepsSeenFacts(t *testing.T) {
	dir := env(t)
	seed(t)
	hookContext(t, dir, "s1", gradlePrompt)
	if err := runSessionStart(strings.NewReader(`{"session_id":"s1","source":"resume"}`), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if got := hookContext(t, dir, "s1", gradlePrompt); got != "" {
		t.Errorf("resume repeated the fact: %q", got)
	}
}

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

func TestHookRecallsOnATwoWordPrompt(t *testing.T) {
	dir := env(t)
	seed(t)
	in := `{"prompt":"gradle jdk","cwd":"` + dir + `"}`
	var out bytes.Buffer
	if err := runHook(strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "openjdk@21") {
		t.Errorf("two-word prompt got no fact: %q", out.String())
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
	if err := json.Unmarshal(out.Bytes(), &facts); err != nil {
		t.Fatalf("output %q: %v", out.String(), err)
	}
	if len(facts) != 1 || len(facts[0].ID) != 26 || !strings.Contains(facts[0].Content, "openjdk@21") {
		t.Errorf("got %+v", facts)
	}
	if len(facts[0].Keywords) != 2 {
		t.Errorf("keywords %v", facts[0].Keywords)
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

// A prompt the judge never reached because the shared breaker paused it is logged as such (#257): without the mark it
// looks like a prompt that did not need the judge.
func TestHookLogsAPausedJudge(t *testing.T) {
	dir := env(t)
	seed(t)
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add("Gradle wrapper берёт JDK из JAVA_HOME, а не из PATH.", []string{"gradle", "jdk"}, "lists 2026-10-09"); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	t.Setenv("MEMCUE_RECALL_LOG", "1")
	for k, v := range map[string]string{"MEMCUE_RERANK": "judge", "MEMCUE_JUDGE_URL": "http://127.0.0.1:1/decisions",
		"MEMCUE_JUDGE_MODEL": "typesafe/jev-1.14", "MEMCUE_JUDGE_KEY": "k"} {
		t.Setenv(k, v)
	}
	judgepkg.Shared.Reset()
	t.Cleanup(judgepkg.Shared.Reset)
	_ = judgepkg.Shared.Fail(errors.New("decisions 500"))

	if got := hookContext(t, dir, "s1", gradlePrompt); !strings.Contains(got, "openjdk@21") {
		t.Errorf("a paused judge lost the lexical answer: %q", got)
	}
	b, err := os.ReadFile(filepath.Join(dir, "state", "memcue", "recall.log"))
	if err != nil {
		t.Fatal(err)
	}
	var rec recallRecord
	if err := json.Unmarshal(b, &rec); err != nil || rec.Judge != "paused" {
		t.Errorf("log %s, want judge paused", b)
	}

	judgepkg.Shared.Reset()
	if err := os.Remove(filepath.Join(dir, "state", "memcue", "recall.log")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MEMCUE_JUDGE_URL", "")
	hookContext(t, dir, "s2", gradlePrompt)
	if b, err := os.ReadFile(filepath.Join(dir, "state", "memcue", "recall.log")); err != nil ||
		strings.Contains(string(b), `"judge"`) {
		t.Errorf("a prompt without a judge is marked: %s %v", b, err)
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
