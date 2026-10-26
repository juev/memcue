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
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/juev/memcue/internal/extract"
	"github.com/juev/memcue/internal/policy"
	"github.com/juev/memcue/internal/recall"
	"github.com/juev/memcue/internal/store"
	"github.com/juev/memcue/internal/text"
	"github.com/juev/memcue/internal/tier"
	"github.com/juev/memcue/internal/vet"
)

func factsPath() string {
	if p := os.Getenv("MEMCUE_FACTS"); p != "" {
		return expandHome(p)
	}
	return filepath.Join(claudeDir(), "memory", "facts.db")
}

// expandHome resolves a leading ~/ or $HOME/ that a shell would have expanded but an env set by a launcher or a YAML
// profile does not. A bare ~ is the home directory. Anything else is returned as it is; a path that still starts with
// ~ is a configuration mistake the caller reports rather than a directory named "~" in the working directory.
func expandHome(p string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	switch {
	case p == "~":
		return home
	case strings.HasPrefix(p, "~/"):
		return filepath.Join(home, p[len("~/"):])
	case strings.HasPrefix(p, "$HOME/"):
		return filepath.Join(home, p[len("$HOME/"):])
	}
	return p
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

var (
	// kinds are the fact kinds add writes; the card uses rule and decision, an event expires (#287). repo and other,
	// labels of the extraction era, are searched but not written: a scope says the first, no kind the second (#505).
	kinds = map[string]bool{"rule": true, "decision": true, "pitfall": true, "environment": true, "procedure": true,
		"event": true}
	// reScope is where a fact holds: global, a named host, repository or service, a directory, a shelf, or bare repo
	// for the project of the working directory.
	reScope = regexp.MustCompile(`^(global|repo|host:[^ ]+|repo:[^ ]+|service:[^ ]+|dir:/.+|shelf:` + shelfSlug + `)$`)
	// reShelf is the name of a shelf (#433), the same shape for every one, so a shelf split later needs no new code.
	reShelf = regexp.MustCompile(`^` + shelfSlug + `$`)
)

// shelfSlug is the shape of a shelf's name, the part after "shelf:".
const shelfSlug = `[a-z0-9][a-z0-9-]{0,63}`

// validShelf checks the name of a shelf a caller passes with -shelf.
func validShelf(cmd, slug string) error {
	if !reShelf.MatchString(slug) {
		return fmt.Errorf("%s: -shelf %q must match %s", cmd, slug, reShelf)
	}
	return nil
}

// validKindScope checks the values a caller may pass to add. An empty value means "not stated" and is allowed.
func validKindScope(kind, scope string) error {
	if kind != "" && !kinds[kind] {
		return fmt.Errorf("add: unknown -kind %q; use rule, decision, pitfall, environment, procedure or event", kind)
	}
	if scope != "" && !reScope.MatchString(scope) {
		if recall.IsShelf(scope) {
			return fmt.Errorf("add: -scope %q: a shelf is shelf:<slug>, the slug matching %s", scope, reShelf)
		}
		return fmt.Errorf("add: -scope %q must be global, repo, host:<name>, repo:<key>, service:<name>, dir:<path> "+
			"or shelf:<slug>", scope)
	}
	return nil
}

// hasSecret says whether a fact's text or keywords looks like a credential: the agent's, a user request or a
// migrated note may quote one.
func hasSecret(content string, keywords []string) bool {
	return policy.Secret(content) || policy.Secret(strings.Join(keywords, " "))
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
	origin := fs.String("origin", "", "where it was learnt: repository and date (default: those of -cwd or the working directory, empty outside a repository)")
	cwd := fs.String("cwd", "", "working directory for the default origin (default: the process working directory; an empty value is no project)")
	expires := fs.String("expires", "", "last day the fact is worth recalling, YYYY-MM-DD (default: never)")
	candidate := fs.Bool("candidate", false, "store it for review: not recalled until approved")
	source := fs.String("source", "user", "who said it: user, user_confirmed, tool, agent, note, memory")
	kind := fs.String("kind", "", "rule, decision, pitfall, environment, procedure or event (an event expires, see MEMCUE_EVENT_TTL_DAYS)")
	scope := fs.String("scope", "", "where it holds: global, repo (the working directory's project), host:<name>, repo:<key>, service:<name>, dir:<path>, shelf:<slug> (read only by a search that names it)")
	shelf := fs.String("shelf", "", "put it on this shelf: the same as -scope shelf:<slug>")
	force := fs.Bool("force", false, "store it even when it looks like a secret")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !sources[*source] {
		return fmt.Errorf("add: unknown -source %q", *source)
	}
	if *shelf != "" {
		if *scope != "" {
			return errors.New("add: -shelf and -scope both name where the fact holds; pass one of them")
		}
		if err := validShelf("add", *shelf); err != nil {
			return err
		}
		*scope = recall.ShelfPrefix + *shelf
	}
	if err := validKindScope(*kind, *scope); err != nil {
		return err
	}
	content, keywordsList := strings.Join(fs.Args(), " "), splitKeywords(*keywords)
	// The agent writes its own facts (#494): what it saw (tool) or concluded (agent) is live at once and meets no other
	// check, so the state rule runs here (#475). A fact in the user's words keeps them.
	if *source == "agent" || *source == "tool" {
		if part := vet.State(store.Fact{Kind: *kind, Source: *source, Content: content}); part != "" {
			return fmt.Errorf("add: the fact records installation state (%s), which a command reads; store what does not change with the next release or restart", part)
		}
	}
	if !*force && hasSecret(content, keywordsList) {
		return errors.New("add: the fact looks like a secret; use a password manager, or pass -force to store it anyway")
	}
	if *origin == "" && !emptyFlag(fs, "cwd") {
		*origin = defaultOriginAt(*cwd)
	}
	if *scope == "repo" {
		if *scope = repoScope(*scope, *origin); *scope == "" {
			return errors.New("add: -scope repo needs a project: run it in one, or pass -cwd or -origin")
		}
	}
	m := store.Meta{Kind: *kind, Scope: defaultScope(*scope, *origin), Source: *source}
	if *candidate {
		m.Review = store.Candidate
	}
	return withStore(func(s *store.Store) error {
		f, err := s.AddWith(content, keywordsList, *origin, eventExpires(*kind, *expires), m)
		if err != nil {
			return err
		}
		if !*candidate && extract.API.URL != "" {
			f = expandStored(s, f)
		}
		_, err = fmt.Fprintln(out, f.ID)
		return err
	})
}

// expandOneTimeout bounds one expansion call of add and correct. The fact is stored before the call, so the command has
// to finish inside the plugins' own timeouts for it (DSH 10 s, Hermes 15 s): a plugin that kills a slow add reports the
// fact as not stored, and the model writes it again. One fact without reasoning takes 1-2 s.
const expandOneTimeout = 5 * time.Second

// expandOneFn is the expansion call of add and correct, replaced in tests.
var expandOneFn = func(input string) (extract.ExpandResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), expandOneTimeout)
	defer cancel()
	return extract.ExpandFacts(ctx, input)
}

