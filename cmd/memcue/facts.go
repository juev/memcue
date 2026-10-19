package main

import (
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
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *origin == "" {
		*origin = defaultOrigin()
	}
	return withStore(func(s *store.Store) error {
		f, err := s.Add(strings.Join(fs.Args(), " "), splitKeywords(*keywords), *origin)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, f.ID)
		return err
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
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 2 {
		return errors.New("usage: memcue correct [-keywords K] ID TEXT")
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
		f, err := s.Correct(fs.Arg(0), strings.Join(fs.Args()[1:], " "), kw)
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
			if _, err := fmt.Fprintf(out, "%s v%d %s [%s] %s\n", f.ID, f.Version, f.Origin,
				strings.Join(f.Keywords, ", "), strings.Join(strings.Fields(f.Content), " ")); err != nil {
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
	if err := fs.Parse(args); err != nil {
		return err
	}
	return withStore(func(s *store.Store) error {
		hits, err := s.Search(text.Terms(strings.Join(fs.Args(), " ")), *n)
		if err != nil {
			return err
		}
		for _, h := range hits {
			if _, err := fmt.Fprintln(out, strings.Join(strings.Fields(h.ID+" "+h.Origin+" | "+h.Content), " ")); err != nil {
				return err
			}
		}
		return nil
	})
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
