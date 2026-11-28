package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRepair3HookInputCancellation(t *testing.T) {
	r, w, e := os.Pipe()
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = r.Close() }()
	defer func() { _ = w.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	inv := &invocation{ctx: ctx, stop: cancel}
	done := make(chan struct{})
	go func() { time.Sleep(350 * time.Millisecond); _ = w.Close(); close(done) }()
	var out bytes.Buffer
	start := time.Now()
	e = inv.runSessionCardMode(r, &out, true)
	elapsed := time.Since(start)
	<-done
	if !errors.Is(e, context.DeadlineExceeded) || elapsed > 250*time.Millisecond {
		t.Fatalf("pre-open native hook input escaped cancellation: %s %v", elapsed, e)
	}
}

func TestRepair3EnvelopeLimitRefusesWithoutTruncation(t *testing.T) {
	inv := newInvocation()
	defer inv.close()
	data := `{"cwd":"` + strings.Repeat("a", 80) + `"}`
	if _, e := inv.inputBytesLimit(strings.NewReader(data), 64); e == nil {
		t.Fatal("oversized control envelope was accepted instead of refused")
	}
}