// expandStored gives a fact add or correct just stored its search queries, so it is found by the words a user asks with
// from the first prompt (#288, #297), and returns the fact as it stands afterwards. Only with an API endpoint:
// claude -p takes seconds to start. A candidate is left alone, since the Hermes mirror adds them on the turn's path,
// and the dream expands it once approved. Any failure leaves the fact without queries for the dream to expand; the fact
// is stored either way.
func expandStored(s *store.Store, f store.Fact) store.Fact {
	input, err := renderSplitInput([]store.Fact{f})
	if err != nil {
		return f
	}
	r, err := expandOneFn(input)
	if err != nil {
		return f
	}
	for _, e := range r.Facts {
		if e.ID == f.ID && len(e.Queries) > 0 {
			nf, err := s.SetQueries(f.ID, f.Version, e.Queries)
			if err != nil {
				return f
			}
			return nf
		}
	}
	return f
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

// defaultOriginAt names the repository of dir and today, the way every fact of a session is named. An empty dir means
// the process working directory; a caller that knows the session workspace passes it explicitly.
func defaultOriginAt(dir string) string {
	if dir == "" {
		var err error
		dir, err = os.Getwd()
		if err != nil {
			return ""
		}
	}
	return originAt(dir, time.Now().Format(time.DateOnly))
}

// emptyFlag reports whether the flag name was passed with an empty value. A host that knows its session has no project
// says so with -cwd "" (#347): an unset -cwd still means the process's working directory, as on the command line.
func emptyFlag(fs *flag.FlagSet, name string) bool {
	empty := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name && f.Value.String() == "" {
			empty = true
		}
	})
	return empty
}

