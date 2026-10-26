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

// searched is what memcue search prints for query.
func searched(t *testing.T, query string) string {
	t.Helper()
	var out bytes.Buffer
	if err := runSearch([]string{query}, &out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// An explicit add is active at once and says the user stated it; a candidate is not searched until it is approved.
func TestAddCandidateApprove(t *testing.T) {
	env(t)
	var out bytes.Buffer
	if err := runAdd([]string{"-candidate", "-source", "memory", "-kind", "environment", "-keywords", "gradle,jdk",
		gradleFact}, &out); err != nil {
		t.Fatal(err)
	}
	id := strings.TrimSpace(out.String())
	if ctx := searched(t, gradlePrompt); strings.Contains(ctx, "openjdk@21") {
		t.Fatalf("a candidate was found: %q", ctx)
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
	if ctx := searched(t, gradlePrompt); !strings.Contains(ctx, "openjdk@21") {
		t.Errorf("an approved fact is not found: %q", ctx)
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

// The dry-run record says whether its facts go through review; the kind test is only a fallback for old files.
func TestImportUsesTheReviewFlag(t *testing.T) {
	dir := env(t)
	path := filepath.Join(dir, "x.json")
	body := `{"note":"note","origin":"app 2026-10-20","review":false,"facts":[{"content":"Факт заметки.","keywords":["note"],"kind":"environment"}]}` + "\n" +
		`{"note":"session","origin":"app 2026-10-20","review":true,"facts":[{"content":"Факт сессии без kind.","keywords":["session"]}]}` + "\n" +
		`{"note":"old","origin":"app 2026-10-20","facts":[{"content":"Старый файл с kind.","keywords":["old"],"kind":"environment"}]}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runImport([]string{path}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	active, _ := s.Active()
	candidates, _ := s.Candidates()
	if len(active) != 1 || !strings.Contains(active[0].Content, "заметки") {
		t.Errorf("active %+v", active)
	}
	if len(candidates) != 2 {
		t.Fatalf("candidates %+v", candidates)
	}
	got := map[string]bool{}
	for _, f := range candidates {
		got[f.Content] = true
	}
	if !got["Факт сессии без kind."] || !got["Старый файл с kind."] {
		t.Errorf("candidates %+v", candidates)
	}
}

// kind and scope are what put a fact on the session card, so add rejects values the card cannot use.
func TestAddValidatesKindAndScope(t *testing.T) {
	env(t)
	for _, args := range [][]string{
		{"-kind", "repo", "x"},
		{"-kind", "planet", "x"},
		{"-scope", "planet", "x"},
		{"-scope", "host:", "x"},
		{"-scope", "repo:a b", "x"},
	} {
		if err := runAdd(args, &bytes.Buffer{}); err == nil {
			t.Errorf("add accepted %v", args)
		}
	}
	var out bytes.Buffer
	if err := runAdd([]string{"-kind", "rule", "-scope", "global", "Всегда отвечай по-русски."}, &out); err != nil {
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
	if f.Kind != "rule" || f.Scope != "global" {
		t.Errorf("stored %+v", f)
	}
}
