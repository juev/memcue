package main

import (
	"bytes"
	"context"
	"errors"
	"github.com/juev/memcue/internal/extract"
	"net/http"
	"net/http/httptest"
	"reflect"
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

func TestContentOnlyDreamFailedCallAppliesNothing(t *testing.T) {
	dir := env(t)
	_, duplicate, kept, _ := dreamBase(t)
	addFactForTest(t, "-kind", "rule", "-scope", "global", "Cobalt observatory rules apply everywhere.")
	before := factHistory(t, duplicate)
	fakeDream(t, mergeAnswer(duplicate, kept))
	fakeDreamRun(t, nil)
	good := dreamRunFn
	dreamRunFn = func(ctx context.Context, packet string) (extract.DreamReply, error) {
		if hasFacts(packetBlock(packet, "## Данные: область")) {
			return extract.DreamReply{}, errModel
		}
		return good(ctx, packet)
	}
	if err := runDream([]string{"-run", "-all", "-j", "2"}, &bytes.Buffer{}); !errors.Is(err, errModel) {
		t.Fatalf("failed scope call: %v", err)
	}
	after := factHistory(t, duplicate)
	if !reflect.DeepEqual(before, after) {
		t.Error("one failed call partially applied another call's answer")
	}
	recs := readDreamLog(t, dir)
	if len(recs) != 1 || len(recs[0].Calls) != 2 {
		t.Errorf("missing review/scope evidence: %+v", recs)
	}
}