// originAt names a fact by where it was learnt: the project key (tier.Of: the remote, the go.mod module, or the
// directory) and the date. In a temporary directory the origin stays empty: a scratch directory is a wrong answer, not
// a weak one (#287, #343). A bare date would be just as wrong: it matches no project.
func originAt(dir, date string) string {
	if dir == "" {
		return ""
	}
	name := tier.Of(dir, claudeDir())
	if name == "" {
		return ""
	}
	return strings.TrimSpace(name + " " + date)
}

// repoScope turns the scope a model or a caller chose into the one stored: a repo scope, bare or named, becomes the
// scope of the project the origin names (tier.Scope of its key), so a repository is never named by the model (#343).
// Outside a project, which leaves the origin empty, a repo scope has nothing to hold in and is dropped. Every other
// scope is kept as it is.
func repoScope(scope, origin string) string {
	if scope != "repo" && !strings.HasPrefix(strings.ToLower(scope), "repo:") {
		return scope
	}
	return tier.Scope(originKey(origin))
}

// defaultScope is the scope a fact gets when nobody named one (#442): the project its origin names (tier.Scope of the
// key, global for the global tier), or global when the origin names none. An empty scope is in no place a search
// asks for, so a fact written without one would be found only by -in all.
func defaultScope(scope, origin string) string {
	if strings.TrimSpace(scope) != "" {
		return scope
	}
	if key := originTier(origin); key != "" {
		return tier.Scope(key)
	}
	return "global"
}

// originTier is the project key an origin starts with, "<key> <date>" with anything after the date, such as the
// "(file.md)" a migrated memory note keeps; "" when the second word is not a date.
func originTier(origin string) string {
	fields := strings.Fields(origin)
	if len(fields) < 2 {
		return ""
	}
	if _, err := time.Parse(time.DateOnly, fields[1]); err != nil {
		return ""
	}
	return fields[0]
}

// originKey is the project key at the head of an origin "<key> <date>"; "" when the origin does not end in a date.
func originKey(origin string) string {
	origin = strings.TrimSpace(origin)
	i := strings.LastIndexByte(origin, ' ')
	if i < 0 {
		return ""
	}
	if _, err := time.Parse(time.DateOnly, origin[i+1:]); err != nil {
		return ""
	}
	return strings.TrimSpace(origin[:i])
}

// projectScopes are the scopes of the project of dir, one per key, for the session card: a rule or decision written
// on a mirror's other host or in a fork's upstream belongs to the session too. The global tier has none.
func projectScopes(dir string) []string {
	var out []string
	for _, k := range tier.Keys(dir, claudeDir()) {
		if k != tier.Global {
			out = append(out, tier.Scope(k))
		}
	}
	return out
}

// defaultEventTTL is how long a fact that records what happened stays recalled. Knowledge outlives the run that
// produced it; an event fact without an expiry keeps answering questions about that run for months, which is exactly
// what the base did before (#287).
const defaultEventTTL = 7

// eventExpires gives an event fact a short life unless the caller set one, and MEMCUE_EVENT_TTL_DAYS says how long;
// 0 keeps event facts without an expiry. A rule, a decision or a pitfall has no default expiry: it is knowledge.
func eventExpires(kind, expires string) string {
	if expires != "" || kind != "event" {
		return expires
	}
	days := defaultEventTTL
	if v := strings.TrimSpace(os.Getenv("MEMCUE_EVENT_TTL_DAYS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			days = n
		}
	}
	if days == 0 {
		return ""
	}
	return time.Now().AddDate(0, 0, days).Format(time.DateOnly)
}

