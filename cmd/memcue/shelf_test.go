package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/juev/memcue/internal/extract"
	"github.com/juev/memcue/internal/store"
)

// shelfMessages is a research session (#433): a page read with web_extract, a file read in the shell, and a rule the
// user stated.
const shelfMessages = `[{"role":"user","content":"впредь пиши лимиты в фактах числом"},
	{"role":"assistant","content":null,"tool_calls":[
		{"id":"a","function":{"name":"web_extract","arguments":"{\"url\":\"https://example.com/limits\"}"}},
		{"id":"b","function":{"name":"terminal","arguments":"{\"command\":\"cat /etc/proxy.conf\"}"}}]},
	{"role":"tool","tool_call_id":"a","content":"The Example API allows 60 requests a minute per key."},
	{"role":"tool","tool_call_id":"b","content":"proxy_rate 90 per minute"}]`

func shelfExtraction(t *testing.T) {
	t.Helper()
	old := extractFn
	extractFn = func(string, string) (extracted, error) {
		return extracted{Facts: []extract.Fact{
			// No scope: the shelf is the one it gets.
			{Content: "Example API: 60 запросов в минуту на ключ.", Kind: "environment", Source: "tool",
				Evidence: "allows 60 requests a minute per key"},
			{Content: "Прокси на db1 режет до 90 запросов в минуту.", Kind: "environment", Source: "tool",
				Scope: "host:db1", Evidence: "proxy_rate 90 per minute"},
			{Content: "Лимиты в фактах пишутся числом.", Kind: "rule", Source: "user", Scope: "global",
				Evidence: "впредь пиши лимиты в фактах числом"},
		}}, nil
	}
	t.Cleanup(func() { extractFn = old })
}

// scopesByContent maps each candidate's content to its scope.
func scopesByContent(t *testing.T) map[string]string {
	t.Helper()
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	cs, err := s.Candidates()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, f := range cs {
		out[f.Content] = f.Scope
	}
	return out
}

func TestExtractMessagesShelvesExternalFacts(t *testing.T) {
	env(t)
	shelfExtraction(t)
	if err := runExtractMessages([]string{"-origin", "hermes 2026-10-26", "-min-chars", "0", "-shelf", "research"},
		strings.NewReader(shelfMessages)); err != nil {
		t.Fatal(err)
	}
	got := scopesByContent(t)
	want := map[string]string{
		"Example API: 60 запросов в минуту на ключ.":   "shelf:research",
		"Прокси на db1 режет до 90 запросов в минуту.": "host:db1",
		"Лимиты в фактах пишутся числом.":              "global",
	}
	if len(got) != len(want) {
		t.Fatalf("stored %v, want %v", got, want)
	}
	for c, sc := range want {
		if got[c] != sc {
			t.Errorf("%q: scope %q, want %q", c, got[c], sc)
		}
	}
}

// Without -shelf the rule is off: the page's fact without a scope is rejected as before, the rest is stored as is.
func TestExtractMessagesWithoutShelfKeepsScopes(t *testing.T) {
	dir := env(t)
	shelfExtraction(t)
	if err := runExtractMessages([]string{"-origin", "hermes 2026-10-26", "-min-chars", "0"},
		strings.NewReader(shelfMessages)); err != nil {
		t.Fatal(err)
	}
	got := scopesByContent(t)
	if len(got) != 2 || got["Прокси на db1 режет до 90 запросов в минуту."] != "host:db1" ||
		got["Лимиты в фактах пишутся числом."] != "global" {
		t.Errorf("stored %v", got)
	}
	recs := readLog(t, dir+"/state")
	if len(recs) != 1 || len(recs[0].Rejected) != 1 ||
		!strings.HasPrefix(recs[0].Rejected[0], "an environment fact needs a scope") {
		t.Errorf("log %+v", recs)
	}
}

func TestExtractMessagesRefusesABadShelf(t *testing.T) {
	env(t)
	err := runExtractMessages([]string{"-shelf", "Research!"}, strings.NewReader("[]"))
	if err == nil || !strings.Contains(err.Error(), "[a-z0-9][a-z0-9-]{0,63}") {
		t.Errorf("err = %v, want the slug shape named", err)
	}
}

