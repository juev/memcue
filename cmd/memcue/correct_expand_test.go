package main

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/juev/memcue/internal/extract"
)

func correctForTest(t *testing.T, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	if err := runCorrect(args, &out); err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(out.String())
}

// A correction writes new text, so the queries of the old one no longer describe the fact: with an API endpoint the
// corrected version gets its own before the command returns (#297).
func TestCorrectExpandsTheCorrectedFact(t *testing.T) {
	env(t)
	withAPI(t, "https://api.example.test")
	calls := fakeExpandOnce(t, nil)
	id := addFactForTest(t, "-origin", "x 2026-10-22", "Postgres демо-базы слушает порт 5432 на db1.")
	printed := correctForTest(t, id, "Postgres демо-базы слушает порт 5433 на db2.")
	f := storedFact(t, id)
	if f.Content != "Postgres демо-базы слушает порт 5433 на db2." || len(f.Queries) != 1 ||
		f.Queries[0] != "какой порт у postgres" {
		t.Fatalf("calls %d, fact %+v", *calls, f)
	}
	// The printed version is the one the fact stands at, the expansion's version included.
	if want := fmt.Sprintf("v%d", f.Version); !strings.HasSuffix(printed, want) {
		t.Fatalf("printed %q, fact at v%d", printed, f.Version)
	}
}

// Without an API endpoint, and when the call fails, the correction still lands; the dream expands the fact later.
func TestCorrectKeepsTheFactWithoutExpansion(t *testing.T) {
	env(t)
	id := addFactForTest(t, "-origin", "x 2026-10-22", "Postgres демо-базы слушает порт 5432 на db1.")
	calls := fakeExpandOnce(t, nil)
	correctForTest(t, id, "Без endpoint запрос не идёт.")
	if f := storedFact(t, id); *calls != 0 || len(f.Queries) != 0 || f.Content != "Без endpoint запрос не идёт." {
		t.Fatalf("calls %d, fact %+v", *calls, f)
	}
	withAPI(t, "https://api.example.test")
	failing := fakeExpandOnce(t, errors.New("endpoint down"))
	correctForTest(t, id, "Endpoint лежит, факт всё равно правится.")
	if f := storedFact(t, id); *failing != 1 || len(f.Queries) != 0 ||
		f.Content != "Endpoint лежит, факт всё равно правится." {
		t.Fatalf("calls %d, fact %+v", *failing, f)
	}
}

// A candidate waits for the vet: the approval expands it, so correct makes no call.
func TestCorrectCallsNoModelForACandidate(t *testing.T) {
	env(t)
	withAPI(t, "https://api.example.test")
	calls := fakeExpandOnce(t, nil)
	id := addFactForTest(t, "-candidate", "-origin", "x 2026-10-22", "Кандидат на проверке у vet.")
	correctForTest(t, id, "Кандидат на проверке у vet, текст поправлен.")
	if *calls != 0 {
		t.Fatalf("calls %d, want none", *calls)
	}
}

// A correction the model gives with queries writes them for the new text in the same dream (#297).
func TestDreamCorrectionCarriesItsQueries(t *testing.T) {
	env(t)
	_, a, b, _ := dreamBase(t)
	fakeDream(t, func(string) []extract.Action {
		return []extract.Action{{Op: "correct", ID: b, Content: "слитый текст", Keywords: []string{"nightly-sync"},
			Queries: []string{"ночная подписка", " "}, Reason: "merge"},
			{Op: "retire", ID: a, By: b, Kind: "duplicate", Reason: "merged into " + b}}
	})
	if err := dreamModel([]string{"-all"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if f := storedFact(t, b); f.Content != "слитый текст" || len(f.Queries) != 1 || f.Queries[0] != "ночная подписка" {
		t.Fatalf("corrected fact %+v", f)
	}
}

// A correction without queries drops the old ones, so the next dream gives the new text queries without reviewing it
// again (#297).
func TestDreamCorrectionWithoutQueriesIsExpandedNext(t *testing.T) {
	env(t)
	_, a, b, _ := dreamBase(t)
	calls := fakeDream(t, mergeAnswer(a, b))
	if err := dreamModel([]string{"-all"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if f := storedFact(t, b); f.Content != "слитый текст" || len(f.Queries) != 0 {
		t.Fatalf("corrected fact %+v", f)
	}
	sent := fakeExpand(t, nil)
	*calls = nil
	if err := dreamModel(nil, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(*sent, " "), b) || len(*calls) != 0 {
		t.Errorf("expanded %q, groups %q", *sent, *calls)
	}
	if f := storedFact(t, b); len(f.Queries) != 1 {
		t.Errorf("corrected fact after the next dream %+v", f)
	}
}
