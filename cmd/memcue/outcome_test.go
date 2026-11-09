package main

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/juev/memcue/internal/store"
)

type lostReceiptWriter struct {
	deadline bool
	cancel   context.CancelFunc
}

func (w lostReceiptWriter) Write(p []byte) (int, error) {
	if w.deadline {
		w.cancel()
		return 0, context.DeadlineExceeded
	}
	return len(p) / 2, io.ErrShortWrite
}

func TestMutationReceiptLoss(t *testing.T) {
	dsn := os.Getenv("MEMCUE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	t.Setenv("MEMCUE_POSTGRES_DSN", dsn)
	t.Setenv("MEMCUE_STATE_DIR", t.TempDir())
	for _, mode := range []string{"short", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			s, e := store.OpenPostgresRequest(dsn, 4*time.Second)
			if e != nil {
				t.Fatal(e)
			}
			f, e := s.AddWith("Prefer concrete examples in explanations.", "", "", store.Meta{Source: "user", Kind: "rule"})
			if e != nil {
				t.Fatal(e)
			}
			if e = s.Close(); e != nil {
				t.Fatal(e)
			}
			operations := []struct {
				name    string
				version int
				run     func(*invocation, io.Writer) error
			}{
				{"add", 1, func(i *invocation, w io.Writer) error {
					return i.runAdd([]string{"-source", "user", "--", "Prefer clear explanations in reviews."}, w)
				}},
				{"correct", 2, func(i *invocation, w io.Writer) error {
					return i.runCorrect([]string{"-expected-version", "1", "--", f.ID, "Prefer examples and concise explanations."}, w)
				}},
				{"retire", 3, func(i *invocation, w io.Writer) error {
					return i.runRetire([]string{"-expected-version", "2", "--", f.ID}, w)
				}},
				{"reopen", 4, func(i *invocation, w io.Writer) error { return i.runReopen([]string{f.ID}, w) }},
			}
			for _, op := range operations {
				t.Run(op.name, func(t *testing.T) {
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					inv := &invocation{ctx: ctx, stop: cancel}
					e := op.run(inv, lostReceiptWriter{deadline: mode == "deadline", cancel: cancel})
					// Independent real client observes the durable version before the outcome oracle.
					second, e2 := store.OpenPostgresRequest(dsn, 4*time.Second)
					if e2 != nil {
						t.Fatal(e2)
					}
					defer func() { _ = second.Close() }()
					h, e2 := second.History(f.ID)
					if e2 != nil {
						t.Fatal(e2)
					}
					// PostgreSQL keeps the current version only (#700): it carries the version number.
					if op.name != "add" && (len(h) != 1 || h[0].Version != op.version) {
						t.Fatalf("commit precondition: versions=%+v want=%d", h, op.version)
					}
					if e == nil || !strings.Contains(strings.ToUpper(e.Error()), "UNKNOWN OUTCOME") {
						t.Fatalf("%s committed but response loss lacks explicit UNKNOWN OUTCOME: %v", op.name, e)
					}
					if !strings.Contains(e.Error(), "history") {
						t.Fatal("missing history recovery")
					}
				})
			}
			// A stale explicit retry cannot append another version.
			inv := newInvocation()
			defer inv.close()
			e = inv.runCorrect([]string{"-expected-version", "1", "--", f.ID, "Prefer concise examples."}, io.Discard)
			if !errors.Is(e, store.ErrChanged) {
				t.Fatalf("stale retry: %v", e)
			}
		})
	}
}