func TestReScopeShelf(t *testing.T) {
	for scope, want := range map[string]bool{"shelf:research": true, "shelf:rust-async": true, "shelf:0": true,
		"Shelf:Research!": false, "shelf:Research": false, "shelf:": false, "shelf:-x": false,
		"shelf:" + strings.Repeat("a", 65): false} {
		if got := reScope.MatchString(scope); got != want {
			t.Errorf("reScope(%q) = %v, want %v", scope, got, want)
		}
	}
}

func addShelfFact(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := runAdd(append(args, "--", "Example API allows 60 requests a minute."), &out)
	return strings.TrimSpace(out.String()), err
}

func factScope(t *testing.T, id string) string {
	t.Helper()
	var scope string
	if err := withStore(func(s *store.Store) error {
		f, err := s.Get(id)
		scope = f.Scope
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return scope
}

func TestAddShelf(t *testing.T) {
	env(t)
	byShelf, err := addShelfFact(t, "-kind", "environment", "-shelf", "research")
	if err != nil {
		t.Fatal(err)
	}
	byScope, err := addShelfFact(t, "-kind", "environment", "-scope", "shelf:research")
	if err != nil {
		t.Fatal(err)
	}
	if a, b := factScope(t, byShelf), factScope(t, byScope); a != "shelf:research" || a != b {
		t.Errorf("-shelf stored %q, -scope stored %q", a, b)
	}
	if _, err := addShelfFact(t, "-shelf", "research", "-scope", "shelf:research"); err == nil ||
		!strings.Contains(err.Error(), "-shelf and -scope") {
		t.Errorf("both flags: err = %v", err)
	}
	if _, err := addShelfFact(t, "-shelf", "Research"); err == nil || !strings.Contains(err.Error(), "[a-z0-9]") {
		t.Errorf("bad -shelf: err = %v", err)
	}
	if _, err := addShelfFact(t, "-scope", "shelf:Research X"); err == nil ||
		!strings.Contains(err.Error(), "[a-z0-9][a-z0-9-]{0,63}") {
		t.Errorf("bad shelf scope: err = %v, want the slug shape named", err)
	}
}

func searchOutput(t *testing.T, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	if err := runSearch(args, &out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestSearchReadsTheShelfOnlyWhenNamed(t *testing.T) {
	env(t)
	shelved, err := addShelfFact(t, "-kind", "environment", "-shelf", "research")
	if err != nil {
		t.Fatal(err)
	}
	if got := searchOutput(t, "-in", "all", "Example", "API"); strings.Contains(got, shelved) {
		t.Errorf("-in all returned the shelved fact: %q", got)
	}
	if got := searchOutput(t, "Example", "API"); strings.Contains(got, shelved) {
		t.Errorf("the default returned the shelved fact: %q", got)
	}
	for _, in := range []string{"shelf:research", "shelf"} {
		if got := searchOutput(t, "-in", in, "Example", "API"); !strings.Contains(got, shelved) {
			t.Errorf("-in %s did not return the shelved fact: %q", in, got)
		}
	}
}

func TestSearchHintsAtTheShelf(t *testing.T) {
	env(t)
	got := searchOutput(t, "-in", "all", "Example", "API")
	if got != "No stored facts match.\n" {
		t.Errorf("empty shelf: %q", got)
	}
	if _, err := addShelfFact(t, "-kind", "environment", "-shelf", "research"); err != nil {
		t.Fatal(err)
	}
	got = searchOutput(t, "-in", "all", "Example", "API")
	if want := "No stored facts match.\n1 fact on the shelf matches: -in shelf:research\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	// A search that named the shelf and found nothing has nothing to point at.
	if got := searchOutput(t, "-in", "shelf", "nothing", "here"); got != "No stored facts match.\n" {
		t.Errorf("-in shelf: %q", got)
	}
}

func TestSearchJSONCarriesScope(t *testing.T) {
	env(t)
	if _, err := addShelfFact(t, "-kind", "environment", "-shelf", "research"); err != nil {
		t.Fatal(err)
	}
	var hits []map[string]any
	if err := json.Unmarshal([]byte(searchOutput(t, "-json", "-in", "shelf", "Example", "API")), &hits); err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0]["scope"] != "shelf:research" {
		t.Errorf("hits %v", hits)
	}
}
