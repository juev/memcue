package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/juev/memcue/internal/extract"
)

// fakeDreamRun plays the endpoint of dream -run: it answers the packet with the functions of the session's model in
// the tests (answerPacket) and counts the calls, which a pass makes at once (#471).
func fakeDreamRun(t *testing.T, fail error) *int {
	t.Helper()
	calls := 0
	var mu sync.Mutex
	old := dreamRunFn
	dreamRunFn = func(ctx context.Context, packet string) (extract.DreamReply, error) {
		// The fakes of the session's model behind answerPacket keep what they were sent: one answer at a time.
		mu.Lock()
		defer mu.Unlock()
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

// dreamedHoursAgo marks the base as reviewed, with ordinary query work remaining on two facts.
// The duplicate pair already has queries so the sweep can review it in a separate call.
func dreamedHoursAgo(t *testing.T, dir string) {
	t.Helper()

	fakeDream(t, func(string) []extract.Action { return nil })
	state, err := stateDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := markDream(state, time.Now().Add(-dreamEvery-time.Hour)); err != nil {
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

// A due dream is prepared, sent to the endpoint and applied: the bare facts get ordinary search queries and dream.log keeps
// the run with its tokens. The duplicate pair forms a separate review call. Right after the run nothing is due,
// so the next -run makes no call.

func TestDreamRunLogsEachCall(t *testing.T) {
	dir := env(t)
	withAPI(t, "https://api.example.test")
	dreamBase(t)
	dreamedHoursAgo(t, dir)
	fakeDreamRun(t, nil)
	if err := runDream([]string{"-run"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	recs := readDreamLog(t, dir)
	last := recs[len(recs)-1]
	byTask := map[string]dreamCallRecord{}
	var tokens extract.Tokens
	var cost float64
	for _, c := range last.Calls {
		byTask[c.Task] = c
		tokens.Add(c.Tokens)
		cost += c.CostUSD
		if c.API != 3000 || c.Error != "" {
			t.Errorf("call %+v", c)
		}
	}
	if len(last.Calls) != 1 || tokens != *last.Tokens || cost != last.CostUSD {
		t.Fatalf("calls %+v do not add up to the pass: tokens %+v, cost %v", last.Calls, last.Tokens, last.CostUSD)
	}
	if r := byTask[taskReview]; r.Groups != 1 || r.Facts != 0 {
		t.Errorf("review call %+v, want the group of the two copies", r)
	}

}

// A failed call is logged with its error on its own line; the call that answered keeps its record.
func TestDreamRunLogsTheFailedCall(t *testing.T) {
	dir := env(t)
	withAPI(t, "https://api.example.test")
	dreamBase(t)
	dreamedHoursAgo(t, dir)
	fakeDreamRun(t, nil)
	good := dreamRunFn
	dreamRunFn = func(ctx context.Context, packet string) (extract.DreamReply, error) {
		if strings.Contains(packet, "## Группа 1") {
			return extract.DreamReply{Tokens: extract.Tokens{Input: 7}}, errModel
		}
		return good(ctx, packet)
	}
	if err := runDream([]string{"-run"}, &bytes.Buffer{}); !errors.Is(err, errModel) {
		t.Fatalf("err %v, want the call's error", err)
	}
	recs := readDreamLog(t, dir)
	last := recs[len(recs)-1]
	failed, answered := 0, 0
	for _, c := range last.Calls {
		switch {
		case c.Task == taskReview && c.Error != "" && c.Tokens.Input == 7:
			failed++
		case c.Task == "expand" && c.Error == "" && c.Proposed == 2:
			answered++
		}
	}
	if len(last.Calls) != 1 || failed != 1 || answered != 0 {
		t.Errorf("calls %+v", last.Calls)
	}
}

// Pending queries do not make a dream due before MEMCUE_DREAM_EVERY has passed: no call.
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

func fewPerPacket(t *testing.T) {
	t.Helper()
	old := packetFresh
	packetFresh = 2
	t.Cleanup(func() { packetFresh = old })
}

// dream -run -all finishes the base (#405): packets one after another, with no interval, until a packet has nothing
// to split or expand; that one is answered too and the loop stops. Without MEMCUE_API_URL it runs, on
// claude -p; a plain -run still refuses there.

func TestDreamRunAllStopsOnAFailedPass(t *testing.T) {
	dir := env(t)
	dreamBase(t)
	fewPerPacket(t)

	fakeDream(t, func(string) []extract.Action { return nil })
	fakeDreamRun(t, nil)
	good := dreamRunFn
	var mu sync.Mutex
	first := ""
	dreamRunFn = func(ctx context.Context, packet string) (extract.DreamReply, error) {
		// The packet of the second pass fails.
		id := regexp.MustCompile("`packet` — ровно `([^`]+)`").FindStringSubmatch(packet)[1]
		mu.Lock()
		if first == "" {
			first = id
		}
		second := id != first
		mu.Unlock()
		if second {
			return extract.DreamReply{}, errModel
		}
		return good(ctx, packet)
	}
	err := runDream([]string{"-run", "-all", "-j", "1"}, &bytes.Buffer{})
	if !errors.Is(err, errModel) || !strings.Contains(err.Error(), "pass 2") {
		t.Fatalf("err %v", err)
	}
	if recs := readDreamLog(t, dir); len(recs) != 2 || recs[0].Error != "" || recs[0].Fresh == 0 {
		t.Errorf("records %+v", recs)
	}
}

// The bound of passes stops a run that still has work and says so; the next run continues.
func TestDreamRunAllStopsAtTheBound(t *testing.T) {
	dir := env(t)
	dreamBase(t)
	fewPerPacket(t)

	fakeDream(t, func(string) []extract.Action { return nil })
	fakeDreamRun(t, nil)
	old := dreamAllPasses
	dreamAllPasses = 1
	t.Cleanup(func() { dreamAllPasses = old })
	err := runDream([]string{"-run", "-all", "-j", "1"}, &bytes.Buffer{})
	if n := len(readDreamLog(t, dir)); err == nil || !strings.Contains(err.Error(), "stopped after 1 passes") || n != 1 {
		t.Fatalf("err %v after %d passes", err, n)
	}
	dreamAllPasses = old
	if err := runDream([]string{"-run", "-all"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
}

// The merge sweep may keep returning the same group, but it cannot keep -all running
// after split, expansion and scope work is complete.
func TestDreamRunAllFinishesDespiteRepeatingSweep(t *testing.T) {
	dir := env(t)
	dreamBase(t)
	fewPerPacket(t)

	fakeDream(t, func(string) []extract.Action { return nil })
	fakeDreamRun(t, nil)
	var out bytes.Buffer
	if err := runDream([]string{"-run", "-all"}, &out); err != nil {
		t.Fatal(err)
	}
	if n := len(readDreamLog(t, dir)); !strings.Contains(out.String(),
		"dream -all: done after") || n >= dreamAllPasses {
		t.Errorf("%d passes, out %q", n, out.String())
	}
}

// -j packets of ordinary expansion work are joined in one pass. One failed call still fails the whole pass.

func TestDreamRunPacketsFlag(t *testing.T) {
	env(t)
	withAPI(t, "https://api.example.test")
	for _, args := range [][]string{{"-run", "-j", "0"}, {"-prepare", "-all", "-j", "2"}} {
		if err := runDream(args, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "-j") {
			t.Errorf("%q: err %v", args, err)
		}
	}
}
