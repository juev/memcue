package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/juev/memcue/internal/server"
	"github.com/juev/memcue/internal/store"
)

func TestServeBaseAnswersAndStopsWithALongPollInFlight(t *testing.T) {
	st, err := store.OpenServer(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() {
		served <- serveBase(ctx, st, ln, func(*http.Request) string { return "test" }, log.New(io.Discard, "", 0))
	}()
	url := "http://" + ln.Addr().String() + "/v1/changes"
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	var c server.Changes
	err = json.NewDecoder(resp.Body).Decode(&c)
	_ = resp.Body.Close()
	if err != nil || resp.StatusCode != http.StatusOK || c.BaseID == "" {
		t.Fatalf("GET: %d %+v %v", resp.StatusCode, c, err)
	}
	polled := make(chan struct{})
	go func() {
		if resp, err := http.Get(url + "?wait=60s"); err == nil {
			_ = resp.Body.Close()
		}
		close(polled)
	}()
	time.Sleep(100 * time.Millisecond)
	start := time.Now()
	cancel()
	select {
	case err := <-served:
		if err != nil {
			t.Fatalf("serveBase: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serveBase did not stop with a long poll in flight")
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("stopping took %s", d)
	}
	<-polled
}

func TestServerRefusesABaseWithAHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "facts.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddWith("a local fact", "x 2026-11-10", "", store.Meta{}); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	t.Setenv("MEMCUE_FACTS", path)
	t.Setenv("MEMCUE_TAILSCALE_STATE_DIR", filepath.Join(t.TempDir(), "tailnet"))
	if err := runServer(nil); !errors.Is(err, store.ErrSyncedBase) {
		t.Fatalf("server on a base with a history: %v", err)
	}
	if err := runServer([]string{"extra"}); err == nil {
		t.Fatal("server took an argument")
	}
}
