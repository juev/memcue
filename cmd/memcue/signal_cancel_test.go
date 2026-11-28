package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/juev/memcue/internal/extract"
)

// signalledModel plays a model that never answers and an invocation that a SIGINT or SIGTERM ends as soon as the
// model call starts: only the caller's context can end the call.
func signalledModel(t *testing.T) (*invocation, func(context.Context) error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	var once sync.Once
	wait := func(call context.Context) error {
		once.Do(cancel)
		select {
		case <-call.Done():
			return call.Err()
		case <-time.After(10 * time.Second):
			return errors.New("the model call outlived the invocation")
		}
	}
	return &invocation{ctx: ctx, stop: cancel}, wait
}

// A signal ends a local migrate during its note extraction.
func TestMigrateStopsWithTheInvocation(t *testing.T) {
	dir := env(t)
	notePath := filepath.Join(dir, "claude", "memory", "lists", "a.md")
	write(t, notePath, "note a\n")
	inv, wait := signalledModel(t)
	noteFn = func(ctx context.Context, _, _ string) (extract.Result, error) {
		return extract.Result{}, wait(ctx)
	}
	t.Cleanup(func() { noteFn = extract.Note })
	var out strings.Builder
	start := time.Now()
	err := inv.runMigrate([]string{notePath}, &out)
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("migrate took %v after the cancellation (err %v)", took, err)
	}
}
