package main

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/juev/memcue/internal/extract"
	"github.com/juev/memcue/internal/store"
)

func TestNoteLabelReadsTheMigratedNote(t *testing.T) {
	for _, c := range []struct{ origin, kind, scope, note string }{
		{"_global 2026-10-19 (_global/feedback_dns_dual_stack.md)", "", "global", "feedback"},
		{"_global 2026-10-19 (_global/user_role.md)", "", "global", "user"},
		{"lists 2026-10-06 (lists/feedback_one_issue_one_branch.md)", "", "repo:lists", "feedback"},
		{"llm 2026-10-01 (llm/negative_sonnet_extraction.md)", "pitfall", "repo:llm", "negative"},
		{"server-migration 2026-09-24 (server-migration/reference_caddy.md)", "environment", "repo:server-migration",
			"reference"},
		{"_global 2026-10-19 (_global/project_memcue.md)", "", "global", "project"},
		{"llm 2026-10-19 (llm/byte-vs-bpe-experiment-status.md)", "", "repo:llm", "byte-vs-bpe-experiment-status.md"},
		{"_global 2026-10-19", "", "", ""},
		{"hermes 2026-10-19", "", "", ""},
	} {
		kind, scope, note := noteLabel(c.origin)
		if kind != c.kind || scope != c.scope || note != c.note {
			t.Errorf("%q: %q %q %q, want %q %q %q", c.origin, kind, scope, note, c.kind, c.scope, c.note)
		}
	}
}

// labelBase holds facts of each case: settled by the note, half settled by it, without a note, already labelled.
func labelBase(t *testing.T) map[string]store.Fact {
	t.Helper()
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	out := map[string]store.Fact{}
	for name, f := range map[string]struct{ content, origin string }{
		"negative": {"Gradle вне make требует JAVA_HOME.", "_global 2026-10-19 (_global/negative_gradle.md)"},
		"project":  {"memcue хранит базу в ~/.claude/memory/facts.db.", "_global 2026-10-19 (_global/project_memcue.md)"},
		"feedback": {"При работе с DNS всегда создавать A и AAAA.",
			"_global 2026-10-19 (_global/feedback_dns_dual_stack.md)"},
		"bare": {"Hermes держит обёртку в /opt/data/bin/memcue.", "hermes 2026-10-19"},
	} {
		got, err := s.Add(f.content, nil, f.origin)
		if err != nil {
			t.Fatal(err)
		}
		out[name] = got
	}
	labelled, err := s.AddWith("Ветка на каждый issue.", nil, "lists 2026-10-06", "",
		store.Meta{Kind: "decision", Scope: "repo:lists"})
	if err != nil {
		t.Fatal(err)
	}
	out["labelled"] = labelled
	return out
}

