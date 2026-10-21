package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/juev/memcue/internal/store"
)

func TestAuditHeuristics(t *testing.T) {
	cases := []struct {
		name, content, want string
	}{
		{"russian date", "20 октября 2026 обновили demo до 0.3.11.", "event_date"},
		{"iso date", "2026-10-20 перезапустили контейнер demo.", "event_date"},
		{"far apart", "2026-10-20. " + strings.Repeat("слово ", 60) + "обновили demo.", ""},
		{"ci", "CI в freshgo гоняет четыре job.", "ci_makefile"},
		{"makefile", "Makefile: цель test запускает go test.", "ci_makefile"},
		{"durable", "На db1 PostgreSQL 18 слушает порт 5432.", ""},
	}
	for _, c := range cases {
		name, reason := auditFact(store.Fact{Content: c.content})
		if name != c.want {
			t.Errorf("%s: heuristic %q, want %q (reason %q)", c.name, name, c.want, reason)
		}
		if (name == "") != (reason == "") {
			t.Errorf("%s: reason %q with heuristic %q", c.name, reason, name)
		}
	}
	if name, _ := auditFact(store.Fact{Kind: "rule", Content: "CI перед merge."}); name != "" {
		t.Errorf("a curated rule was proposed: %q", name)
	}
}

func writeAuditLog(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "state", "memcue"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state", "memcue", name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readAuditPlan(t *testing.T, path string) auditPlan {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var plan auditPlan
	if err := json.Unmarshal(b, &plan); err != nil {
		t.Fatalf("%q: %v", b, err)
	}
	return plan
}

