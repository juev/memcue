package main

import (
	"bytes"
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/juev/memcue/internal/hlc"
	"github.com/juev/memcue/internal/server"
	"github.com/juev/memcue/internal/store"
)

func TestMigrateServerReportsAndLoadsAnEmptyServerOnly(t *testing.T) {
	env(t)
	local, err := store.Open(os.Getenv("MEMCUE_FACTS"))
	if err != nil {
		t.Fatal(err)
	}
	a, err := local.AddWith("loffe runs headscale", "global 2026-11-01", "", store.Meta{Kind: "environment"})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, err := local.Correct(a.ID, "loffe runs headscale behind caddy"); err != nil {
		t.Fatal(err)
	}
	b, err := local.AddWith("an outdated fact", "global 2026-11-01", "", store.Meta{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := local.Retire(b.ID); err != nil {
		t.Fatal(err)
	}
	_ = local.Close()

	srv, err := store.OpenServer(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	ts := httptest.NewServer(server.New(srv, log.New(io.Discard, "", 0), func(*http.Request) string { return "test" }))
	defer ts.Close()
	old := serverDial
	serverDial = func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", ts.Listener.Addr().String())
	}
	t.Cleanup(func() { serverDial = old })
	t.Setenv("MEMCUE_SERVER", "memcue-server")

	migrate := func(args ...string) (string, error) {
		inv := newInvocation()
		defer inv.close()
		var out bytes.Buffer
		err := inv.runMigrate(append([]string{"-server"}, args...), &out)
		return out.String(), err
	}
	report, err := migrate()
	if err != nil || !strings.Contains(report, "2 facts (1 active, 1 retired) from 4 versions") {
		t.Fatalf("report %q: %v", report, err)
	}
	if rows, _, _ := srv.Changes(0, 10); len(rows) != 0 {
		t.Fatalf("a report sent %d facts", len(rows))
	}
	if out, err := migrate("-apply"); err != nil || !strings.Contains(out, "loaded 2 facts") {
		t.Fatalf("apply %q: %v", out, err)
	}
	got, err := srv.Get(a.ID)
	if err != nil || got.Content != "loffe runs headscale behind caddy" || got.Kind != "environment" ||
		!got.Created.Equal(a.Created) || got.Version != 2 {
		t.Fatalf("server has %+v (%v), created %s", got, err, a.Created)
	}
	if got, err := srv.Get(b.ID); err != nil || got.Status != store.Retired {
		t.Fatalf("retired fact on the server: %+v %v", got, err)
	}
	rows, _, _ := srv.Changes(0, 10)
	for _, row := range rows {
		st, err := hlc.Parse(row.Stamps[store.RegContent])
		if err != nil || st.Millis != row.Created.UnixMilli() {
			t.Fatalf("stamp of %s is %q, want its creation time %s", row.ID, row.Stamps[store.RegContent], row.Created)
		}
	}
	if _, err := migrate("-apply"); err == nil || !strings.Contains(err.Error(), "already holds facts") {
		t.Fatalf("a second load: %v", err)
	}
	if rows, _, _ := srv.Changes(0, 10); len(rows) != 2 {
		t.Fatalf("a refused load changed the server: %d rows", len(rows))
	}
}

func TestDreamOnTheServerSeesANewTextAndNotNewLabels(t *testing.T) {
	env(t)
	path := filepath.Join(t.TempDir(), "server.db")
	s, err := store.OpenServer(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	// The dream of the server opens its base as the CLI opens any base.
	s, err = openFileBase(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	f, err := s.AddWith("dream runs on the server", "global 2026-11-10", "", store.Meta{Kind: "decision"})
	if err != nil {
		t.Fatal(err)
	}
	active, _ := s.Active()
	seen := versions(active)
	if _, err := s.Relabel(f.ID, 1, "rule", "global", ""); err != nil {
		t.Fatal(err)
	}
	active, _ = s.Active()
	if fresh, err := freshFacts(s, active, time.Time{}, seen); err != nil || len(fresh) != 0 {
		t.Fatalf("new labels made a fact fresh: %v %v", fresh, err)
	}
	if _, err := s.Correct(f.ID, "dream runs on the server only", 2); err != nil {
		t.Fatal(err)
	}
	active, _ = s.Active()
	if fresh, err := freshFacts(s, active, time.Time{}, seen); err != nil || len(fresh) != 1 {
		t.Fatalf("a new text is not fresh: %v %v", fresh, err)
	}
}

func TestServerDreamsWhenADreamIsDue(t *testing.T) {
	old := dreamCheck
	dreamCheck = 10 * time.Millisecond
	t.Cleanup(func() { dreamCheck = old })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var checks, dreams atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		runDreams(ctx, log.New(io.Discard, "", 0), func() (int, error) {
			if checks.Add(1) < 3 {
				return 0, nil
			}
			return 2, nil
		}, func(context.Context) error { dreams.Add(1); cancel(); return nil })
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("runDreams did not stop")
	}
	if checks.Load() != 3 || dreams.Load() != 1 {
		t.Fatalf("%d checks, %d dreams; want a dream at the third check", checks.Load(), dreams.Load())
	}
}

func TestServerRefusesAClientBackend(t *testing.T) {
	env(t)
	t.Setenv("MEMCUE_SERVER", "memcue-server")
	if err := runServer(nil); err == nil || !strings.Contains(err.Error(), "unset MEMCUE_SERVER") {
		t.Fatalf("server with MEMCUE_SERVER: %v", err)
	}
}
