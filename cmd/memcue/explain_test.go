package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/juev/memcue/internal/recall"
	"github.com/juev/memcue/internal/store"
)

// lexicalOnly turns the judge and the embedder off, whatever the developer's environment holds.
func lexicalOnly(t *testing.T) {
	t.Helper()
	for _, k := range []string{"MEMCUE_JUDGE_URL", "MEMCUE_JUDGE_MODEL", "MEMCUE_EMBED_URL", "MEMCUE_EMBED_MODEL"} {
		t.Setenv(k, "")
	}
}

// gradleID returns the id of the fact seed writes.
func gradleID(t *testing.T) string {
	t.Helper()
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	facts, err := s.Active()
	if err != nil || len(facts) != 1 {
		t.Fatalf("facts = %v, %v", facts, err)
	}
	return facts[0].ID
}

func TestRecallExplainPrintsEveryStep(t *testing.T) {
	env(t)
	lexicalOnly(t)
	seed(t)
	var out bytes.Buffer
	if err := runRecall([]string{"-explain", "gradle без make, какой JAVA_HOME для JDK"}, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"terms: ", "gate: ", "lexical:\n  passed", "fallback to the words: no judge configured",
		"final:\n  served", gradleID(t)} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}

func TestRecallExplainFactNamesWhereItWasLost(t *testing.T) {
	env(t)
	lexicalOnly(t)
	seed(t)
	id := gradleID(t)

	var hit bytes.Buffer
	if err := runRecall([]string{"-fact", id, "gradle без make, какой JAVA_HOME для JDK"}, &hit); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"fact " + id + " (active)", "lexical: passed", "final: served"} {
		if !strings.Contains(hit.String(), want) {
			t.Errorf("hit lacks %q:\n%s", want, hit.String())
		}
	}

	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add("Публичный Caddy перечитывает конфиг только с полным путём.", nil, "infra 2026-10-01"); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	var miss bytes.Buffer
	if err := runRecall([]string{"-fact", id, "caddy перечитывает конфиг, а gradle?"}, &miss); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"lexical: too few terms", "final: not reached"} {
		if !strings.Contains(miss.String(), want) {
			t.Errorf("miss lacks %q:\n%s", want, miss.String())
		}
	}
}

func TestRecallExplainFactRejectsAnUnknownID(t *testing.T) {
	env(t)
	lexicalOnly(t)
	seed(t)
	if err := runRecall([]string{"-fact", "nope", "gradle jdk"}, &bytes.Buffer{}); err == nil {
		t.Error("an unknown id gave no error")
	}
}

// A fact the vector reader never compared is told apart: outside the pool of recent vectors, or with no vector.
func TestVectorPlaceTellsThePoolFromAMissingVector(t *testing.T) {
	env(t)
	seed(t)
	t.Setenv("MEMCUE_EMBED_URL", "http://127.0.0.1:1/api")
	t.Setenv("MEMCUE_EMBED_MODEL", "test-model")
	id := gradleID(t)
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	// The judge was asked about something else, so the vector reader ran and did not read this fact.
	tr := &recall.Trace{VectorPool: 0, Judge: []recall.Step{{ID: "other"}}}

	got, err := vectorPlace(s, tr, id)
	if err != nil || !strings.HasPrefix(got, "no vector for ") {
		t.Errorf("without a vector: %q, %v", got, err)
	}

	f, err := s.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveVector(id, embedder().Tag(), store.TextHash(f.Content), []float32{1, 0}); err != nil {
		t.Fatal(err)
	}
	got, err = vectorPlace(s, tr, id)
	if err != nil || !strings.HasPrefix(got, "outside the pool") {
		t.Errorf("with a vector: %q, %v", got, err)
	}
}
