package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/juev/memcue/internal/extract"
	"github.com/juev/memcue/internal/store"
	"github.com/juev/memcue/internal/tier"
	"github.com/juev/memcue/internal/transcript"
)

// runMigrate turns memory notes into facts: memcue migrate [-dry-run] [-list] [-all] [-j N] [NOTE.md...]. -all finds the
// notes of Claude Code's built-in memory itself and skips a note whose facts are already in the base, so the run can
// be repeated; a note named on the command line is migrated again, for a note that changed. With -dry-run it prints the facts as JSON lines and stores nothing.
func runMigrate(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	dry := fs.Bool("dry-run", false, "print the facts instead of storing them")
	all := fs.Bool("all", false, "migrate every note of the built-in memory")
	jobs := fs.Int("j", 4, "notes sent to the model at once")
	list := fs.Bool("list", false, "only list the notes that would be migrated")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var named, found []note
	for _, p := range fs.Args() {
		named = append(named, note{path: p, tier: noteTier(p, claudeDir())})
	}
	if *all {
		var err error
		if found, err = findNotes(claudeDir()); err != nil {
			return err
		}
	}
	if len(named)+len(found) == 0 {
		return errors.New("usage: memcue migrate [-dry-run] [-list] [-all] [-j N] [NOTE.md ...]")
	}
	var s *store.Store
	if _, err := os.Stat(factsPath()); err == nil || (!*dry && !*list) {
		if s, err = store.Open(factsPath()); err != nil {
			return err
		}
		defer func() { _ = s.Close() }()
		if found, err = notMigrated(s, found, extract.NotePromptVersion); err != nil { // a named note goes through again
			return err
		}
	}
	notes := append(named, found...)
	if *list {
		for _, n := range notes {
			if _, err := fmt.Fprintln(out, n.ref(), n.path); err != nil {
				return err
			}
		}
		return nil
	}
	if *dry {
		s = nil
	}

	type result struct {
		note
		r   extract.Result
		err error
	}
	work, done := make(chan note), make(chan result)
	for i := 0; i < max(*jobs, 1); i++ {
		go func() {
			for n := range work {
				b, err := os.ReadFile(n.path)
				var r extract.Result
				if err == nil {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
					r, err = noteFn(ctx, n.ref(), string(b))
					cancel()
				}
				done <- result{note: n, r: r, err: err}
			}
		}()
	}
	go func() {
		for _, n := range notes {
			work <- n
		}
		close(work)
	}()

	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	var failed int
	for range notes {
		res := <-done
		if res.err != nil {
			failed++
			fmt.Fprintf(os.Stderr, "memcue: %s: %v\n", res.ref(), res.err)
			continue
		}
		origin := res.origin()
		var ids []string
		if s != nil {
			for _, f := range res.r.Facts {
				sf, err := s.AddWith(f.Content, f.Keywords, origin, "", store.Meta{PromptVersion: extract.NotePromptVersion})
				if err != nil {
					return fmt.Errorf("%s: %w", res.ref(), err)
				}
				ids = append(ids, sf.ID)
			}
		}
		if err := enc.Encode(map[string]any{"note": res.ref(), "origin": origin, "facts": res.r.Facts,
			"dropped": res.r.Dropped, "cost_usd": res.r.CostUSD, "ids": ids,
			"prompt_version": extract.NotePromptVersion, "review": false}); err != nil {
			return err
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d notes failed; run again to retry them", failed, len(notes))
	}
	return nil
}

// note is a memory note and the repository it belongs to.
type note struct {
	path, tier string
}

// ref is "<tier>/<file>", the note's name inside origins.
func (n note) ref() string { return n.tier + "/" + filepath.Base(n.path) }

func (n note) origin() string {
	return strings.TrimSpace(n.tier + " " + noteDate(n.path) + " (" + n.ref() + ")")
}

// noteTier names the repository of a note given by path: the project's repository for a note of the built-in layout
// (projects/<project>/memory/<note>.md), else the note's directory.
func noteTier(path, claude string) string {
	dir := filepath.Dir(path)
	if abs, err := filepath.Abs(dir); err == nil && filepath.Base(abs) == "memory" &&
		filepath.Dir(filepath.Dir(abs)) == filepath.Join(claude, "projects") {
		return projectTier(filepath.Dir(abs), claude)
	}
	return filepath.Base(dir)
}

// findNotes lists the notes of the built-in memory: projects/<project>/memory/*.md, and memory/<tier>/*.md for a
// memory kept in one place and linked into the projects. MEMORY.md is an index, not a note. A note reached twice
// through links is listed once.
func findNotes(claude string) ([]note, error) {
	var out []note
	seen := map[string]bool{}
	add := func(dir, tierName string) error {
		paths, err := filepath.Glob(filepath.Join(dir, "*.md"))
		if err != nil {
			return err
		}
		for _, p := range paths {
			real, err := filepath.EvalSymlinks(p)
			if err != nil || seen[real] || filepath.Base(p) == "MEMORY.md" {
				continue
			}
			seen[real] = true
			out = append(out, note{path: real, tier: tierName})
		}
		return nil
	}
	shared, _ := filepath.Glob(filepath.Join(claude, "memory", "*"))
	for _, dir := range shared {
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			if err := add(dir, filepath.Base(dir)); err != nil {
				return nil, err
			}
		}
	}
	projects, _ := filepath.Glob(filepath.Join(claude, "projects", "*", "memory"))
	for _, dir := range projects {
		if err := add(dir, projectTier(filepath.Dir(dir), claude)); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// projectTier names a project directory of Claude Code by the repository its sessions ran in: the cwd of a
// transcript, or, when the transcripts are gone, the path its name encodes, if that path still exists.
func projectTier(dir, claude string) string {
	transcripts, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	for _, t := range transcripts {
		f, err := os.Open(t)
		if err != nil {
			continue
		}
		sess, err := transcript.Read(io.LimitReader(f, 1<<20), 0)
		_ = f.Close()
		if err == nil && sess.CWD != "" {
			if name := tier.Of(sess.CWD, claude); name != "" {
				return name
			}
			return filepath.Base(sess.CWD)
		}
	}
	if p := decodeProject(filepath.Base(dir)); p != "" {
		if name := tier.Of(p, claude); name != "" {
			return name
		}
	}
	return filepath.Base(dir)
}

var reNotAlnum = regexp.MustCompile(`[^A-Za-z0-9]`)

// decodeProject finds the existing directory whose path Claude Code encoded as name (every character other than a
// letter or digit becomes "-"). The encoding is ambiguous, so it walks the file system from the root.
func decodeProject(name string) string {
	var walk func(dir, rest string) string
	walk = func(dir, rest string) string {
		if rest == "" {
			return dir
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return ""
		}
		for _, e := range entries {
			enc := reNotAlnum.ReplaceAllString(e.Name(), "-")
			if !e.IsDir() || !strings.HasPrefix(rest, enc) {
				continue
			}
			after := rest[len(enc):]
			if after != "" && !strings.HasPrefix(after, "-") {
				continue
			}
			if p := walk(filepath.Join(dir, e.Name()), strings.TrimPrefix(after, "-")); p != "" {
				return p
			}
		}
		return ""
	}
	if !strings.HasPrefix(name, "-") {
		return ""
	}
	return walk(string(filepath.Separator), name[1:])
}

// notMigrated drops the notes whose facts the base already holds at the current prompt version. A note that was
// migrated with an older prompt is included again, so a better note prompt can be applied with -all. A named note is
// always migrated again, whatever this says.
func notMigrated(s *store.Store, notes []note, current int) ([]note, error) {
	rows, err := s.Rows()
	if err != nil {
		return nil, err
	}
	type migrated struct {
		seen    bool
		version int
	}
	done := map[string]migrated{}
	for _, f := range rows {
		if i := strings.LastIndex(f.Origin, " ("); i >= 0 && strings.HasSuffix(f.Origin, ")") {
			ref := f.Origin[i+2 : len(f.Origin)-1]
			m := done[ref]
			m.seen = true
			if f.PromptVersion > m.version {
				m.version = f.PromptVersion
			}
			done[ref] = m
		}
	}
	var out []note
	for _, n := range notes {
		if m := done[n.ref()]; !m.seen || m.version < current {
			out = append(out, n)
		}
	}
	return out, nil
}

// noteFn is the model step of migrate, replaced in tests.
var noteFn = extract.Note

// runImport stores the facts of `memcue migrate -dry-run` output files: memcue import FILE.json...
func runImport(args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: memcue import FILE.json [FILE.json ...]")
	}
	s, err := store.Open(factsPath())
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()
	n := 0
	for _, path := range args {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		dec := json.NewDecoder(f)
		for dec.More() {
			var rec struct {
				Note          string         `json:"note"`
				Origin        string         `json:"origin"`
				Facts         []extract.Fact `json:"facts"`
				PromptVersion int            `json:"prompt_version"`
				Review        *bool          `json:"review"`
			}
			if err := dec.Decode(&rec); err != nil {
				_ = f.Close()
				return fmt.Errorf("%s: %w", path, err)
			}
			for _, fact := range rec.Facts {
				meta := store.Meta{Kind: fact.Kind, Scope: fact.Scope, Source: fact.Source, Evidence: fact.Evidence,
					PromptVersion: rec.PromptVersion}
				// The record says whether its facts go through review. The kind test is only the fallback for files
				// written before the field existed: a session's fact has a kind, a migrated note's fact does not.
				if rec.Review != nil {
					if *rec.Review {
						meta.Review = store.Candidate
					}
				} else if fact.Kind != "" {
					meta.Review = store.Candidate
				}
				if _, err := s.AddWith(fact.Content, fact.Keywords, rec.Origin, "", meta); err != nil {
					_ = f.Close()
					return fmt.Errorf("%s: %w", rec.Note, err)
				}
				n++
			}
		}
		_ = f.Close()
	}
	_, err = fmt.Fprintf(out, "imported %d facts\n", n)
	return err
}

// noteDate is the day the note first appeared in git, or its modification day when git does not know it.
func noteDate(path string) string {
	dir := filepath.Dir(path)
	out, err := exec.Command("git", "-C", dir, "log", "--follow", "--diff-filter=A", "--format=%as", "--",
		filepath.Base(path)).Output()
	if err == nil {
		if lines := strings.Fields(string(out)); len(lines) > 0 {
			return lines[len(lines)-1]
		}
	}
	if fi, err := os.Stat(path); err == nil {
		return fi.ModTime().Format("2006-01-02")
	}
	return ""
}
