package store

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/juev/memcue/internal/hlc"
)

var syncT0 = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

func openRole(t *testing.T, open func(string) (*Store, error), at time.Time) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "base.db")
	s, err := open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return at }
	t.Cleanup(func() { _ = s.Close() })
	return s, path
}

// push sends the replica's outbox to the server and acknowledges what the server accepted.
func push(t *testing.T, r, srv *Store) []Applied {
	t.Helper()
	out, err := r.Outbox()
	if err != nil {
		t.Fatal(err)
	}
	res, err := srv.ApplyChanges(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range res {
		if a.Refused != "" {
			continue
		}
		if err := r.Acknowledge(a.OpID, a.Seq); err != nil {
			t.Fatal(err)
		}
	}
	return res
}

// pull applies to the replica every server row after its last_seq.
func pull(t *testing.T, srv, r *Store) {
	t.Helper()
	st, err := r.State()
	if err != nil {
		t.Fatal(err)
	}
	rows, _, err := srv.Changes(st.LastSeq, 1000)
	if err != nil {
		t.Fatal(err)
	}
	base, err := srv.BaseID()
	if err != nil {
		t.Fatal(err)
	}
	if err := r.ApplyRows(base, rows); err != nil {
		t.Fatal(err)
	}
}

func mustGet(t *testing.T, s *Store, id string) Fact {
	t.Helper()
	f, err := s.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func outboxLen(t *testing.T, r *Store) int {
	t.Helper()
	out, err := r.Outbox()
	if err != nil {
		t.Fatal(err)
	}
	return len(out)
}

func TestReplicaWriteIsFoundAtOnceAndQueued(t *testing.T) {
	r, _ := openRole(t, OpenReplica, syncT0)
	f, err := r.AddWith("Gradle needs JAVA_HOME pointing at openjdk 21", "x 2026-11-10", "", Meta{Kind: "pitfall", Scope: "repo:x"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ids(t, r, "gradle java_home")[f.ID]; !ok {
		t.Fatal("a replica write is not found by search")
	}
	if _, err := r.Correct(f.ID, "Gradle needs JAVA_HOME pointing at openjdk 25", 1); err != nil {
		t.Fatal(err)
	}
	h, err := r.History(f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(h) != 1 || h[0].Version != 2 || h[0].Content != "Gradle needs JAVA_HOME pointing at openjdk 25" {
		t.Fatalf("a replica keeps one row per fact, got %+v", h)
	}
	out, err := r.Outbox()
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || !out[0].Create || out[1].Create || out[1].Base != out[0].Stamp ||
		!reflect.DeepEqual(out[1].Values, map[string]string{RegContent: "Gradle needs JAVA_HOME pointing at openjdk 25"}) {
		t.Fatalf("outbox %+v", out)
	}
	st, err := r.State()
	if err != nil {
		t.Fatal(err)
	}
	if st.Outbox != 2 || !st.LastSync.IsZero() || st.BaseID != "" {
		t.Fatalf("state of a replica that never synced: %+v", st)
	}
}

func TestReplicaChangesReachAnotherReplicaThroughTheServer(t *testing.T) {
	srv, _ := openRole(t, OpenServer, syncT0)
	a, _ := openRole(t, OpenReplica, syncT0)
	b, _ := openRole(t, OpenReplica, syncT0)
	f, err := a.AddWith("loffe runs headscale behind caddy", "global 2026-11-10", "", Meta{Kind: "environment"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Correct(f.ID, "loffe runs headscale behind caddy on port 8080", 1); err != nil {
		t.Fatal(err)
	}
	push(t, a, srv)
	if n := outboxLen(t, a); n != 0 {
		t.Fatalf("outbox keeps %d accepted changes", n)
	}
	pull(t, srv, b)
	got := mustGet(t, b, f.ID)
	if got.Content != "loffe runs headscale behind caddy on port 8080" || got.Kind != "environment" || got.Source != "agent" ||
		!got.Created.Equal(syncT0) {
		t.Fatalf("replica B got %+v", got)
	}
	if _, ok := ids(t, b, "headscale caddy")[f.ID]; !ok {
		t.Fatal("a pulled fact is not found by search")
	}
	_, head, err := srv.Changes(0, 10)
	if err != nil {
		t.Fatal(err)
	}
	st, err := b.State()
	if err != nil {
		t.Fatal(err)
	}
	base, _ := srv.BaseID()
	if st.LastSeq != head || st.BaseID != base || st.LastSync.IsZero() {
		t.Fatalf("state %+v, head %d, base %s", st, head, base)
	}
}

func TestServerRowKeepsTheReplicasPendingChange(t *testing.T) {
	srv, _ := openRole(t, OpenServer, syncT0)
	a, _ := openRole(t, OpenReplica, syncT0)
	b, _ := openRole(t, OpenReplica, syncT0.Add(time.Second))
	f, err := a.AddWith("the sync server listens on the tailnet only", "global 2026-11-10", "", Meta{Kind: "decision"})
	if err != nil {
		t.Fatal(err)
	}
	push(t, a, srv)
	pull(t, srv, b)
	if _, err := b.Relabel(f.ID, 1, "rule", "global", ""); err != nil {
		t.Fatal(err)
	}
	push(t, b, srv)
	if _, err := a.Correct(f.ID, "the sync server listens on the tailnet only, never on a host port", 1); err != nil {
		t.Fatal(err)
	}
	pull(t, srv, a)
	got := mustGet(t, a, f.ID)
	if got.Kind != "rule" || got.Content != "the sync server listens on the tailnet only, never on a host port" {
		t.Fatalf("after the server row, replica A has %+v", got)
	}
	if n := outboxLen(t, a); n != 1 {
		t.Fatalf("the server row dropped the outbox: %d entries", n)
	}
	if _, ok := ids(t, a, "host port")[f.ID]; !ok {
		t.Fatal("the pending correction is not found after the server row")
	}
	push(t, a, srv)
	if got := mustGet(t, srv, f.ID); got.Kind != "rule" || got.Content != "the sync server listens on the tailnet only, never on a host port" {
		t.Fatalf("server has %+v", got)
	}
}

func TestConcurrentCorrectionsKeepTheLosingText(t *testing.T) {
	srv, _ := openRole(t, OpenServer, syncT0)
	a, _ := openRole(t, OpenReplica, syncT0)
	b, _ := openRole(t, OpenReplica, syncT0.Add(time.Second))
	f, err := a.AddWith("dream runs on loffe", "global 2026-11-10", "", Meta{Kind: "decision"})
	if err != nil {
		t.Fatal(err)
	}
	push(t, a, srv)
	pull(t, srv, b)
	if _, err := a.Correct(f.ID, "dream runs on the server only", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Correct(f.ID, "dream runs every 24h", 1); err != nil {
		t.Fatal(err)
	}
	push(t, a, srv)
	res := push(t, b, srv)
	if len(res) != 1 || res[0].Loser == "" {
		t.Fatalf("a concurrent correction kept no loser: %+v", res)
	}
	if got := mustGet(t, srv, f.ID); got.Content != "dream runs every 24h" {
		t.Fatalf("the later correction lost: %+v", got)
	}
	loser := mustGet(t, srv, res[0].Loser)
	if loser.Content != "dream runs on the server only" || loser.Kind != "decision" || loser.Origin != "global 2026-11-10" ||
		loser.Status != Active {
		t.Fatalf("loser %+v", loser)
	}
	if of, err := srv.LoserOf(loser.ID); err != nil || of != f.ID {
		t.Fatalf("loser refers to %q (%v), want %s", of, err, f.ID)
	}
	pull(t, srv, a)
	if _, ok := ids(t, a, "server only")[loser.ID]; !ok {
		t.Fatal("the losing text does not reach the replica")
	}
	if got := mustGet(t, a, f.ID); got.Content != "dream runs every 24h" {
		t.Fatalf("replica A did not converge: %+v", got)
	}
}

func TestSequentialCorrectionThroughTheServerKeepsNoLoser(t *testing.T) {
	srv, _ := openRole(t, OpenServer, syncT0)
	a, _ := openRole(t, OpenReplica, syncT0)
	b, _ := openRole(t, OpenReplica, syncT0.Add(time.Second))
	f, err := a.AddWith("first text", "global 2026-11-10", "", Meta{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Correct(f.ID, "second text", 1); err != nil {
		t.Fatal(err)
	}
	push(t, a, srv)
	pull(t, srv, b)
	if _, err := b.Correct(f.ID, "third text", 2); err != nil {
		t.Fatal(err)
	}
	for _, r := range push(t, b, srv) {
		if r.Loser != "" {
			t.Fatalf("a correction made after seeing the previous one kept a loser: %+v", r)
		}
	}
}

func TestRetireAgainstACorrectionStaysRetiredAndReopensCorrected(t *testing.T) {
	srv, _ := openRole(t, OpenServer, syncT0)
	a, _ := openRole(t, OpenReplica, syncT0.Add(time.Second))
	b, _ := openRole(t, OpenReplica, syncT0)
	f, err := a.AddWith("caddy fronts headscale", "global 2026-11-10", "", Meta{})
	if err != nil {
		t.Fatal(err)
	}
	push(t, a, srv)
	pull(t, srv, b)
	if _, err := a.Retire(f.ID, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Correct(f.ID, "caddy fronts headscale on loffe", 1); err != nil {
		t.Fatal(err)
	}
	push(t, b, srv)
	push(t, a, srv)
	pull(t, srv, a)
	got := mustGet(t, a, f.ID)
	if got.Status != Retired || got.Content != "caddy fronts headscale on loffe" {
		t.Fatalf("retire against correct: %+v", got)
	}
	if _, ok := ids(t, a, "caddy headscale")[f.ID]; ok {
		t.Fatal("a retired fact is found")
	}
	back, err := a.Restore(f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if back.Status != Active || back.Content != "caddy fronts headscale on loffe" {
		t.Fatalf("reopen gave %+v", back)
	}
}

func TestRetriedOperationIsAppliedOnce(t *testing.T) {
	srv, _ := openRole(t, OpenServer, syncT0)
	a, _ := openRole(t, OpenReplica, syncT0)
	f, err := a.AddWith("one write", "global 2026-11-10", "", Meta{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Correct(f.ID, "one write, corrected", 1); err != nil {
		t.Fatal(err)
	}
	out, err := a.Outbox()
	if err != nil {
		t.Fatal(err)
	}
	first, err := srv.ApplyChanges(out)
	if err != nil {
		t.Fatal(err)
	}
	_, head, _ := srv.Changes(0, 10)
	again, err := srv.ApplyChanges(out)
	if err != nil {
		t.Fatal(err)
	}
	_, head2, _ := srv.Changes(0, 10)
	if !reflect.DeepEqual(first, again) || head != head2 || mustGet(t, srv, f.ID).Version != 2 {
		t.Fatalf("retry: first %+v, again %+v, head %d then %d", first, again, head, head2)
	}
}

func TestServerRefusesACreateOfATakenID(t *testing.T) {
	srv, _ := openRole(t, OpenServer, syncT0)
	ch := createChange("01JDTAKEN0000000000000000A", "first", stamp(100, devA))
	if res, err := srv.ApplyChanges([]Change{ch}); err != nil || res[0].Refused != "" {
		t.Fatalf("create: %+v %v", res, err)
	}
	other := createChange("01JDTAKEN0000000000000000A", "other", stamp(200, devB))
	other.OpID, other.Created = "op-other", created.Add(time.Hour)
	res, err := srv.ApplyChanges([]Change{other, {ID: "X", Values: map[string]string{RegKind: "rule"}}})
	if err != nil {
		t.Fatal(err)
	}
	if res[0].Refused != RefusedIDTaken || res[1].Refused != RefusedMalformed {
		t.Fatalf("refusals %+v", res)
	}
	if got := mustGet(t, srv, ch.ID); got.Content != "first" {
		t.Fatalf("a refused create changed the fact: %+v", got)
	}
}

func TestResetReplicaKeepsTheOutbox(t *testing.T) {
	srv, _ := openRole(t, OpenServer, syncT0)
	a, _ := openRole(t, OpenReplica, syncT0)
	synced, err := a.AddWith("synced before the reset", "global 2026-11-10", "", Meta{})
	if err != nil {
		t.Fatal(err)
	}
	push(t, a, srv)
	pull(t, srv, a)
	local, err := a.AddWith("written offline before the reset", "global 2026-11-10", "", Meta{})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.ResetReplica("new-base"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Get(synced.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a row of the old server base survived the reset: %v", err)
	}
	if _, ok := ids(t, a, "offline reset")[local.ID]; !ok {
		t.Fatal("a fact only in the outbox is not found after the reset")
	}
	st, err := a.State()
	if err != nil {
		t.Fatal(err)
	}
	if st.Outbox != 1 || st.LastSeq != 0 || st.BaseID != "new-base" {
		t.Fatalf("state after reset %+v", st)
	}
	old, _ := srv.BaseID()
	if err := a.ApplyRows(old, nil); !errors.Is(err, ErrBaseChanged) {
		t.Fatalf("rows of another base: %v", err)
	}
}

func TestReplicaClockIsSharedAndPassesReceivedStamps(t *testing.T) {
	r, path := openRole(t, OpenReplica, syncT0)
	r2, err := OpenReplica(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r2.Close() })
	r2.now = func() time.Time { return syncT0.Add(-time.Hour) } // a second process whose wall clock is behind
	if _, err := r.AddWith("first process", "global 2026-11-10", "", Meta{}); err != nil {
		t.Fatal(err)
	}
	if _, err := r2.AddWith("second process", "global 2026-11-10", "", Meta{}); err != nil {
		t.Fatal(err)
	}
	out, err := r.Outbox()
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[1].Stamp <= out[0].Stamp {
		t.Fatalf("processes of one replica do not share the clock: %q then %q", out[0].Stamp, out[1].Stamp)
	}
	ahead := hlc.Stamp{Millis: syncT0.Add(time.Hour).UnixMilli(), Device: devB}.String()
	row := Row{Fact: Fact{ID: "01JDAHEAD00000000000000000", Version: 1, Content: "from a clock ahead", Created: syncT0,
		Status: Active, Meta: Meta{Source: "agent"}}, Stamps: Stamps{}, Seq: 1}
	for _, reg := range Registers {
		row.Stamps[reg] = ahead
	}
	if err := r.ApplyRows("base", []Row{row}); err != nil {
		t.Fatal(err)
	}
	f, err := r.AddWith("after the received row", "global 2026-11-10", "", Meta{})
	if err != nil {
		t.Fatal(err)
	}
	out, err = r.Outbox()
	if err != nil {
		t.Fatal(err)
	}
	last := out[len(out)-1]
	if last.ID != f.ID || last.Stamp <= ahead {
		t.Fatalf("a stamp issued after %q is %q", ahead, last.Stamp)
	}
}

func TestSyncedBasesRefuseOtherRoles(t *testing.T) {
	_, replica := openRole(t, OpenReplica, syncT0)
	if _, err := Open(replica); !errors.Is(err, ErrSyncedBase) {
		t.Fatalf("Open of a replica: %v", err)
	}
	if _, err := OpenServer(replica); !errors.Is(err, ErrSyncedBase) {
		t.Fatalf("OpenServer of a replica: %v", err)
	}
	again, err := OpenReplica(replica)
	if err != nil {
		t.Fatal(err)
	}
	_ = again.Close()

	plain, path := open(t)
	if _, err := plain.AddWith("a fact with a history", "global 2026-11-10", "", Meta{}); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenReplica(path); !errors.Is(err, ErrSyncedBase) {
		t.Fatalf("OpenReplica of a base with facts: %v", err)
	}
	if _, err := OpenServer(path); !errors.Is(err, ErrSyncedBase) {
		t.Fatalf("OpenServer of a base with facts: %v", err)
	}
}
