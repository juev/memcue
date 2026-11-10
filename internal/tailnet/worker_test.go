package tailnet

import (
	"context"
	"encoding/json"
	"net"
	"sync"
	"testing"
	"time"
)

// fixtureWorker dials once and reports what it saw.
type fixtureWorker struct {
	mu      sync.Mutex
	dialed  bool
	stopped chan struct{}
}

func (w *fixtureWorker) Run(ctx context.Context, dial func(context.Context) (net.Conn, error)) {
	defer close(w.stopped)
	if conn, err := dial(ctx); err == nil {
		_ = conn.Close()
		w.mu.Lock()
		w.dialed = true
		w.mu.Unlock()
	}
	<-ctx.Done()
}

func (w *fixtureWorker) Status() any {
	w.mu.Lock()
	defer w.mu.Unlock()
	return map[string]bool{"dialed": w.dialed}
}

func TestHelperRunsItsWorkerAndReportsItWithTheVersion(t *testing.T) {
	w := &fixtureWorker{stopped: make(chan struct{})}
	cfg := Config{Dir: privateFixtureDir(t), Hostname: "fixture", Host: "memcue-server", Port: "80", Server: true,
		Version: "9.9.9", Worker: w, Timeout: time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- serve(ctx, cfg, &fixtureNode{}, ready) }()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("helper: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("helper failed to start")
	}
	var sync map[string]bool
	deadline := time.Now().Add(3 * time.Second)
	for {
		s, err := Admin(ctx, cfg, "status")
		if err != nil {
			t.Fatal(err)
		}
		if s.Version != "9.9.9" {
			t.Fatalf("status version %q", s.Version)
		}
		if err := json.Unmarshal(s.Sync, &sync); err != nil {
			t.Fatalf("status sync %q: %v", s.Sync, err)
		}
		if sync["dialed"] || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !sync["dialed"] {
		t.Fatal("the worker never reached the server through the node")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("helper did not stop")
	}
	select {
	case <-w.stopped:
	default:
		t.Fatal("the helper returned while its worker still ran")
	}
}

func TestServerProfileDiffersFromAPostgresProfileOnly(t *testing.T) {
	pg := Config{Hostname: "memcue", ControlURL: "https://c.example", Host: "db", Port: "5432"}
	if got := string(profileBytes(pg)); got != `{"Version":1,"Hostname":"memcue","ControlURL":"https://c.example","Host":"db","Port":"5432"}` {
		t.Fatalf("a PostgreSQL profile changed: %s", got)
	}
	srv := pg
	srv.Host, srv.Port, srv.Server = "db", "5432", true
	if string(profileBytes(srv)) == string(profileBytes(pg)) {
		t.Fatal("a server profile reads as the PostgreSQL profile of the same host and port")
	}
}
