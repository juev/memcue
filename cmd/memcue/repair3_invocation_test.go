package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRepair3SessionCardInvocationBudget(t *testing.T) {
	if os.Getenv("MEMCUE_REPAIR_CLI_DSN") == "" {
		t.Skip("isolated PostgreSQL required")
	}
	root := t.TempDir()
	fixture, err := url.Parse(os.Getenv("MEMCUE_REPAIR_CLI_DSN"))
	if err != nil || fixture.Host == "" {
		t.Fatal("invalid isolated PostgreSQL fixture DSN")
	}
	backend := fixture.Host
	repo := filepath.Join(root, "synthetic-repo")
	if e := os.MkdirAll(filepath.Join(repo, ".git"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(repo, ".git", "config"), []byte("[remote \"origin\"]\n url = https://example.invalid/review/project.git\n"), 0600); e != nil {
		t.Fatal(e)
	}
	t.Setenv("MEMCUE_STATE_DIR", filepath.Join(root, "session-state"))
	t.Setenv("MEMCUE_FACTS", filepath.Join(root, "no-local.db"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, "empty-claude"))
	t.Setenv("MEMCUE_TAILSCALE_MODE", "off")
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = ln.Close() }()
	var n atomic.Int32
	var wg sync.WaitGroup
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, e := ln.Accept()
			if e != nil {
				return
			}
			i := n.Add(1)
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { _ = c.Close() }()
				up, e := net.DialTimeout("tcp", backend, time.Second)
				if e != nil {
					return
				}
				defer func() { _ = up.Close() }()
				if i == 1 || i == 3 {
					time.Sleep(2200 * time.Millisecond)
				}
				end := make(chan struct{})
				go func() { _, _ = io.Copy(up, c); _ = up.Close(); close(end) }()
				_, _ = io.Copy(c, up)
				_ = c.Close()
				_ = up.Close()
				<-end
			}()
		}
	}()
	fixture.Host = ln.Addr().String()
	t.Setenv("MEMCUE_POSTGRES_DSN", fixture.String())
	var out bytes.Buffer
	start := time.Now()
	e = runSessionCardMode(strings.NewReader(fmt.Sprintf(`{"cwd":%q}`, repo)), &out, true)
	elapsed := time.Since(start)
	_ = ln.Close()
	<-done
	wg.Wait()
	if e != nil && !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal(e)
	}
	if elapsed > 4300*time.Millisecond {
		t.Fatalf("supported session-card reset its invocation budget across %d connections: %s", n.Load(), elapsed)
	}
	t.Logf("aggregate supported session-card completed in %s, connections=%d", elapsed, n.Load())
}
