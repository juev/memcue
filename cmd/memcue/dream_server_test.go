package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/juev/memcue/internal/extract"
	"github.com/juev/memcue/internal/store"
)

func newServerDreamFixture(t *testing.T) (string, *store.Store) {
	t.Helper()
	env(t)
	dir, err := stateDir()
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.OpenServer(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return dir, st
}

func serverDreamFact(t *testing.T, st *store.Store, content, scope string) string {
	t.Helper()
	f, err := st.AddWith(content, "example 2026-11-26", "", store.Meta{Kind: "pitfall", Scope: scope})
	if err != nil {
		t.Fatal(err)
	}
	return f.ID
}

func TestServerDreamResumesFreshBacklogAfterFailedBatchAndRestart(t *testing.T) {
	dir, st := newServerDreamFixture(t)
	fewPerPacket(t)
	now := time.Now()
	if err := markBase(dir); err != nil {
		t.Fatal(err)
	}
	state := serverDreamState{FreshAt: now.Add(-2 * dreamEvery), SweepAt: now}
	if err := writeJSONFile(filepath.Join(dir, serverDreamFile), state); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for i := range 12 {
		ids = append(ids, serverDreamFact(t, st, fmt.Sprintf("Synthetic glacier experiment %d.", i), ""))
	}
	slices.Sort(ids)
	fakeDream(t, func(string) []extract.Action { return nil })
	fakeDreamRun(t, nil)
	inv := &invocation{ctx: context.Background()}
	p, err := inv.serverDreamBatch(dir, now, dreamEvery, io.Discard)
	if err != nil || p.Stop != "continue" || p.Completed.Fresh != 8 || p.Remaining.Fresh != 4 {
		t.Fatalf("first batch: %+v, error %v", p, err)
	}

	good := dreamRunFn
	dreamRunFn = func(ctx context.Context, text string) (extract.DreamReply, error) {
		if strings.Contains(packetBlock(text, "## Data: splitting"), ids[11]) {
			return extract.DreamReply{}, errModel
		}
		return good(ctx, text)
	}
	p, err = inv.serverDreamBatch(dir, now, dreamEvery, io.Discard)
	if !errors.Is(err, errModel) || p.Completed.Fresh != 0 || p.Remaining.Fresh != 4 {
		t.Fatalf("failed batch: %+v, error %v", p, err)
	}
	seen, err := readDreamSeen(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids[8:] {
		if seen[id] != 0 {
			t.Fatalf("failed batch consumed %s", id)
		}
	}

	dreamRunFn = good
	inv = &invocation{ctx: context.Background()}
	p, err = inv.serverDreamBatch(dir, now, dreamEvery, io.Discard)
	if err != nil || p.Stop != "done" || p.Completed.Fresh != 4 || p.Remaining.Fresh != 0 {
		t.Fatalf("after restart: %+v, error %v", p, err)
	}
	p, err = inv.serverDreamBatch(dir, now, dreamEvery, io.Discard)
	if err != nil || p.Task != "" {
		t.Fatalf("next routine interval should be idle: %+v, error %v", p, err)
	}
}

func TestServerDreamCompletesWeeklySweepWithoutFreshFacts(t *testing.T) {
	dir, st := newServerDreamFixture(t)
	oldMergeSweep := mergeSweep
	mergeSweep = 1
	t.Cleanup(func() { mergeSweep = oldMergeSweep })
	for i := range 9 {
		serverDreamFact(t, st, fmt.Sprintf("First npm publish of synthetic bundle %d used a temporary placeholder.", i), "")
	}
	if err := markBase(dir); err != nil {
		t.Fatal(err)
	}
	calls := fakeDream(t, func(string) []extract.Action { return nil })
	fakeDreamRun(t, nil)
	now := time.Now()
	inv := &invocation{ctx: context.Background()}
	p, err := inv.serverDreamBatch(dir, now, dreamEvery, io.Discard)
	if err != nil || p.Task != "old" || p.Completed.Old != 4 || p.Remaining.Old != 5 {
		t.Fatalf("first sweep batch: %+v, error %v", p, err)
	}
	inv = &invocation{ctx: context.Background()}
	for _, want := range []int{1, 0} {
		p, err = inv.serverDreamBatch(dir, now, dreamEvery, io.Discard)
		if err != nil || p.Task != "old" || p.Remaining.Old != want {
			t.Fatalf("sweep after restart: %+v, error %v. Want %d remaining", p, err, want)
		}
	}
	if len(*calls) == 0 {
		t.Fatal("no old groups reached the model")
	}
	for _, text := range *calls {
		if strings.Contains(text, "Noise candidate") {
			t.Fatalf("weekly sweep automatically offered an old noise candidate: %s", text)
		}
	}
	p, err = inv.serverDreamBatch(dir, now.Add(serverSweepEvery-time.Second), dreamEvery, io.Discard)
	if err != nil || p.Task != "" {
		t.Fatalf("sweep restarted before seven days: %+v, error %v", p, err)
	}
	p, err = inv.serverDreamBatch(dir, now.Add(serverSweepEvery), dreamEvery, io.Discard)
	if err != nil || p.Task != "old" || p.Remaining.Old != 5 {
		t.Fatalf("next weekly sweep did not start: %+v, error %v", p, err)
	}
}

func TestServerDreamAlternatesAndLogsRemainingWorkAtBudget(t *testing.T) {
	dir, st := newServerDreamFixture(t)
	fewPerPacket(t)
	oldMergeSweep := mergeSweep
	mergeSweep = 1
	t.Cleanup(func() { mergeSweep = oldMergeSweep })
	for i := range 12 {
		serverDreamFact(t, st, fmt.Sprintf("Synthetic weekly review sample %d.", i), "")
	}
	if err := markBase(dir); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := markDream(dir, now.Add(-2*dreamEvery)); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	id := 0
	addFresh := func(count int) error {
		mu.Lock()
		defer mu.Unlock()
		for range count {
			if _, err := st.AddWith(fmt.Sprintf("Synthetic incoming fact sample %d.", id), "example 2026-11-26", "", store.Meta{Kind: "pitfall"}); err != nil {
				return err
			}
			id++
		}
		return nil
	}
	if err := addFresh(12); err != nil {
		t.Fatal(err)
	}
	fakeDream(t, func(string) []extract.Action { return nil })
	fakeDreamRun(t, nil)
	good := dreamRunFn
	dreamRunFn = func(ctx context.Context, text string) (extract.DreamReply, error) {
		if hasFacts(packetBlock(text, "## Data: splitting")) {
			// Add new facts while the model is reviewing the prior snapshot.
			if err := addFresh(3); err != nil {
				return extract.DreamReply{}, err
			}
		}
		return good(ctx, text)
	}
	more, err := runServerDreamBatches(context.Background(), dir, dreamEvery)
	if err != nil || !more {
		t.Fatalf("burst did not yield pending work at the budget: more=%v, error=%v", more, err)
	}
	log, err := os.ReadFile(filepath.Join(dir, "dream.out"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(log)
	if strings.Count(got, "dream server fresh:") != 2 || strings.Count(got, "dream server old:") != 2 ||
		!strings.Contains(got, "budget;") || !strings.Contains(got, "4 old remaining") {
		t.Fatalf("burst did not alternate or log remaining work: %s", got)
	}
	inv := &invocation{ctx: context.Background()}
	for _, task := range []string{"fresh", "old"} {
		p, err := inv.serverDreamBatch(dir, now, dreamEvery, io.Discard)
		if err != nil || p.Task != task || p.Remaining.Fresh == 0 {
			t.Fatalf("while new facts arrive: %+v, error %v, want %s pass", p, err, task)
		}
		if task == "old" && p.Remaining.Old != 0 {
			t.Fatalf("weekly cycle extended into new facts: %+v", p.Remaining)
		}
	}
}

func TestServerDreamCancellationKeepsPacketPending(t *testing.T) {
	dir, st := newServerDreamFixture(t)
	now := time.Now()
	if err := markDream(dir, now.Add(-2*dreamEvery)); err != nil {
		t.Fatal(err)
	}
	serverDreamFact(t, st, "Synthetic fresh fact for the shutdown test.", "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	old := dreamRunFn
	t.Cleanup(func() { dreamRunFn = old })
	dreamRunFn = func(ctx context.Context, _ string) (extract.DreamReply, error) {
		cancel()
		return extract.DreamReply{}, ctx.Err()
	}
	more, err := runServerDreamBatches(ctx, dir, dreamEvery)
	if more || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled run: more=%v, error=%v", more, err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "dream.out"))
	if err != nil || !bytes.Contains(raw, []byte("canceled; 1 fresh")) {
		t.Fatalf("canceled work not logged as pending: %v, %s", err, raw)
	}
	fakeDream(t, func(string) []extract.Action { return nil })
	fakeDreamRun(t, nil)
	more, err = runServerDreamBatches(context.Background(), dir, dreamEvery)
	if more || err != nil {
		t.Fatalf("restart did not complete pending work: more=%v, error=%v", more, err)
	}
}
