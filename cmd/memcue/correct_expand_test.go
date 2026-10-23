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
	f := vetStatus(t, id)
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
	if f := vetStatus(t, id); *calls != 0 || len(f.Queries) != 0 || f.Content != "Без endpoint запрос не идёт." {
		t.Fatalf("calls %d, fact %+v", *calls, f)
	}
	withAPI(t, "https://api.example.test")
	failing := fakeExpandOnce(t, errors.New("endpoint down"))
	correctForTest(t, id, "Endpoint лежит, факт всё равно правится.")
	if f := vetStatus(t, id); *failing != 1 || len(f.Queries) != 0 ||
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

// A dream that corrects a fact gives it queries in the same run: its expansion step runs before the groups are judged,
// so the corrected text would otherwise wait for the next dream (#297).
func TestDreamExpandsACorrectedFact(t *testing.T) {
	env(t)
	_, a, b, _ := dreamBase(t)
	fakeDream(t, func(string) []extract.Action {
		return []extract.Action{{Op: "correct", ID: b, Content: "слитый текст", Keywords: []string{"nightly-sync"}, Reason: "merge"},
			{Op: "retire", ID: a, By: b, Kind: "duplicate", Reason: "merged into " + b}}
	})
	asked := 0
	old := expandFn
	expandFn = func(input string) (extract.ExpandResult, error) {
		asked++
		// Only the corrected text is answered: the facts of the base stay unexpanded, so the call can only come from
		// the new step after the correction.
		if !strings.Contains(input, "слитый текст") {
			return extract.ExpandResult{}, nil
		}
		id := input[strings.Index(input, `"id":"`)+6:]
		id = id[:strings.Index(id, `"`)]
		return extract.ExpandResult{Facts: []extract.Expansion{{ID: id, Queries: []string{"какой порт у postgres"}}}}, nil
	}
	t.Cleanup(func() { expandFn = old })
	if err := runDream([]string{"-all"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	f := vetStatus(t, b)
	if asked == 0 || len(f.Queries) != 1 || f.Queries[0] != "какой порт у postgres" {
		t.Fatalf("asked %d, corrected fact %+v", asked, f)
	}
}
