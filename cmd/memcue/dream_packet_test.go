package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/juev/memcue/internal/extract"
	"github.com/juev/memcue/internal/store"
)

// An answer to an older packet is refused whole and changes nothing: a second -prepare replaced the packet it was for.
func TestDreamApplyRefusesAnotherPacket(t *testing.T) {
	dir := env(t)
	_, a, b, _ := dreamBase(t)
	fakeDream(t, mergeAnswer(a, b))
	path, err := prepareAnswer([]string{"-all"}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond) // the packet id is its start time
	if err := runDream([]string{"-prepare", "-all"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	err = runDream([]string{"-apply", path}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "run -prepare again") {
		t.Fatalf("err %v", err)
	}
	if f := vetStatus(t, a); f.Status != store.Active || f.Version != 1 {
		t.Errorf("an answer to another packet changed %+v", f)
	}
	if _, ok := lastDream(filepath.Join(dir, "state", "memcue")); ok {
		t.Error("a refused answer marked the dream")
	}
}

// The model can act only on the facts the packet gave it for each part: an action outside the groups and queries for
// a fact it was not asked about are not applied.
func TestDreamApplyKeepsToThePacket(t *testing.T) {
	env(t)
	old, a, b, other := dreamBase(t)
	s, _ := store.Open(factsPath())
	if _, err := s.SetQueries(other, 1, []string{"уже есть"}); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	fakeDream(t, func(string) []extract.Action {
		return []extract.Action{{Op: "retire", ID: old, By: a, Kind: "outdated", Reason: "not in a group"},
			{Op: "retire", ID: a, By: b, Kind: "duplicate", Reason: "dup"}}
	})
	fakeExpandOf(t, map[string][]string{other: {"новый запрос"}, old: {"порт постгреса"}})
	if err := dreamModel([]string{"-all"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if f := vetStatus(t, old); f.Status != store.Active || len(f.Queries) != 1 {
		t.Errorf("old %+v", f)
	}
	if f := vetStatus(t, a); f.Status != store.Retired {
		t.Errorf("the action of the group was not applied: %+v", f)
	}
	if f := vetStatus(t, other); len(f.Queries) != 1 || f.Queries[0] != "уже есть" {
		t.Errorf("queries of a fact the packet did not ask about were written: %+v", f)
	}
}

// fakeExpandOf answers expansion with the queries of each id it knows, whatever the packet asked.
func fakeExpandOf(t *testing.T, queries map[string][]string) {
	t.Helper()
	old := expandFn
	expandFn = func(string) (extract.ExpandResult, error) {
		var r extract.ExpandResult
		for id, q := range queries {
			r.Facts = append(r.Facts, extract.Expansion{ID: id, Queries: q})
		}
		return r, nil
	}
	t.Cleanup(func() { expandFn = old })
}

// The fresh facts a packet leaves out are not counted as seen: the next packet takes them.
func TestDreamPacketBoundsLeaveTheRestForTheNext(t *testing.T) {
	dir := env(t)
	state := filepath.Join(dir, "state", "memcue")
	dreamBase(t)
	oldFresh := packetFresh
	packetFresh = 2
	t.Cleanup(func() { packetFresh = oldFresh })
	splits := fakeSplit(t, func(string) ([]extract.Split, error) { return nil, nil })
	if err := dreamModel([]string{"-all"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	first := countFacts(t, (*splits)[0])
	if first != 2 {
		t.Fatalf("first packet split %d facts, want 2", first)
	}
	if err := markDream(state, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	*splits = nil
	if err := dreamModel(nil, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if len(*splits) != 1 || countFacts(t, (*splits)[0]) != 2 {
		t.Fatalf("the second packet split %q, want the other two", *splits)
	}
	*splits = nil
	if err := dreamModel(nil, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if len(*splits) != 0 {
		t.Errorf("a third packet split %q", *splits)
	}
}

func countFacts(t *testing.T, input string) int {
	t.Helper()
	var v struct {
		Facts []json.RawMessage `json:"facts"`
	}
	if err := json.Unmarshal([]byte(input), &v); err != nil {
		t.Fatal(err)
	}
	return len(v.Facts)
}

// A packet with nothing to do is the dream: it is marked and logged, and no packet is kept.
func TestDreamWithNothingToDoIsMarked(t *testing.T) {
	dir := env(t)
	state := filepath.Join(dir, "state", "memcue")
	var out bytes.Buffer
	if err := runDream([]string{"-prepare", "-all"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "nothing for the dream") {
		t.Errorf("out %q", out.String())
	}
	if _, ok := lastDream(state); !ok {
		t.Error("an empty dream was not marked")
	}
	if _, err := os.Stat(filepath.Join(state, packetFile)); !os.IsNotExist(err) {
		t.Errorf("a packet was kept: %v", err)
	}
	if recs := readDreamLog(t, dir); len(recs) != 1 {
		t.Errorf("log %+v", recs)
	}
}

// The session start carries the dream hint when a dream is due, and only then.
func TestSessionStartCarriesTheDreamHint(t *testing.T) {
	dir := env(t)
	state := filepath.Join(dir, "state", "memcue")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	start := func() string {
		var out bytes.Buffer
		if err := runSessionStart(strings.NewReader(`{"session_id":"s1","source":"startup","cwd":"/tmp"}`), &out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	dreamBase(t)
	if got := start(); strings.Contains(got, "memcue-dream") {
		t.Errorf("hint on the first start: %s", got)
	}
	addFact(t, "Gradle в demo-app собирается через make build.", "gradle", "make")
	if err := markDream(state, time.Now().Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := start(); !strings.Contains(got, "1 facts wait for the dream") || !strings.Contains(got, "memcue-dream") {
		t.Errorf("no hint when due: %s", got)
	}
}

// A correction to nothing but spaces is refused by the check, before anything is written, so the rest of the answer
// applies and the dream is marked.
func TestDreamRefusesABlankCorrection(t *testing.T) {
	dir := env(t)
	_, a, b, _ := dreamBase(t)
	fakeDream(t, func(groups string) []extract.Action {
		if !strings.Contains(groups, a) {
			return nil
		}
		return []extract.Action{{Op: "retire", ID: a, By: b, Kind: "duplicate", Reason: "dup"},
			{Op: "correct", ID: b, Content: "   ", Reason: "blank"}}
	})
	if err := dreamModel([]string{"-all"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if f := vetStatus(t, a); f.Status != store.Retired {
		t.Errorf("a %+v", f)
	}
	if f := vetStatus(t, b); f.Version != 1 {
		t.Errorf("a blank correction was written: %+v", f)
	}
	recs := readDreamLog(t, dir)
	if last := recs[len(recs)-1]; last.Error != "" || len(last.Refused) != 1 || !strings.Contains(last.Refused[0], "no content") {
		t.Errorf("record %+v", last)
	}
	if _, ok := lastDream(filepath.Join(dir, "state", "memcue")); !ok {
		t.Error("the dream was not marked")
	}
}
