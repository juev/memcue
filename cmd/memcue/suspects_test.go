package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/juev/memcue/internal/extract"
	"github.com/juev/memcue/internal/store"
)

// Every MEMCUE_* variable the source reads is known, or a fact naming a live variable would look stale.
func TestKnownEnvCoversTheSource(t *testing.T) {
	getenv := regexp.MustCompile(`Getenv\("(MEMCUE_[A-Z0-9_]+)"\)`)
	known := map[string]bool{}
	for _, e := range knownEnv {
		known[e] = true
	}
	for _, root := range []string{".", "../../internal"} {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, m := range getenv.FindAllStringSubmatch(string(b), -1) {
				if !known[m[1]] {
					t.Errorf("%s reads %s, which knownEnv lacks", path, m[1])
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestKnownSelfReadsTheHelp(t *testing.T) {
	k := knownSelf()
	for _, c := range []string{"add", "dream", "vet", "reopen", "extract-messages", "version"} {
		if !k.Commands[c] {
			t.Errorf("command %q missing", c)
		}
	}
	for _, c := range []string{"label", "embed", "review", "audit", "environment:", "MEMCUE_FACTS"} {
		if k.Commands[c] {
			t.Errorf("%q taken for a command", c)
		}
	}
}

// fakeSuspects answers the suspects call by the fact text: "устар" retires, anything else is kept; it records the ids.
func fakeSuspects(t *testing.T, fail error) *[]string {
	t.Helper()
	var asked []string
	old := suspectFn
	suspectFn = func(_ context.Context, input string) (extract.SuspectResult, error) {
		if fail != nil {
			return extract.SuspectResult{}, fail
		}
		var r extract.SuspectResult
		for _, part := range strings.Split(input, `"id":"`)[1:] {
			id := part[:strings.Index(part, `"`)]
			asked = append(asked, id)
			verdict := extract.KeepSuspect
			if strings.Contains(part, "устар") {
				verdict = extract.RetireSuspect
			}
			r.Decisions = append(r.Decisions, extract.SuspectDecision{ID: id, Verdict: verdict, Reason: "test"})
		}
		return r, nil
	}
	t.Cleanup(func() { suspectFn = old })
	return &asked
}

func suspectBase(t *testing.T) (stale, history, plain string) {
	t.Helper()
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	add := func(c string) string {
		f, err := s.Add(c, nil, "x 2026-10-19")
		if err != nil {
			t.Fatal(err)
		}
		return f.ID
	}
	stale = add("Разметку делает `memcue label`, это устаревшее описание.")
	history = add("Команду `memcue embed` убрали в 0.5.0 вместе с векторами.")
	plain = add("Postgres демо-базы слушает порт 5432 на db1.")
	return
}

// The model retires the suspect that describes a past state as current and keeps the record of history; both are
// remembered, so the next run asks about neither, and a fact that names nothing removed is never asked about.
func TestVetAsksAboutSuspects(t *testing.T) {
	env(t)
	stale, history, plain := suspectBase(t)
	asked := fakeSuspects(t, nil)
	var out bytes.Buffer
	if err := runVet(nil, &out); err != nil {
		t.Fatal(err)
	}
	if got := unique(*asked); len(got) != 2 || slices.Contains(got, plain) {
		t.Fatalf("asked %q, want the two suspects", *asked)
	}
	if f := vetStatus(t, stale); f.Status != store.Retired {
		t.Errorf("stale fact %+v", f)
	}
	if f := vetStatus(t, history); f.Status != store.Active {
		t.Errorf("history fact %+v", f)
	}
	if !strings.Contains(out.String(), "Подозрительных одобренных фактов 2: снято 1") {
		t.Errorf("out %q", out.String())
	}
	*asked = nil
	if err := runVet(nil, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if len(*asked) != 0 {
		t.Errorf("asked again: %q", *asked)
	}
}

// A failed call retires and remembers nothing, so the next run asks again; it is reported as the run's error.
func TestVetSuspectsFailureIsRetried(t *testing.T) {
	env(t)
	stale, _, _ := suspectBase(t)
	fakeSuspects(t, errors.New("endpoint down"))
	if err := runVet(nil, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "endpoint down") {
		t.Fatalf("err %v", err)
	}
	if f := vetStatus(t, stale); f.Status != store.Active {
		t.Errorf("retired on a failed call: %+v", f)
	}
	asked := fakeSuspects(t, nil)
	if err := runVet(nil, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if got := unique(*asked); len(got) != 2 {
		t.Errorf("next run asked %q", *asked)
	}
}

// A dry run asks but changes and remembers nothing; -no-model only lists the suspects.
func TestVetSuspectsDryRunAndNoModel(t *testing.T) {
	env(t)
	stale, _, _ := suspectBase(t)
	asked := fakeSuspects(t, nil)
	var out bytes.Buffer
	if err := runVet([]string{"-no-model"}, &out); err != nil {
		t.Fatal(err)
	}
	if len(*asked) != 0 || !strings.Contains(out.String(), "the model was off") {
		t.Fatalf("no-model: asked %q, out %q", *asked, out.String())
	}
	if err := runVet([]string{"-dry-run"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if f := vetStatus(t, stale); len(unique(*asked)) != 2 || f.Status != store.Active {
		t.Fatalf("dry run: asked %q, fact %+v", *asked, f)
	}
	*asked = nil
	if err := runVet(nil, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if len(unique(*asked)) != 2 || vetStatus(t, stale).Status != store.Retired {
		t.Errorf("after the dry run: asked %q", *asked)
	}
}

func unique(ids []string) []string {
	var out []string
	for _, id := range ids {
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

// A retire needs two agreeing answers: one call that says retire and a confirmation that says keep leaves the fact
// alone, says so in the log, and remembers it so the next run does not roll the dice again (#296).
func TestVetSuspectsRetireNeedsTwoAgreeingAnswers(t *testing.T) {
	env(t)
	stale, history, _ := suspectBase(t)
	old := suspectFn
	calls := 0
	suspectFn = func(_ context.Context, input string) (extract.SuspectResult, error) {
		calls++
		var r extract.SuspectResult
		for _, part := range strings.Split(input, `"id":"`)[1:] {
			id := part[:strings.Index(part, `"`)]
			verdict := extract.KeepSuspect
			if calls == 1 && strings.Contains(part, "устар") {
				verdict = extract.RetireSuspect
			}
			r.Decisions = append(r.Decisions, extract.SuspectDecision{ID: id, Verdict: verdict, Reason: "test"})
		}
		return r, nil
	}
	t.Cleanup(func() { suspectFn = old })

	var out bytes.Buffer
	if err := runVet(nil, &out); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("the model was asked %d times, want the batch and the confirmation", calls)
	}
	if f := vetStatus(t, stale); f.Status != store.Active {
		t.Errorf("retired on one answer: %+v", f)
	}
	if f := vetStatus(t, history); f.Status != store.Active {
		t.Errorf("history fact %+v", f)
	}
	if !strings.Contains(out.String(), "the second answer did not agree") {
		t.Errorf("out %q", out.String())
	}
}
