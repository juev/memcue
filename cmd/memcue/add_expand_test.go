package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/juev/memcue/internal/extract"
)

// fakeExpandOnce answers the expansion call of add and correct with one query per fact, or fails, and counts the calls.
func fakeExpandOnce(t *testing.T, fail error) *int {
	t.Helper()
	calls := 0
	old := expandOneFn
	expandOneFn = func(input string) (extract.ExpandResult, error) {
		calls++
		if fail != nil {
			return extract.ExpandResult{}, fail
		}
		id := input[strings.Index(input, `"id":"`)+6:]
		id = id[:strings.Index(id, `"`)]
		return extract.ExpandResult{Facts: []extract.Expansion{{ID: id, Queries: []string{"какой порт у postgres"}}}}, nil
	}
	t.Cleanup(func() { expandOneFn = old })
	return &calls
}

func withAPI(t *testing.T, url string) {
	t.Helper()
	old := extract.API.URL
	extract.API.URL = url
	t.Cleanup(func() { extract.API.URL = old })
}

func addFactForTest(t *testing.T, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	if err := runAdd(args, &out); err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(out.String())
}

// With an API endpoint, add gives the fact it stored its search queries at once (#288).
func TestAddExpandsTheFact(t *testing.T) {
	env(t)
	withAPI(t, "https://api.example.test")
	calls := fakeExpandOnce(t, nil)
	id := addFactForTest(t, "-origin", "x 2026-10-22", "Postgres демо-базы слушает порт 5432 на db1.")
	if f := storedFact(t, id); *calls != 1 || len(f.Queries) != 1 || f.Queries[0] != "какой порт у postgres" {
		t.Fatalf("calls %d, fact %+v", *calls, f)
	}
}

// A failed call keeps the fact, without queries, for the dream to expand; add still succeeds and prints the id.
func TestAddKeepsTheFactWhenExpansionFails(t *testing.T) {
	env(t)
	withAPI(t, "https://api.example.test")
	calls := fakeExpandOnce(t, errors.New("endpoint down"))
	id := addFactForTest(t, "-origin", "x 2026-10-22", "Postgres демо-базы слушает порт 5432 на db1.")
	if f := storedFact(t, id); *calls != 1 || len(f.Queries) != 0 || f.Content == "" {
		t.Fatalf("calls %d, fact %+v", *calls, f)
	}
}

// A candidate (the Hermes mirror writes them on the turn's path) and an add without an API endpoint call no model.
func TestAddCallsNoModelForCandidatesOrWithoutAPI(t *testing.T) {
	env(t)
	calls := fakeExpandOnce(t, nil)
	addFactForTest(t, "-origin", "x 2026-10-22", "Без endpoint запрос не идёт.")
	withAPI(t, "https://api.example.test")
	addFactForTest(t, "-candidate", "-origin", "x 2026-10-22", "Кандидат ждёт vet.")
	if *calls != 0 {
		t.Fatalf("calls %d, want none", *calls)
	}
}
