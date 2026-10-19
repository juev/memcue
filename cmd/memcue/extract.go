package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/juev/memcue/internal/extract"
	"github.com/juev/memcue/internal/store"
	"github.com/juev/memcue/internal/tier"
	"github.com/juev/memcue/internal/transcript"
)

// maxChunk is the most transcript text one model call reads; a longer session goes in several calls.
const maxChunk = 200_000

// runExtract turns a session transcript into facts: memcue extract [-dry-run] [-from N] [-model M] TRANSCRIPT.jsonl.
// Facts are only added, never compared with the base. With -dry-run it prints them as JSON and stores nothing; the
// output can be stored later with import.
func runExtract(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("extract", flag.ContinueOnError)
	dry := fs.Bool("dry-run", false, "print the facts instead of storing them")
	from := fs.Int("from", 0, "skip the first N lines of the transcript (read by an earlier run)")
	model := fs.String("model", extract.Model, "model the extraction runs on")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: memcue extract [-dry-run] [-from N] [-model M] TRANSCRIPT.jsonl")
	}
	path := fs.Arg(0)
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	sess, err := transcript.Read(f, *from)
	_ = f.Close()
	if err != nil {
		return err
	}
	extract.Model = *model
	origin := strings.TrimSpace(tier.Of(sess.CWD, claudeDir()) + " " + sess.Start)
	x, err := extractText(origin, sess.Text)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	facts := x.Facts
	var ids []string
	if !*dry {
		s, err := store.Open(factsPath())
		if err != nil {
			return err
		}
		defer func() { _ = s.Close() }()
		for _, fact := range facts {
			sf, err := s.Add(fact.Content, fact.Keywords, origin)
			if err != nil {
				return err
			}
			ids = append(ids, sf.ID)
		}
	}
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	return enc.Encode(map[string]any{"note": path, "origin": origin, "facts": facts, "dropped": strings.Join(x.Dropped, " "),
		"cost_usd": x.CostUSD, "tokens": x.Tokens, "chars": len(sess.Text), "lines": sess.Lines, "ids": ids})
}

func contextTimeout() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 10*time.Minute)
}

// chunks splits text at paragraph breaks into pieces of at most n bytes; a paragraph longer than n is its own piece.
func chunks(text string, n int) []string {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	var out []string
	var cur strings.Builder
	for _, p := range strings.SplitAfter(text, "\n\n") {
		if cur.Len() > 0 && cur.Len()+len(p) > n {
			out = append(out, cur.String())
			cur.Reset()
		}
		cur.WriteString(p)
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}
