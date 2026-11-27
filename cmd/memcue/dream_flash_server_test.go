package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/juev/memcue/internal/store"
)

func TestServerDreamFlashRetiresFreshSingletonNoise(t *testing.T) {
	dir, st := newServerDreamFixture(t)
	if err := markBase(dir); err != nil {
		t.Fatal(err)
	}
	fact, err := st.AddWith("The finished experiment selected the saffron menu for a one-off preview; it recorded no reusable result.",
		"example 2026-11-26", "", store.Meta{Kind: "other", Scope: "repo:example/ui"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	state := serverDreamState{FreshAt: now.Add(-dreamEvery), SweepAt: now}
	if err := writeJSONFile(filepath.Join(dir, serverDreamFile), state); err != nil {
		t.Fatal(err)
	}
	server, mu, kinds := flashEndpoint(t, false, "")
	withFlashEndpoint(t, server)
	p, err := (&invocation{ctx: context.Background()}).serverDreamBatch(dir, now, dreamEvery, &bytes.Buffer{})
	if err != nil || p.Task != "fresh" || p.Completed.Fresh != 1 {
		t.Fatalf("fresh batch: %+v, error %v", p, err)
	}
	got, err := st.Get(fact.ID)
	if err != nil || got.Status != store.Retired {
		t.Fatalf("fresh noise status %q, error %v; want retired", got.Status, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(strings.Join(*kinds, ","), "noise") {
		t.Fatalf("Flash noise stage was not called: %v", *kinds)
	}
}

func TestServerDreamFlashWeeklySweepLeavesSingletonNoiseForManualReview(t *testing.T) {
	dir, st := newServerDreamFixture(t)
	add := func(content, kind, scope string) string {
		t.Helper()
		f, err := st.AddWith(content, "synthetic fixture", "", store.Meta{Kind: kind, Scope: scope})
		if err != nil {
			t.Fatal(err)
		}
		return f.ID
	}
	noiseID := add("The finished experiment selected the saffron menu for a one-off preview; it recorded no reusable result.",
		"other", "repo:example/ui")
	leftID := add("Workflow nightly-sync.yml in repo:example/nightly-sync subscribes bot to all repositories except forks and archived ones.",
		"rule", "repo:example/nightly-sync")
	rightID := add("In repo:example/nightly-sync, the nightly-sync.yml workflow subscribes bot to all repositories except forks.",
		"rule", "repo:example/nightly-sync")
	add("In repo:example/metrics, set the umber sampler interval to 5 seconds to preserve short-lived spike measurements.",
		"rule", "repo:example/metrics")
	add("Keep the cobalt audit trail append-only so recovery can replay each acknowledged event.", "rule", "global")
	if err := markBase(dir); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	state := serverDreamState{FreshAt: now, SweepAt: now.Add(-serverSweepEvery)}
	if err := writeJSONFile(filepath.Join(dir, serverDreamFile), state); err != nil {
		t.Fatal(err)
	}
	server, mu, kinds := flashEndpoint(t, false, "")
	withFlashEndpoint(t, server)
	p, err := (&invocation{ctx: context.Background()}).serverDreamBatch(dir, now, dreamEvery, &bytes.Buffer{})
	if err != nil || p.Task != "old" || p.Completed.Old == 0 {
		t.Fatalf("weekly sweep: %+v, error %v", p, err)
	}
	noise, err := st.Get(noiseID)
	if err != nil || noise.Status != store.Active {
		t.Fatalf("old singleton noise status %q, error %v; want active", noise.Status, err)
	}
	duplicate, err := st.Get(rightID)
	if err != nil || duplicate.Status != store.Retired {
		t.Fatalf("old duplicate status %q, error %v; want retired", duplicate.Status, err)
	}
	anchor, err := st.Get(leftID)
	if err != nil || anchor.Status != store.Active {
		t.Fatalf("old pair anchor status %q, error %v; want active", anchor.Status, err)
	}
	mu.Lock()
	defer mu.Unlock()
	seen := map[string]int{}
	for _, kind := range *kinds {
		seen[kind]++
	}
	if seen["pairs"] == 0 || seen["confirmation"] == 0 || seen["noise"] != 0 {
		t.Fatalf("weekly Flash stages %v; want pair judgment and confirmation, no noise screen", seen)
	}
}
