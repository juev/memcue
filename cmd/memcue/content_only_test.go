package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestContentOnlyCLIRejectsAuxiliary(t *testing.T) {
	env(t)
	id := addFactForTest(t, "Original cobalt observatory.")
	for _, args := range [][]string{
		{"-keywords", "saffron", "--", "Unwanted fact."},
		{"-queries", "marigold", "--", "Unwanted fact."},
		{"Unwanted fact.", "-keywords", "saffron"},
	} {
		if err := runAdd(args, &bytes.Buffer{}); err == nil {
			t.Errorf("add accepted obsolete auxiliary args: %q", args)
		}
	}
	for _, args := range [][]string{
		{"-keywords", "saffron", id, "Unwanted correction."},
		{id, "-keywords", "saffron", "Unwanted correction."},
		{id, "-queries=marigold", "Unwanted correction."},
	} {
		if err := runCorrect(args, &bytes.Buffer{}); err == nil {
			t.Errorf("correct accepted obsolete auxiliary args: %q", args)
		}
	}
	if f := storedFact(t, id); f.Content != "Original cobalt observatory." {
		t.Errorf("refused auxiliary request changed content: %q", f.Content)
	}
	var out bytes.Buffer
	if err := runSearch([]string{"-json", "cobalt"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), id) {
		t.Error("content search lost original fact")
	}
}

func TestContentOnlyWritesNeverCallModel(t *testing.T) {
	env(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "unexpected model call", 500)
	}))
	defer server.Close()
	withAPI(t, server.URL)
	id := addFactForTest(t, "Cobalt observatory PROJ-1234 uses protocol 1.2.3.")
	if storedFact(t, id).Version != 1 {
		t.Error("add wrote auxiliary versions")
	}
	if err := runCorrect([]string{id, "--", "Обсерватория PROJ-1234 использует протокол 1.2.4."}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	f := storedFact(t, id)
	if f.Version != 2 || f.Content != "Обсерватория PROJ-1234 использует протокол 1.2.4." {
		t.Errorf("correction: %+v", f)
	}
	var out bytes.Buffer
	if err := runSearch([]string{"-json", "PROJ-1234"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), id) {
		t.Error("identifier not immediately searchable")
	}
	if calls.Load() != 0 {
		t.Errorf("writes made %d implicit inference calls", calls.Load())
	}
}
