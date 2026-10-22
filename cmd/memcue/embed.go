package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/juev/memcue/internal/embed"
	"github.com/juev/memcue/internal/store"
)

// meaning shows the judge's score of a fact, when the judge decided.
func meaning(sim float64) string {
	if sim <= 0 {
		return ""
	}
	return fmt.Sprintf(" meaning=%.2f", sim)
}

// runEmbed fills the vectors of the active facts that have none for the configured model. It is the only place that
// writes vectors: a turn never waits for an embedding of a fact.
func runEmbed(args []string, out, errOut io.Writer) error {
	fl := flag.NewFlagSet("embed", flag.ContinueOnError)
	fl.SetOutput(errOut)
	dry := fl.Bool("dry-run", false, "only report how many active facts need a vector")
	batch := fl.Int("batch", 32, "facts per request")
	limit := fl.Int("limit", 0, "embed at most this many facts")
	timeout := fl.Duration("timeout", time.Minute, "one request: a batch of facts, and a cold model, take longer than a prompt")
	if err := fl.Parse(args); err != nil {
		return err
	}
	set := embed.FromEnv()
	if set == nil {
		return errors.New("MEMCUE_EMBED_URL and MEMCUE_EMBED_MODEL are not set")
	}
	set.Timeout = *timeout
	client := embed.New(set)
	if !*dry {
		// Every write starts an embed: one at a time, so two of them do not pay for the same facts. The one that
		// waited reads the missing vectors after the first has saved its own.
		unlock, err := lockEmbed()
		if err != nil {
			return err
		}
		defer unlock()
	}
	return withStore(func(s *store.Store) error {
		pending, err := s.MissingVectors(client.Tag(), *limit)
		if err != nil {
			return err
		}
		if *dry {
			_, _ = fmt.Fprintf(out, "%d facts need a vector for %s\n", len(pending), client.Tag())
			return nil
		}
		if len(pending) == 0 {
			_, _ = fmt.Fprintf(out, "nothing to do: every active fact has a vector for %s\n", client.Tag())
			return nil
		}
		size := *batch
		if size <= 0 {
			size = 32
		}
		done := 0
		for start := 0; start < len(pending); start += size {
			end := min(start+size, len(pending))
			chunk := pending[start:end]
			texts := make([]string, len(chunk))
			for i, f := range chunk {
				texts[i] = f.Content
			}
			vectors, err := client.Embed(context.Background(), texts)
			if err != nil {
				return fmt.Errorf("after %d of %d facts: %w", done, len(pending), err)
			}
			for i, f := range chunk {
				if err := s.SaveVector(f.ID, client.Tag(), store.TextHash(f.Content), vectors[i]); err != nil {
					return err
				}
			}
			done += len(chunk)
			_, _ = fmt.Fprintf(errOut, "embedded %d/%d\n", done, len(pending))
		}
		_, _ = fmt.Fprintf(out, "embedded %d facts for %s\n", done, client.Tag())
		return nil
	})
}

// embedWrites are the commands after which an active fact may have no vector: a fact added, corrected or approved.
// A retire needs none, and merge runs inside git as the merge driver, where the base is not yet the one recall reads.
var embedWrites = map[string]bool{"add": true, "correct": true, "approve": true, "reopen": true, "vet": true,
	"dream": true, "extract-session": true, "extract-messages": true}

// startEmbed runs `exe embed` in its own process, so a fact a write left without a vector reaches the vector reader
// without the write or the turn waiting for the endpoint. Without MEMCUE_EMBED_* it does nothing. It is best effort:
// a failure leaves the vectors to the next write or a manual `memcue embed`, and goes to embed.log.
func startEmbed(exe string) {
	if embed.FromEnv() == nil {
		return
	}
	dir, err := stateDir()
	if err != nil {
		return
	}
	logf, err := os.OpenFile(filepath.Join(dir, "embed.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = logf.Close() }()
	cmd := exec.Command(exe, "embed")
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // outlive a hook's process group
	if err := cmd.Start(); err != nil {
		_, _ = fmt.Fprintln(logf, "memcue: start embed:", err)
		return
	}
	_ = cmd.Process.Release()
}

// embedAfterWrite starts an embed for this binary; see startEmbed.
func embedAfterWrite() {
	if exe, err := os.Executable(); err == nil {
		startEmbed(exe)
	}
}

// lockEmbed takes the embed lock of the base, waiting for an embed already running.
func lockEmbed() (func(), error) {
	dir, err := stateDir()
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "embed.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
