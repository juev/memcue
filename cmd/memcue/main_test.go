package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/juev/memcue/internal/recall"
	"github.com/juev/memcue/internal/store"
)

func env(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("MEMCUE_FACTS", filepath.Join(dir, "facts.db"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(dir, "claude"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
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
	logRecall("s1", dir, "прокси", hits, time.Millisecond)
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

func TestAddNamesWorkingRepositoryByDefault(t *testing.T) {
	dir := env(t)
	repo := filepath.Join(dir, "shop")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
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
	if want := "shop " + time.Now().Format(time.DateOnly); f.Origin != want {
		t.Errorf("origin %q, want %q", f.Origin, want)
	}
	all, _ := s.Active()
	for _, a := range all {
		if a.Content == "факт с origin" && a.Origin != "x 2026-01-01" {
			t.Errorf("explicit origin replaced: %q", a.Origin)
		}
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