func runCorrect(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("correct", flag.ContinueOnError)
	keywords := fs.String("keywords", "", "comma-separated; empty keeps the current ones")
	expires := fs.String("expires", "", "new last day the fact is worth recalling, YYYY-MM-DD; empty keeps the current one")
	noExpires := fs.Bool("no-expires", false, "drop the expiry: the fact is recalled from now on without end")
	force := fs.Bool("force", false, "store it even when it looks like a secret")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 2 {
		return errors.New("usage: memcue correct [-keywords K] [-expires YYYY-MM-DD|-no-expires] [-force] ID TEXT")
	}
	if *noExpires && *expires != "" {
		return errors.New("correct: -expires and -no-expires cannot be combined")
	}
	content := strings.Join(fs.Args()[1:], " ")
	return withStore(func(s *store.Store) error {
		kw := splitKeywords(*keywords)
		if kw == nil {
			cur, err := s.Get(fs.Arg(0))
			if err != nil {
				return err
			}
			kw = cur.Keywords
		}
		if !*force && hasSecret(content, kw) {
			return errors.New("correct: the fact looks like a secret; use a password manager, or pass -force to store it anyway")
		}
		var exp *string
		switch {
		case *expires != "":
			exp = expires
		case *noExpires:
			empty := ""
			exp = &empty
		}
		f, err := s.CorrectExpiring(fs.Arg(0), content, kw, exp)
		if err != nil {
			return err
		}
		// The text changed, so the queries have to be asked for again: the words the fact was found by no longer
		// describe it (#297). A candidate is left to the dream, as add leaves it.
		if f.Review == "" && extract.API.URL != "" {
			f = expandStored(s, f)
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

func runReopen(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("reopen", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: memcue reopen ID")
	}
	return withStore(func(s *store.Store) error {
		f, err := s.Restore(fs.Arg(0))
		if err != nil {
			return err
		}
		state := "active"
		if f.Review != "" {
			state = f.Review
		}
		_, err = fmt.Fprintf(out, "%s v%d %s\n", f.ID, f.Version, state)
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

// reindexReport is what reindex prints: the index against the live facts before and, unless -dry-run, after the
// rebuild. Unnumbered is how many live facts whose text gives a numeric term have an index row without one, the
// rows a rebuild would fix (#383).
type reindexReport struct {
	DryRun           bool `json:"dry_run"`
	Live             int  `json:"live"`
	RowsBefore       int  `json:"rows_before"`
	RowsAfter        *int `json:"rows_after,omitempty"`
	NumberedBefore   int  `json:"numbered_before"`
	NumberedAfter    *int `json:"numbered_after,omitempty"`
	UnnumberedBefore int  `json:"unnumbered_before"`
	UnnumberedAfter  *int `json:"unnumbered_after,omitempty"`
}

// runReindex rebuilds the index from the live facts and says what changed. The counts before are read without
// Open, which repairs an index it finds incomplete: through it the report would show the repaired index, and
// -dry-run would rebuild.
func runReindex(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("reindex", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "print the counts and what a rebuild would fix, rebuilding nothing")
	asJSON := fs.Bool("json", false, "print one JSON object instead of lines")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ro, err := store.OpenReadOnly(factsPath())
	if err != nil {
		return err
	}
	before, err := ro.IndexStats()
	if cerr := ro.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	r := reindexReport{DryRun: *dryRun, Live: before.Live, RowsBefore: before.Rows, NumberedBefore: before.Numbered,
		UnnumberedBefore: before.Unnumbered}
	if !*dryRun {
		err := withStore(func(s *store.Store) error {
			if err := s.Rebuild(); err != nil {
				return err
			}
			after, err := s.IndexStats()
			if err != nil {
				return err
			}
			r.Live, r.RowsAfter, r.NumberedAfter, r.UnnumberedAfter = after.Live, &after.Rows, &after.Numbered,
				&after.Unnumbered
			return nil
		})
		if err != nil {
			return err
		}
	}
	if *asJSON {
		return json.NewEncoder(out).Encode(r)
	}
	if *dryRun {
		_, err = fmt.Fprintf(out, "live facts: %d\nindex rows: %d\nrows with a number term: %d\n"+
			"live facts with a number but no number term: %d\n", r.Live, r.RowsBefore, r.NumberedBefore,
			r.UnnumberedBefore)
		return err
	}
	_, err = fmt.Fprintf(out, "live facts: %d\nindex rows: %d -> %d\nrows with a number term: %d -> %d\n",
		r.Live, r.RowsBefore, *r.RowsAfter, r.NumberedBefore, *r.NumberedAfter)
	return err
}

// parseAnywhere parses the flags of fs wherever they stand among args and returns the other arguments in order. The
// flag package stops at the first word, and agents write `search "words" -in all` (#481): the flags became words of
// the query without an error. An argument after "--" is a word even if it starts with a dash; an unknown flag fails
// as before.
func parseAnywhere(fs *flag.FlagSet, args []string) ([]string, error) {
	var flags, words []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			words = append(words, args[i+1:]...)
			break
		}
		if len(a) < 2 || a[0] != '-' {
			words = append(words, a)
			continue
		}
		flags = append(flags, a)
		name := strings.TrimLeft(a, "-")
		if strings.Contains(name, "=") {
			continue
		}
		// A flag that takes a value takes the next argument with it, the way fs.Parse reads it.
		if f := fs.Lookup(name); f != nil && !isBoolFlag(f) && i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	if err := fs.Parse(flags); err != nil {
		return nil, err
	}
	return words, nil
}

func isBoolFlag(f *flag.Flag) bool {
	b, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && b.IsBoolFlag()
}

// runSearch lists the facts that fit the text, best first, with their IDs: how the agent reads its memory (#361), and
// the way to find the fact to correct or retire. -n is the most it returns; a fact far below the best one does not fit
// and is left out (#407), so one fitting fact comes alone and none says so.
func runSearch(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	// Five is the measured point (#372, confirmed on a live base): ten facts halve the useful share and double the
	// noise. Every surface hands over five unless the caller asks for more (#384).
	n := fs.Int("n", 5, "the most facts to return; fewer come when the rest do not fit")
	minScore := fs.Float64("min-score", recall.MinScore,
		"leave out a fact whose BM25 is below this share of the best one (0 keeps every fact up to -n)")
	in := fs.String("in", "", "where to look, comma-separated: project (this repository), global, host (this machine), "+
		"all (every fact off the shelves), shelf (every shelf), or a scope such as repo:KEY, host:NAME, service:NAME, "+
		"shelf:SLUG (default: project, host and global in a repository, all elsewhere)")
	kind := fs.String("kind", "", "only facts of these kinds, comma-separated: "+strings.Join(recall.Kinds, ", "))
	since := fs.String("since", "", "only facts learnt on or after this day (YYYY-MM-DD)")
	expired := fs.Bool("expired", false, "include facts whose expiry has passed")
	allWords := fs.Bool("all-words", false, "a fact must hold every word of the query, not any of them")
	asJSON := fs.Bool("json", false, `print one JSON object, {"facts": [...], "hints": [...]}, instead of lines`)
	cwd := fs.String("cwd", ".", "session working directory: its repository is the project, and its facts win ties")
	session := fs.String("session", "", "session id for the search log")
	words, err := parseAnywhere(fs, args)
	if err != nil {
		return err
	}
	if *n < 1 {
		// A limit below one asks for nothing, and the empty answer it gets would say the base holds nothing (#454).
		return errors.New("search: -n must be >= 1")
	}
	q := recall.Query{Text: strings.Join(words, " "), N: *n, MinScore: *minScore,
		Project: tier.Keys(*cwd, claudeDir()), Host: currentHost(), In: splitKeywords(*in), Kinds: splitKeywords(*kind), Since: *since,
		Expired: *expired, AllWords: *allWords}
	f, err := q.Filter()
	if err != nil {
		return err
	}
	return withStore(func(s *store.Store) error {
		// The stored keys are two scans of the base, a quarter of a search's time: read them only for a place that
		// names a repository, the one ResolveRepos changes.
		if q.NamesRepo() {
			known, err := s.RepoScopes()
			if err != nil {
				return err
			}
			tiers, err := s.OriginTiers()
			if err != nil {
				return err
			}
			if q, err = q.ResolveRepos(known, tiers); err != nil {
				return err
			}
		}
		start := time.Now()
		hits, err := recall.Search(s, q)
		if err != nil {
			return err
		}
		query := q.Text
		logSearch(*session, *cwd, query, hits, time.Since(start))
		// The lines that follow the facts, in the order the plain answer prints them; -json carries the same ones, so
		// a host that parses the answer is told what a reader is (#453).
		var hints []string
		if len(hits) == 0 {
			if q.NarrowByDefault() {
				hints = append(hints, "No facts of this repository, this host or global match; -in all searches every repository.")
			}
			if len(f.ExcludePrefixes) > 0 {
				hint, err := shelfHint(s, q)
				if err != nil {
					return err
				}
				if hint != "" {
					hints = append(hints, hint)
				}
			}
		}
		if text.HasCJK(q.Text) {
			// Found or not: a CJK query that finds facts matched only on its Latin names (#410).
			hints = append(hints, cjkHint)
		}
		if *asJSON {
			return json.NewEncoder(out).Encode(searchAnswer{Facts: searchJSON(hits), Hints: hints})
		}
		if len(hits) == 0 && !q.NarrowByDefault() {
			if _, err := fmt.Fprintln(out, "No stored facts match."); err != nil {
				return err
			}
		}
		for _, h := range hits {
			if _, err := fmt.Fprintln(out, strings.Join(strings.Fields(h.ID+" "+h.Origin+" | "+h.Content), " ")); err != nil {
				return err
			}
		}
		for _, h := range hints {
			if _, err := fmt.Fprintln(out, h); err != nil {
				return err
			}
		}
		return nil
	})
}

// shelfHints is the most shelved facts the hint of an empty search counts, and shelfNames the most shelves it names.
const (
	shelfHints = 10
	shelfNames = 3
)

// shelfHint follows an empty search that did not read the shelves: when the same words find facts on a shelf, it says
// how many and which -in reads them (#433), so a fact put away is not a fact lost. Empty when the shelves hold none.
func shelfHint(s *store.Store, q recall.Query) (string, error) {
	q.In, q.N = []string{"shelf"}, shelfHints
	hits, err := recall.Search(s, q)
	if err != nil || len(hits) == 0 {
		return "", err
	}
	var names []string
	for _, h := range hits {
		if name := strings.ToLower(h.Scope); !slices.Contains(names, name) && len(names) < shelfNames {
			names = append(names, name)
		}
	}
	verb := "facts on the shelf match"
	if len(hits) == 1 {
		verb = "fact on the shelf matches"
	}
	return fmt.Sprintf("%d %s: -in %s", len(hits), verb, strings.Join(names, ",")), nil
}

// cjkHint follows the answer to a query in Chinese, Japanese or Korean: the facts are in Russian and English, and a
// question translated into either finds its fact 54-60 times in 60, against 0-50 asked as it is (#410).
const cjkHint = "The facts are in Russian and English: search again with the query translated into one of them."

// searchRecord is one line of search.log: a query the agent wrote and the facts it got, the record the read side is
// judged by since the agent searches itself (#361).
type searchRecord struct {
	Time    string   `json:"time"`
	Session string   `json:"session,omitempty"`
	Tier    string   `json:"tier,omitempty"`
	Host    string   `json:"host,omitempty"`
	Query   string   `json:"query"`
	Facts   []string `json:"facts,omitempty"`
	Ms      float64  `json:"ms"`
}

// logQuery is how much of a query search.log keeps: an agent's query is a few words, a longer one is a pasted text.
const logQuery = 500

// logSearch appends a search to search.log in the state directory, searches that found nothing included. It keeps
// the text of queries, so it is off unless MEMCUE_SEARCH_LOG=1. A failure to log never fails the search.
func logSearch(session, cwd, query string, hits []store.Hit, took time.Duration) {
	if os.Getenv("MEMCUE_SEARCH_LOG") != "1" {
		return
	}
	dir, err := stateDir()
	if err != nil {
		return
	}
	rec := searchRecord{Time: time.Now().UTC().Format(time.RFC3339), Session: session, Tier: tier.Of(cwd, claudeDir()),
		Host: currentHost(), Query: clip(query, logQuery), Ms: float64(took.Microseconds()) / 1000}
	for _, h := range hits {
		rec.Facts = append(rec.Facts, h.ID)
	}
	_ = appendJSON(filepath.Join(dir, "search.log"), rec)
}

// searchFact is one fact as search -json prints it: what a caller needs to show, correct or retire it.
type searchFact struct {
	ID       string   `json:"id"`
	Content  string   `json:"content"`
	Keywords []string `json:"keywords,omitempty"`
	Origin   string   `json:"origin,omitempty"`
	Scope    string   `json:"scope,omitempty"`
	Created  string   `json:"created"`
	Expires  string   `json:"expires,omitempty"`
}

// searchAnswer is what search -json prints: the facts, and the lines the plain answer prints after them (#453).
type searchAnswer struct {
	Facts []searchFact `json:"facts"`
	Hints []string     `json:"hints,omitempty"`
}

func searchJSON(hits []store.Hit) []searchFact {
	out := make([]searchFact, 0, len(hits))
	for _, h := range hits {
		out = append(out, searchFact{ID: h.ID, Content: h.Content, Keywords: h.Keywords, Origin: h.Origin, Scope: h.Scope,
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

// runDump is git's textconv: memcue dump FILE. It opens the base read-only: a diff of a historical file must not
// migrate it, change its mode or rebuild its index.
func runDump(args []string, out io.Writer) error {
	if len(args) != 1 {
		return errors.New("usage: memcue dump FILE")
	}
	s, err := store.OpenReadOnly(args[0])
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
	driver, err := installDriver(top, exe)
	if err != nil {
		return err
	}
	q := shellQuote(driver)
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
	msg := fmt.Sprintf("merge driver registered in %s (driver script %s)\n", top, driver)
	if added {
		msg += fmt.Sprintf("added to .gitattributes: %s (commit it)\n", line)
	}
	_, err = fmt.Fprint(out, msg)
	return err
}

// installDriver writes the small shell script git calls for a merge or a diff. Pinning the absolute path of the
// running binary breaks the first time an upgrade moves it (a versioned Homebrew directory, a release unpacked into a
// versioned folder), and git then reports a binary conflict with no hint. The script looks for the binary again:
// MEMCUE_BIN, then PATH, then the path memcue had when install ran.
func installDriver(top, exe string) (string, error) {
	out, err := exec.Command("git", "-C", top, "rev-parse", "--git-path", "memcue-driver.sh").Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse --git-path: %w", err)
	}
	path := strings.TrimSpace(string(out))
	if path == "" {
		return "", errors.New("git did not name the driver script path")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(top, path)
	}
	script := "#!/bin/sh\n" +
		"# Written by memcue install. The binary may move on upgrade, so look it up again before failing.\n" +
		"for candidate in \"$MEMCUE_BIN\" \"$(command -v memcue 2>/dev/null)\" " + shellQuote(exe) + "; do\n" +
		"\tif [ -n \"$candidate\" ] && [ -x \"$candidate\" ]; then exec \"$candidate\" \"$@\"; fi\n" +
		"done\n" +
		"echo 'memcue: no memcue binary found; set MEMCUE_BIN, put memcue on PATH, or re-run memcue install' >&2\n" +
		"exit 1\n"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		return "", err
	}
	return path, nil
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
