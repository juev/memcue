package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/juev/memcue/internal/embed"
	"github.com/juev/memcue/internal/recall"
	"github.com/juev/memcue/internal/store"
)

// vectorParams reads the meaning-pass settings from the environment. Without MEMCUE_EMBED_URL and
// MEMCUE_EMBED_MODEL the returned params have no embedder, and recall is exactly what it was before.
func vectorParams() recall.VectorParams {
	vp := recall.VectorParams{
		Margin:   embed.ParseMargin("MEMCUE_EMBED_MARGIN", recall.DefaultVectorMargin),
		MinScore: embed.ParseScore("MEMCUE_EMBED_MIN_SCORE"),
		MaxFacts: embed.ParseCount("MEMCUE_EMBED_MAX_FACTS"),
	}
	set := embed.FromEnv()
	if set == nil {
		return vp
	}
	// The hook runs as a fresh process on every prompt, so the pause after a failure is remembered in a file rather
	// than being paid again by each of the next turns.
	if dir, err := stateDir(); err == nil {
		set.StateFile = filepath.Join(dir, "embed.pause")
	}
	vp.Embedder = embed.New(set)
	return vp
}

// meaning marks a fact the vector pass added: the words never found it, so its coverage and matched count say
// nothing about why it came.
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
