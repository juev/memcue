package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

func TestHookLogsEveryPrompt(t *testing.T) {
	dir := env(t)
	seed(t)
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

func TestHookLogOff(t *testing.T) {
	dir := env(t)
	seed(t)
	t.Setenv("MEMCUE_RECALL_LOG", "0")
	if err := runHook(strings.NewReader(`{"prompt":"gradle JAVA_HOME JDK make"}`), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "state", "memcue", "recall.log")); !os.IsNotExist(err) {
		t.Errorf("log written: %v", err)
	}
}
