package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/juev/memcue/internal/store"
)

const gradleFact = "Gradle вне make требует JAVA_HOME=/opt/homebrew/opt/openjdk@21, иначе сборка не находит JDK."

// An explicit add is active at once and says the user stated it; a candidate is not recalled until it is approved.
func TestAddCandidateApprove(t *testing.T) {
	dir := env(t)
	var out bytes.Buffer
	if err := runAdd([]string{"-candidate", "-source", "memory", "-kind", "environment", "-keywords", "gradle,jdk",
		gradleFact}, &out); err != nil {
		t.Fatal(err)
	}
	id := strings.TrimSpace(out.String())
	if ctx := hookContext(t, dir, "s1", gradlePrompt); strings.Contains(ctx, "openjdk@21") {
		t.Fatalf("a candidate was recalled: %q", ctx)
	}
	out.Reset()
	if err := runCandidates([]string{"-json"}, &out); err != nil {
		t.Fatal(err)
	}
	var cs []candidateFact
	if err := json.Unmarshal(out.Bytes(), &cs); err != nil || len(cs) != 1 || cs[0].ID != id ||
		cs[0].Source != "memory" || cs[0].Kind != "environment" {
		t.Fatalf("candidates %q: %v", out.String(), err)
	}
	out.Reset()
	if err := runApprove([]string{id}, &out); err != nil || !strings.HasPrefix(out.String(), id+" v2") {
		t.Fatalf("approve %q: %v", out.String(), err)
	}
	if ctx := hookContext(t, dir, "s2", gradlePrompt); !strings.Contains(ctx, "openjdk@21") {
		t.Errorf("an approved fact is not recalled: %q", ctx)
	}
	if err := runApprove([]string{id}, &bytes.Buffer{}); err == nil {
		t.Error("approving an active fact again succeeded")
	}

	out.Reset()
	if err := runAdd([]string{"Пользователь пишет коммиты по-английски."}, &out); err != nil {
		t.Fatal(err)
	}
	s, _ := store.Open(factsPath())
	defer func() { _ = s.Close() }()
	if f, _ := s.Get(strings.TrimSpace(out.String())); f.Review != "" || f.Source != "user" {
		t.Errorf("explicit add %+v", f)
	}
	if err := runAdd([]string{"-source", "boss", "x"}, &bytes.Buffer{}); err == nil {
		t.Error("an unknown source was accepted")
	}
}

// The facts of extract -dry-run go through review when imported; the facts of a note migration do not.
func TestImportOfSessionFactsMakesCandidates(t *testing.T) {
	dir := env(t)
	path := filepath.Join(dir, "x.json")
	body := `{"note":"n","origin":"app 2026-10-20","facts":[` +
		`{"content":"Факт сессии про db1.","keywords":["db1"],"kind":"environment","scope":"host:db1","source":"tool","evidence":"db1"},` +
		`{"content":"Факт заметки про db2.","keywords":["db2"]}]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runImport([]string{path}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	s, _ := store.Open(factsPath())
	defer func() { _ = s.Close() }()
	cs, _ := s.Candidates()
	act, _ := s.Active()
	if len(cs) != 1 || !strings.Contains(cs[0].Content, "сессии") || len(act) != 1 || !strings.Contains(act[0].Content, "заметки") {
		t.Errorf("candidates %+v active %+v", cs, act)
	}
}