func TestAuditDryRunWritesPlanWithReasonsAndUsage(t *testing.T) {
	dir := env(t)
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	add := func(content, kind string) store.Fact {
		t.Helper()
		f, err := s.AddWith(content, nil, "x 2026-10-20", "", store.Meta{Kind: kind})
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	event := add("20 октября 2026 обновили demo до 0.3.11.", "")
	ci := add("CI в freshgo гоняет четыре job.", "")
	good := add("На db1 PostgreSQL 18 слушает порт 5432.", "")
	rule := add("Правило: CI зелёный перед merge.", "rule")
	_ = s.Close()
	writeAuditLog(t, dir, "recall.log", `{"time":"2026-10-20T10:00:00Z","facts":[{"id":"`+event.ID+`"}]}`+"\n")
	writeAuditLog(t, dir, "card.log", `{"time":"2026-10-20T11:00:00Z","facts":["`+ci.ID+`"]}`+"\n")

	planPath := filepath.Join(dir, "audit.json")
	var out strings.Builder
	if err := runAudit([]string{"-dry-run", "-plan", planPath}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "2 proposals") {
		t.Errorf("output %q", out.String())
	}
	plan := readAuditPlan(t, planPath)
	if len(plan.Proposals) != 2 || plan.MinPrecision != auditMinPrecision {
		t.Fatalf("plan %+v", plan)
	}
	byID := map[string]auditProposal{}
	for _, p := range plan.Proposals {
		byID[p.ID] = p
	}
	e, ok := byID[event.ID]
	if !ok || e.Heuristic != "event_date" || e.Reason == "" || e.LastRecalled != "2026-10-20T10:00:00Z" || e.Decision != "" {
		t.Errorf("event proposal %+v", e)
	}
	c, ok := byID[ci.ID]
	if !ok || c.Heuristic != "ci_makefile" || c.Reason == "" || c.Delivered != "2026-10-20T11:00:00Z" {
		t.Errorf("ci proposal %+v", c)
	}
	if _, ok := byID[good.ID]; ok {
		t.Error("a durable fact was proposed")
	}
	if _, ok := byID[rule.ID]; ok {
		t.Error("a curated rule was proposed")
	}
	if len(plan.Heuristics) != 2 || plan.Heuristics[0].Proposed != 1 || plan.Heuristics[1].Proposed != 1 {
		t.Errorf("heuristics %+v", plan.Heuristics)
	}
	s, err = store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if act, _ := s.Active(); len(act) != 4 {
		t.Errorf("dry-run changed the base: %+v", act)
	}
}

func auditTestFact(t *testing.T, s *store.Store, content string) store.Fact {
	t.Helper()
	f, err := s.AddWith(content, nil, "x 2026-10-20", "", store.Meta{})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestAuditApplyUsesLabelledPrecision(t *testing.T) {
	dir := env(t)
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	a := auditTestFact(t, s, "20 октября 2026 обновили alpha.")
	b := auditTestFact(t, s, "21 октября 2026 обновили beta.")
	c := auditTestFact(t, s, "CI в beta гоняет job.")
	_ = s.Close()
	plan := auditPlan{Generated: "2026-10-20T12:00:00Z", MinPrecision: auditMinPrecision, Heuristics: []auditHeuristic{
		{Name: "event_date", Proposed: 2}, {Name: "ci_makefile", Proposed: 1}}, Proposals: []auditProposal{
		{ID: a.ID, Version: a.Version, Heuristic: "event_date", Reason: "r", Decision: "retire"},
		{ID: b.ID, Version: b.Version, Heuristic: "event_date", Reason: "r", Decision: "keep"},
		{ID: c.ID, Version: c.Version, Heuristic: "ci_makefile", Reason: "r", Decision: "retire"}}}
	path := filepath.Join(dir, "plan.json")
	writeJSON(t, path, plan)
	var out strings.Builder
	if err := runAudit([]string{"-apply", path}, &out); err != nil {
		t.Fatal(err)
	}
	s, err = store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	for _, id := range []string{a.ID, b.ID} {
		got, _ := s.Get(id)
		if got.Status != store.Active {
			t.Errorf("event fact %s was retired: %+v", id, got)
		}
	}
	if got, _ := s.Get(c.ID); got.Status != store.Retired {
		t.Errorf("CI fact is %s, want retired", got.Status)
	}
	bts, err := os.ReadFile(filepath.Join(dir, "state", "memcue", "audit.log"))
	if err != nil {
		t.Fatal(err)
	}
	var rec auditRecord
	if err := json.Unmarshal(bytesTrim(bts), &rec); err != nil {
		t.Fatalf("%q: %v", bts, err)
	}
	if rec.Applied != 1 || rec.Refused != 1 || len(rec.Heuristics) != 2 {
		t.Fatalf("record %+v", rec)
	}
	event, ci := rec.Heuristics[0], rec.Heuristics[1]
	if event.Name != "event_date" || event.Precision == nil || *event.Precision != 0.5 || event.Refused != 1 {
		t.Errorf("event summary %+v", event)
	}
	if ci.Name != "ci_makefile" || ci.Precision == nil || *ci.Precision != 1 || ci.Applied != 1 {
		t.Errorf("ci summary %+v", ci)
	}
}

// A partial review labels one proposal of three. Its precision is 1.0, but the unreviewed facts must stay active.
func TestAuditApplyRefusesAPartiallyLabelledHeuristic(t *testing.T) {
	dir := env(t)
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	a := auditTestFact(t, s, "20 октября 2026 обновили alpha.")
	b := auditTestFact(t, s, "21 октября 2026 обновили beta.")
	c := auditTestFact(t, s, "22 октября 2026 обновили gamma.")
	_ = s.Close()
	plan := auditPlan{Generated: "2026-10-20T12:00:00Z", MinPrecision: auditMinPrecision, Heuristics: []auditHeuristic{
		{Name: "event_date", Proposed: 3}}, Proposals: []auditProposal{
		{ID: a.ID, Version: a.Version, Heuristic: "event_date", Reason: "r", Decision: "retire"},
		{ID: b.ID, Version: b.Version, Heuristic: "event_date", Reason: "r"},
		{ID: c.ID, Version: c.Version, Heuristic: "event_date", Reason: "r"}}}
	path := filepath.Join(dir, "plan.json")
	writeJSON(t, path, plan)
	var out strings.Builder
	if err := runAudit([]string{"-apply", path}, &out); err != nil {
		t.Fatal(err)
	}
	s, err = store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	for _, id := range []string{a.ID, b.ID, c.ID} {
		if got, _ := s.Get(id); got.Status != store.Active {
			t.Errorf("partially reviewed fact %s was retired: %+v", id, got)
		}
	}
	bts, err := os.ReadFile(filepath.Join(dir, "state", "memcue", "audit.log"))
	if err != nil {
		t.Fatal(err)
	}
	var rec auditRecord
	if err := json.Unmarshal(bytesTrim(bts), &rec); err != nil {
		t.Fatalf("%q: %v", bts, err)
	}
	if rec.Applied != 0 || rec.Refused != 1 || len(rec.Heuristics) != 1 || rec.Heuristics[0].Labelled != 1 ||
		rec.Heuristics[0].Precision == nil || *rec.Heuristics[0].Precision != 1 {
		t.Errorf("record %+v", rec)
	}
}

func TestAuditApplyRefusesAChangedFact(t *testing.T) {
	dir := env(t)
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	f := auditTestFact(t, s, "CI в beta гоняет job.")
	if _, err := s.Correct(f.ID, "CI в beta гоняет два job.", nil); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	plan := auditPlan{Generated: "2026-10-20T12:00:00Z", MinPrecision: auditMinPrecision, Heuristics: []auditHeuristic{
		{Name: "ci_makefile", Proposed: 1}}, Proposals: []auditProposal{
		{ID: f.ID, Version: 1, Heuristic: "ci_makefile", Reason: "r", Decision: "retire"}}}
	path := filepath.Join(dir, "plan.json")
	writeJSON(t, path, plan)
	if err := runAudit([]string{"-apply", path}, &strings.Builder{}); err == nil {
		t.Fatal("want an error for a changed fact")
	}
	s, err = store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if got, _ := s.Get(f.ID); got.Status != store.Active {
		t.Errorf("a changed fact was retired: %+v", got)
	}
}

func TestAuditApplyNeedsDecisions(t *testing.T) {
	dir := env(t)
	plan := auditPlan{Generated: "2026-10-20T12:00:00Z", Heuristics: []auditHeuristic{{Name: "event_date"}},
		Proposals: []auditProposal{{ID: "01TEST", Version: 1, Heuristic: "event_date"}}}
	path := filepath.Join(dir, "plan.json")
	writeJSON(t, path, plan)
	if err := runAudit([]string{"-apply", path}, &strings.Builder{}); err == nil ||
		!strings.Contains(err.Error(), "no retire or keep decisions") {
		t.Errorf("got %v", err)
	}
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func bytesTrim(b []byte) []byte {
	return []byte(strings.TrimSpace(string(b)))
}
