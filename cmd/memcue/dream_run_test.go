package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/juev/memcue/internal/extract"
)

// fakeDreamRun plays the endpoint of dream -run: it answers the packet with the functions of the session's model in
// the tests (answerPacket) and counts the calls.
func fakeDreamRun(t *testing.T, fail error) *int {
	t.Helper()
	calls := 0
	old := dreamRunFn
	dreamRunFn = func(packet string) (extract.DreamReply, error) {
		calls++
		r := extract.DreamReply{Tokens: extract.Tokens{Input: len(packet) / 4, Output: 100}}
		if fail != nil {
			return r, fail
		}
		a, err := answerPacket(packet)
		if err != nil {
			return r, err
		}
		r.Answer, err = json.Marshal(a)
		r.CostUSD, r.API, r.TTFT = 0.5, 3*time.Second, time.Second
		return r, err
	}
	t.Cleanup(func() { dreamRunFn = old })
	return &calls
}

// translateAll answers every fact a packet gives to translate.
func translateAll(t *testing.T) {
	t.Helper()
	old := translateFn
	translateFn = func(in string) ([]extract.Translated, error) {
		var req struct {
			Facts []struct{ ID string } `json:"facts"`
		}
		if err := json.Unmarshal([]byte(in), &req); err != nil {
			return nil, err
		}
		var out []extract.Translated
		for _, f := range req.Facts {
			out = append(out, extract.Translated{ID: f.ID, Translation: "translated " + f.ID})
		}
		return out, nil
	}
	t.Cleanup(func() { translateFn = old })
}

