package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/juev/memcue/internal/store"
	"github.com/juev/memcue/internal/text"
	"github.com/juev/memcue/internal/tier"
)

func factsPath() string {
	if p := os.Getenv("MEMCUE_FACTS"); p != "" {
		return p
	}
	return filepath.Join(claudeDir(), "memory", "facts.db")
}

func splitKeywords(s string) []string {
	var out []string
	for _, k := range strings.Split(s, ",") {
		if k = strings.TrimSpace(k); k != "" {
			out = append(out, k)
		}
	}
	return out
}

func withStore(fn func(*store.Store) error) error {
	s, err := store.Open(factsPath())
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()
	return fn(s)
}

func runAdd(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	keywords := fs.String("keywords", "", "comma-separated words someone would ask with")
	origin := fs.String("origin", "", "where it was learnt: repository and date (default: those of the working directory)")
	expires := fs.String("expires", "", "last day the fact is worth recalling, YYYY-MM-DD (default: never)")
	candidate := fs.Bool("candidate", false, "store it for review: not recalled until approved")
	source := fs.String("source", "user", "who said it: user, user_confirmed, tool, agent, note, memory")
	kind := fs.String("kind", "", "rule, decision, pitfall or environment")
	scope := fs.String("scope", "", "where it holds: global, host:<name>, repo:<name>, service:<name>")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !sources[*source] {
		return fmt.Errorf("add: unknown -source %q", *source)
	}
	if *origin == "" {
		*origin = defaultOrigin()
	}
	m := store.Meta{Kind: *kind, Scope: *scope, Source: *source}
	if *candidate {
		m.Review = store.Candidate
	}
	return withStore(func(s *store.Store) error {
		f, err := s.AddWith(strings.Join(fs.Args(), " "), splitKeywords(*keywords), *origin, *expires, m)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, f.ID)
		return err
	})
}

var sources = map[string]bool{"user": true, "user_confirmed": true, "tool": true, "agent": true, "note": true,
	"memory": true}

// runCandidates lists the facts that wait for approval: memcue candidates [-json].
func runCandidates(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("candidates", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print a JSON array")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return withStore(func(s *store.Store) error {
		cs, err := s.Candidates()
		if err != nil {
			return err
		}
		if *asJSON {
			return writeFactsJSON(out, cs)
		}
		for _, f := range cs {
			if _, err := fmt.Fprintf(out, "%s %s kind=%s scope=%s source=%s | %s\n", f.ID, f.Origin, f.Kind, f.Scope,
				f.Source, strings.Join(strings.Fields(f.Content), " ")); err != nil {
				return err
			}
		}
		return nil
	})
}

// runApprove takes candidates into recall: memcue approve ID...
func runApprove(args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: memcue approve ID [ID ...]")
	}
	return withStore(func(s *store.Store) error {
		for _, id := range args {
			f, err := s.Approve(id)
			if err != nil {
				return err
			}
			if _, err := fmt.Fprintf(out, "%s v%d\n", f.ID, f.Version); err != nil {
				return err
			}
		}
		return nil
	})
}

// defaultOrigin names the repository of the working directory and today, the way extracted facts are named.
func defaultOrigin() string {
	cwd, err := os.Getwd()
	if err != nil {
		return time.Now().Format(time.DateOnly)
	}
	return strings.TrimSpace(tier.Of(cwd, claudeDir()) + " " + time.Now().Format(time.DateOnly))
}