func getFact(t *testing.T, id string) store.Fact {
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

// fakeLabels stands in for the model: it answers with the given labels and counts the facts it was asked about.
func fakeLabels(t *testing.T, answer func(input string) []extract.Label) *int {
	t.Helper()
	asked := 0
	old := labelFn
	labelFn = func(_ context.Context, input string) (extract.LabelResult, error) {
		asked += strings.Count(input, `"id":`)
		return extract.LabelResult{Labels: answer(input)}, nil
	}
	t.Cleanup(func() { labelFn = old })
	return &asked
}

var reRunFile = regexp.MustCompile(`run file: (\S+)`)

func TestLabelFillsFromNotesThenModelAndReverts(t *testing.T) {
	env(t)
	facts := labelBase(t)
	var hinted bool
	asked := fakeLabels(t, func(input string) []extract.Label {
		hinted = strings.Contains(input, `"note":"feedback"`)
		return []extract.Label{
			{ID: facts["project"].ID, Kind: "environment", Scope: "host:mac"}, // the note's scope wins
			{ID: facts["feedback"].ID, Kind: "rule", Scope: "repo:dns"},       // the note's scope wins
			{ID: facts["bare"].ID, Kind: "environment", Scope: "host:loffe", Expires: "2026-12-01"},
			{ID: "01UNKNOWN", Kind: "rule", Scope: "global"},
		}
	})

	var out bytes.Buffer
	if err := runLabel(nil, &out); err != nil {
		t.Fatal(err)
	}
	if *asked != 3 {
		t.Errorf("the model was asked about %d facts, want the 3 the notes left open", *asked)
	}
	if !hinted {
		t.Error("the model was not told the fact came from a feedback note")
	}
	for name, want := range map[string][3]string{
		"negative": {"pitfall", "global", ""},
		"feedback": {"rule", "global", ""},
		"project":  {"environment", "global", ""},
		"bare":     {"environment", "host:loffe", "2026-12-01"},
		"labelled": {"decision", "repo:lists", ""},
	} {
		f := getFact(t, facts[name].ID)
		if f.Kind != want[0] || f.Scope != want[1] || f.Expires != want[2] {
			t.Errorf("%s: %q %q %q, want %v", name, f.Kind, f.Scope, f.Expires, want)
		}
		if !f.Created.Equal(facts[name].Created) || f.Content != facts[name].Content {
			t.Errorf("%s changed its date or text: %+v", name, f)
		}
	}
	if f := getFact(t, facts["labelled"].ID); f.Version != facts["labelled"].Version {
		t.Errorf("a labelled fact got a version: %+v", f)
	}
	if !strings.Contains(out.String(), "labelled 4 of 4 facts (notes 1, model 3)") {
		t.Errorf("report:\n%s", out.String())
	}

	m := reRunFile.FindStringSubmatch(out.String())
	if m == nil {
		t.Fatalf("no run file in %q", out.String())
	}
	// A fact edited after the run keeps the edit; the others go back.
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Correct(facts["bare"].ID, "Hermes держит обёртку memcue в /opt/data/bin.", nil); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	out.Reset()
	if err := runLabel([]string{"-revert", m[1]}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "reverted 3 facts, kept 1") {
		t.Errorf("revert:\n%s", out.String())
	}
	if f := getFact(t, facts["negative"].ID); f.Kind != "" || f.Scope != "" {
		t.Errorf("revert left %+v", f)
	}
	if f := getFact(t, facts["bare"].ID); f.Kind != "environment" || !strings.Contains(f.Content, "обёртку memcue") {
		t.Errorf("revert undid a later edit: %+v", f)
	}
}

func TestLabelDryRunAndNoModel(t *testing.T) {
	env(t)
	facts := labelBase(t)
	asked := fakeLabels(t, func(string) []extract.Label { return nil })

	var out bytes.Buffer
	if err := runLabel([]string{"-dry-run"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "4 facts lack a kind or a scope: the notes settle 1, the model would get 3") {
		t.Errorf("dry run:\n%s", out.String())
	}
	if f := getFact(t, facts["negative"].ID); f.Version != facts["negative"].Version {
		t.Errorf("a dry run wrote %+v", f)
	}

	out.Reset()
	if err := runLabel([]string{"-no-model"}, &out); err != nil {
		t.Fatal(err)
	}
	if *asked != 0 {
		t.Errorf("-no-model asked the model about %d facts", *asked)
	}
	if f := getFact(t, facts["negative"].ID); f.Kind != "pitfall" || f.Scope != "global" {
		t.Errorf("negative %+v", f)
	}
	if f := getFact(t, facts["feedback"].ID); f.Kind != "" || f.Scope != "global" {
		t.Errorf("a feedback fact got its kind without the model: %+v", f)
	}
	// The note settles the scope of a project fact even without the model; its kind waits for the model.
	if f := getFact(t, facts["project"].ID); f.Kind != "" || f.Scope != "global" {
		t.Errorf("project %+v", f)
	}
	if f := getFact(t, facts["bare"].ID); f.Version != facts["bare"].Version {
		t.Errorf("a fact without a note changed without the model: %+v", f)
	}
}