// dreamedHoursAgo runs a first dream over the whole base, which gives the facts search queries and translates
// nothing, and moves its mark back, so the next check finds the interval passed and the facts owed a translation. A
// fact without queries waits for them before it is translated (#396).
func dreamedHoursAgo(t *testing.T, dir string) {
	t.Helper()
	fakeExpand(t, nil)
	fakeDream(t, func(string) []extract.Action { return nil })
	if err := dreamModel([]string{"-all"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if err := markDream(dir+"/state/memcue", time.Now().Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
}

// Without an endpoint, dream -run refuses: on Claude Code the dream stays with the session's model (#401).
func TestDreamRunNeedsAnEndpoint(t *testing.T) {
	env(t)
	calls := fakeDreamRun(t, nil)
	err := runDream([]string{"-run"}, &bytes.Buffer{})
	if !errors.Is(err, extract.ErrNoEndpoint) || *calls != 0 {
		t.Fatalf("err %v after %d calls, want ErrNoEndpoint and none", err, *calls)
	}
	if err := runDream([]string{"-run", "-since", "2026-10-01"}, &bytes.Buffer{}); err == nil {
		t.Error("-run -since was accepted")
	}
}

// A due dream is prepared, sent to the endpoint and applied: the owed facts get their translation and dream.log keeps
// the run with its tokens. The two copies of one fact form a group, and a fact a group may change waits for a later
// packet (#396). Right after the run nothing is due, so the next -run makes no call.
func TestDreamRunAppliesTheAnswer(t *testing.T) {
	dir := env(t)
	withAPI(t, "https://api.example.test")
	old, _, _, other := dreamBase(t)
	dreamedHoursAgo(t, dir)
	translateAll(t)
	calls := fakeDreamRun(t, nil)

	var out bytes.Buffer
	if err := runDream([]string{"-run"}, &out); err != nil {
		t.Fatal(err)
	}
	if *calls != 1 || !strings.Contains(out.String(), "translated 2 facts") {
		t.Fatalf("%d calls, out %q", *calls, out.String())
	}
	for _, id := range []string{old, other} {
		if tr, ok := extract.Translation(vetStatus(t, id).Queries); !ok || tr != "translated "+id {
			t.Errorf("fact %s: translation %q, %v", id, tr, ok)
		}
	}
	recs := readDreamLog(t, dir)
	last := recs[len(recs)-1]
	if last.Translated != 2 || last.Tokens == nil || last.Tokens.Output != 100 || last.Error != "" || last.Packet == "" {
		t.Errorf("record %+v", last)
	}
	// The record says where the time of the pass went: the API time and the first token as the call reports them (#471).
	if tm := last.Timing; tm == nil || tm.API != 3000 || tm.TTFT != 1000 {
		t.Errorf("timing %+v", last.Timing)
	}

	out.Reset()
	if err := runDream([]string{"-run"}, &out); err != nil {
		t.Fatal(err)
	}
	if *calls != 1 || out.String() != "dream: nothing due\n" {
		t.Errorf("second run: %d calls, out %q", *calls, out.String())
	}
}

// Facts owed a translation do not make a dream due before MEMCUE_DREAM_EVERY has passed: no call.
func TestDreamRunWaitsForTheInterval(t *testing.T) {
	dir := env(t)
	withAPI(t, "https://api.example.test")
	dreamBase(t)
	fakeDream(t, func(string) []extract.Action { return nil })
	if err := dreamModel([]string{"-all"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	calls := fakeDreamRun(t, nil)
	var out bytes.Buffer
	if err := runDream([]string{"-run"}, &out); err != nil {
		t.Fatal(err)
	}
	if *calls != 0 || out.String() != "dream: nothing due\n" {
		t.Errorf("%d calls, out %q", *calls, out.String())
	}
	if recs := readDreamLog(t, dir); recs[len(recs)-1].Tokens != nil {
		t.Errorf("a run with nothing due was logged as a call: %+v", recs[len(recs)-1])
	}
}

// A failed endpoint call changes nothing and is logged with its error; the next run tries again.
func TestDreamRunLogsAFailedCall(t *testing.T) {
	dir := env(t)
	withAPI(t, "https://api.example.test")
	old, _, _, _ := dreamBase(t)
	dreamedHoursAgo(t, dir)
	calls := fakeDreamRun(t, errModel)
	if err := runDream([]string{"-run"}, &bytes.Buffer{}); !errors.Is(err, errModel) {
		t.Fatalf("err %v, want the endpoint's error", err)
	}
	if _, ok := extract.Translation(vetStatus(t, old).Queries); ok {
		t.Error("a failed call wrote a translation")
	}
	recs := readDreamLog(t, dir)
	if last := recs[len(recs)-1]; !strings.HasPrefix(last.Error, "model: ") || last.Tokens == nil {
		t.Errorf("record %+v", last)
	}
	translateAll(t)
	fakeDreamRun(t, nil)
	if err := runDream([]string{"-run"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := extract.Translation(vetStatus(t, old).Queries); !ok || *calls != 1 {
		t.Errorf("the run after a failure did not translate (failed calls %d)", *calls)
	}
}

// fewPerPacket makes a packet take one fact to translate and two to expand, so a small base needs several passes.
func fewPerPacket(t *testing.T) {
	t.Helper()
	oldT, oldE := packetTranslate, packetExpand
	packetTranslate, packetExpand = 1, 2
	t.Cleanup(func() { packetTranslate, packetExpand = oldT, oldE })
}

// dream -run -all finishes the base (#405): packets one after another, with no interval, until a packet has nothing
// to split, expand or translate; that one is answered too and the loop stops. Without MEMCUE_API_URL it runs, on
// claude -p; a plain -run still refuses there.
func TestDreamRunAllFinishesTheBase(t *testing.T) {
	dir := env(t)
	ids := []string{}
	old, a, b, other := dreamBase(t)
	ids = append(ids, old, a, b, other)
	fewPerPacket(t)
	noSweep(t) // the sweep would group the duplicate pair on every pass of a base this small; see the next test
	fakeExpand(t, nil)
	fakeDream(t, func(string) []extract.Action { return nil })
	translateAll(t)
	calls := fakeDreamRun(t, nil)

	var out bytes.Buffer
	if err := runDream([]string{"-run", "-all"}, &out); err != nil {
		t.Fatal(err)
	}
	if *calls < 4 || !strings.Contains(out.String(), "dream -all: done after ") {
		t.Fatalf("%d calls, out %q", *calls, out.String())
	}
	for _, id := range ids {
		f := vetStatus(t, id)
		if _, ok := extract.Translation(f.Queries); !ok || len(extract.SearchQueries(f.Queries)) == 0 {
			t.Errorf("fact %s left with queries %q", id, f.Queries)
		}
	}
	priced := 0
	for _, r := range readDreamLog(t, dir) {
		if r.Error != "" {
			t.Errorf("record %+v", r)
		}
		if r.CostUSD == 0.5 {
			priced++
		}
	}
	if priced != *calls {
		t.Errorf("%d records carry the cost of %d calls", priced, *calls)
	}
	if err := runDream([]string{"-run"}, &bytes.Buffer{}); !errors.Is(err, extract.ErrNoEndpoint) {
		t.Errorf("plain -run without an endpoint: err %v, want ErrNoEndpoint", err)
	}
}

// A failed pass stops dream -run -all with its error; the passes before it stay applied.
func TestDreamRunAllStopsOnAFailedPass(t *testing.T) {
	env(t)
	dreamBase(t)
	fewPerPacket(t)
	fakeExpand(t, nil)
	fakeDream(t, func(string) []extract.Action { return nil })
	translateAll(t)
	ok := fakeDreamRun(t, nil)
	good := dreamRunFn
	n := 0
	dreamRunFn = func(packet string) (extract.DreamReply, error) {
		if n++; n == 2 {
			return extract.DreamReply{}, errModel
		}
		return good(packet)
	}
	err := runDream([]string{"-run", "-all"}, &bytes.Buffer{})
	if !errors.Is(err, errModel) || !strings.Contains(err.Error(), "pass 2") || *ok != 1 {
		t.Errorf("err %v after %d good passes", err, *ok)
	}
}

// The bound of passes stops a run that still has work and says so; the next run continues.
func TestDreamRunAllStopsAtTheBound(t *testing.T) {
	env(t)
	dreamBase(t)
	fewPerPacket(t)
	fakeExpand(t, nil)
	fakeDream(t, func(string) []extract.Action { return nil })
	translateAll(t)
	calls := fakeDreamRun(t, nil)
	old := dreamAllPasses
	dreamAllPasses = 1
	t.Cleanup(func() { dreamAllPasses = old })
	err := runDream([]string{"-run", "-all"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "stopped after 1 passes") || *calls != 1 {
		t.Fatalf("err %v after %d calls", err, *calls)
	}
	dreamAllPasses = old
	if err := runDream([]string{"-run", "-all"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
}

// A fact a group of every packet holds waits for its translation (#396). In a base this small the merge sweep groups
// the duplicate pair on every pass, so after dreamAllStalls passes that change nothing the loop stops and says how
// many facts are left, instead of running to the bound.
func TestDreamRunAllStopsWhenNothingMoves(t *testing.T) {
	env(t)
	dreamBase(t)
	fewPerPacket(t)
	fakeExpand(t, nil)
	fakeDream(t, func(string) []extract.Action { return nil })
	translateAll(t)
	calls := fakeDreamRun(t, nil)
	var out bytes.Buffer
	if err := runDream([]string{"-run", "-all"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "stopped after 3 passes that changed nothing; 2 facts still lack a translation") ||
		*calls >= dreamAllPasses {
		t.Errorf("%d calls, out %q", *calls, out.String())
	}
}