func runCorrect(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("correct", flag.ContinueOnError)
	keywords := fs.String("keywords", "", "comma-separated; empty keeps the current ones")
	expires := fs.String("expires", "", "new last day the fact is worth recalling, YYYY-MM-DD; empty keeps the current one")
	noExpires := fs.Bool("no-expires", false, "drop the expiry: the fact is recalled from now on without end")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 2 {
		return errors.New("usage: memcue correct [-keywords K] [-expires YYYY-MM-DD|-no-expires] ID TEXT")
	}
	if *noExpires && *expires != "" {
		return errors.New("correct: -expires and -no-expires cannot be combined")
	}
	return withStore(func(s *store.Store) error {
		kw := splitKeywords(*keywords)
		if kw == nil {
			cur, err := s.Get(fs.Arg(0))
			if err != nil {
				return err
			}
			kw = cur.Keywords
		}
		var exp *string
		switch {
		case *expires != "":
			exp = expires
		case *noExpires:
			empty := ""
			exp = &empty
		}
		f, err := s.CorrectExpiring(fs.Arg(0), strings.Join(fs.Args()[1:], " "), kw, exp)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "%s v%d\n", f.ID, f.Version)
		return err
	})
}

func runRetire(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("retire", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: memcue retire ID")
	}
	return withStore(func(s *store.Store) error {
		f, err := s.Retire(fs.Arg(0))
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "%s v%d retired\n", f.ID, f.Version)
		return err
	})
}

func runFacts(out io.Writer) error {
	return withStore(func(s *store.Store) error {
		fs, err := s.Active()
		if err != nil {
			return err
		}
		for _, f := range fs {
			expires := ""
			if f.Expires != "" {
				expires = " expires=" + f.Expires
			}
			if _, err := fmt.Fprintf(out, "%s v%d %s [%s]%s %s\n", f.ID, f.Version, f.Origin,
				strings.Join(f.Keywords, ", "), expires, strings.Join(strings.Fields(f.Content), " ")); err != nil {
				return err
			}
		}
		return nil
	})
}

// runSearch lists the facts holding any word of the text, best first, with their IDs: the way to find the fact to
// correct or retire. Unlike recall it has no threshold.
func runSearch(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	n := fs.Int("n", 10, "how many facts")
	asJSON := fs.Bool("json", false, "print a JSON array of facts instead of one line each")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return withStore(func(s *store.Store) error {
		hits, err := s.Search(text.Terms(strings.Join(fs.Args(), " ")), *n)
		if err != nil {
			return err
		}
		if *asJSON {
			return json.NewEncoder(out).Encode(searchJSON(hits))
		}
		for _, h := range hits {
			if _, err := fmt.Fprintln(out, strings.Join(strings.Fields(h.ID+" "+h.Origin+" | "+h.Content), " ")); err != nil {
				return err
			}
		}
		return nil
	})
}

// searchFact is one fact as search -json prints it: what a caller needs to show, correct or retire it.
type searchFact struct {
	ID       string   `json:"id"`
	Content  string   `json:"content"`
	Keywords []string `json:"keywords,omitempty"`
	Origin   string   `json:"origin,omitempty"`
	Created  string   `json:"created"`
	Expires  string   `json:"expires,omitempty"`
}

func searchJSON(hits []store.Hit) []searchFact {
	out := make([]searchFact, 0, len(hits))
	for _, h := range hits {
		out = append(out, searchFact{ID: h.ID, Content: h.Content, Keywords: h.Keywords, Origin: h.Origin,
			Created: h.Created.UTC().Format(time.RFC3339), Expires: h.Expires})
	}
	return out
}

// candidateFact is one candidate as candidates -json prints it: what a review needs to judge it.
type candidateFact struct {
	searchFact
	Kind     string `json:"kind,omitempty"`
	Scope    string `json:"scope,omitempty"`
	Source   string `json:"source,omitempty"`
	Evidence string `json:"evidence,omitempty"`
	Author   string `json:"author,omitempty"`
}

func writeFactsJSON(out io.Writer, fs []store.Fact) error {
	list := make([]candidateFact, 0, len(fs))
	for _, f := range fs {
		list = append(list, candidateFact{searchFact: searchFact{ID: f.ID, Content: f.Content, Keywords: f.Keywords,
			Origin: f.Origin, Created: f.Created.UTC().Format(time.RFC3339), Expires: f.Expires}, Kind: f.Kind,
			Scope: f.Scope, Source: f.Source, Evidence: f.Evidence, Author: f.Author})
	}
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	return enc.Encode(list)
}

