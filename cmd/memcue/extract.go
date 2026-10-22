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
	"unicode/utf8"

	"github.com/juev/memcue/internal/extract"
	"github.com/juev/memcue/internal/policy"
	"github.com/juev/memcue/internal/store"
	"github.com/juev/memcue/internal/tier"
	"github.com/juev/memcue/internal/transcript"
)

// maxChunk is the most transcript text one model call reads; a longer session goes in several calls.
const maxChunk = 200_000

// runExtract turns a session transcript into facts: memcue extract [-dry-run] [-from N] [-model M] TRANSCRIPT.jsonl.
// Facts are only added, as candidates for review, never compared with the base; the policy drops what may not be
// stored, and the output names those with the reason. With -dry-run it prints the facts as JSON and stores nothing; the output can be stored later
// with import.
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
	x, err := extractText(origin+"; "+hostLine(), sess.Text)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	var facts []extract.Fact
	var rejected []string
	for _, fact := range x.Facts {
		if ok, why := policy.Check(fact, sess.Text); !ok {
			rejected = append(rejected, why+": "+clip(fact.Content, 80))
			continue
		}
		facts = append(facts, fact)
	}
	var ids []string
	if !*dry {
		s, err := store.Open(factsPath())
		if err != nil {
			return err
		}
		defer func() { _ = s.Close() }()
		for _, fact := range facts {
			sf, err := s.AddBatch([]store.FactInput{{Content: fact.Content, Keywords: fact.Keywords, Origin: origin,
				Queries: fact.Queries, Meta: store.Meta{Kind: fact.Kind, Scope: fact.Scope, Source: fact.Source,
					Evidence: fact.Evidence, Review: store.Candidate}}})
			if err != nil {
				return err
			}
			ids = append(ids, sf[0].ID)
		}
	}
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	return enc.Encode(map[string]any{"note": path, "origin": origin, "facts": facts, "rejected": rejected,
		"dropped": strings.Join(x.Dropped, " "), "cost_usd": x.CostUSD, "tokens": x.Tokens,
		"chars": len(sess.Text), "lines": sess.Lines, "ids": ids, "review": true})
}

func contextTimeout() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 10*time.Minute)
}

// chunks splits text at paragraph breaks into pieces of at most n bytes; a paragraph longer than n is hard-split.
func chunks(text string, n int) []string {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	var out []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, p := range strings.SplitAfter(text, "\n\n") {
		if len(p) > n {
			flush()
			out = append(out, splitLong(p, n)...)
			continue
		}
		if cur.Len() > 0 && cur.Len()+len(p) > n {
			flush()
		}
		cur.WriteString(p)
	}
	flush()
	return out
}

// splitLong cuts a paragraph longer than n bytes at line breaks first, then inside a line by runes.
func splitLong(p string, n int) []string {
	var out []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, line := range strings.SplitAfter(p, "\n") {
		if len(line) > n {
			flush()
			out = append(out, splitRunes(line, n)...)
			continue
		}
		if cur.Len() > 0 && cur.Len()+len(line) > n {
			flush()
		}
		cur.WriteString(line)
	}
	flush()
	return out
}

// splitRunes cuts s into pieces of at most n bytes without splitting a UTF-8 rune. A piece can exceed n only when
// n is smaller than the rune itself; n is 200_000 in practice.
func splitRunes(s string, n int) []string {
	if n <= 0 {
		return []string{s}
	}
	var out []string
	for len(s) > n {
		cut := n
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		if cut == 0 {
			_, cut = utf8.DecodeRuneInString(s)
		}
		out = append(out, s[:cut])
		s = s[cut:]
	}
	if s != "" {
		out = append(out, s)
	}
	return out
}