// runMerge is git's merge driver: memcue merge %O %A %B. The result goes into %A; a non-zero exit tells git the
// merge failed and leaves the conflict to the user.
func runMerge(args []string, out io.Writer) error {
	if len(args) != 3 {
		return errors.New("usage: memcue merge BASE OURS THEIRS")
	}
	s, err := store.Open(args[1])
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()
	st, err := s.Merge(args[2])
	if err != nil {
		return err
	}
	if st.Diverged > 0 {
		_, err = fmt.Fprintf(out, "memcue: %d facts changed on both sides; the later change is current, both stay in the history\n", st.Diverged)
	}
	return err
}

// runDump is git's textconv: memcue dump FILE.
func runDump(args []string, out io.Writer) error {
	if len(args) != 1 {
		return errors.New("usage: memcue dump FILE")
	}
	s, err := store.Open(args[0])
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()
	return s.Dump(out)
}

// runInstall prepares the git repository that holds the fact base (by default the one around it, usually the
// Claude config directory) for syncing it between machines: it registers the merge driver and the textconv in the
// repository's git config and routes the base to them in .gitattributes. A base outside git needs nothing.
func runInstall(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	repo := fs.String("repo", "", "git repository holding the fact base (default: the one around it)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	base, err := filepath.Abs(factsPath())
	if err != nil {
		return err
	}
	if *repo == "" {
		b, err := exec.Command("git", "-C", existingDir(filepath.Dir(base)), "rev-parse", "--show-toplevel").Output()
		if err != nil {
			_, err = fmt.Fprintf(out, "%s is not in a git repository: nothing to set up for sync\n", base)
			return err
		}
		*repo = strings.TrimSpace(string(b))
	}
	top, err := filepath.EvalSymlinks(*repo)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(top, resolveDir(base))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("fact base %s is not inside %s", base, top)
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return err
	}
	q := shellQuote(exe)
	for _, kv := range [][2]string{
		{"merge.memcue.name", "memcue fact base"},
		{"merge.memcue.driver", q + " merge %O %A %B"},
		{"diff.memcue.textconv", q + " dump"},
	} {
		if b, err := exec.Command("git", "-C", top, "config", kv[0], kv[1]).CombinedOutput(); err != nil {
			return fmt.Errorf("git config %s: %v: %s", kv[0], err, b)
		}
	}
	line := filepath.ToSlash(rel) + " merge=memcue diff=memcue"
	added, err := ensureLine(filepath.Join(top, ".gitattributes"), line)
	if err != nil {
		return err
	}
	msg := fmt.Sprintf("merge driver registered in %s\n", top)
	if added {
		msg += fmt.Sprintf("added to .gitattributes: %s (commit it)\n", line)
	}
	_, err = fmt.Fprint(out, msg)
	return err
}

// existingDir is dir or its nearest existing parent: the base may not exist yet.
func existingDir(dir string) string {
	for {
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return dir
		}
		dir = parent
	}
}

// resolveDir resolves symlinks in the directory part of path, which exists; the file itself may not.
func resolveDir(path string) string {
	d := existingDir(filepath.Dir(path))
	r, err := filepath.EvalSymlinks(d)
	if err != nil {
		return path
	}
	rest, err := filepath.Rel(d, path)
	if err != nil {
		return path
	}
	return filepath.Join(r, rest)
}

// ensureLine appends line to the file unless a line equal to it is there; it reports whether it appended.
func ensureLine(path, line string) (bool, error) {
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	for _, l := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(l) == line {
			return false, nil
		}
	}
	if len(b) > 0 && b[len(b)-1] != '\n' {
		line = "\n" + line
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return false, err
	}
	_, err = f.WriteString(line + "\n")
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err == nil, err
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
