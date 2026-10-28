// Command memcue keeps a fact base for Claude Code and finds the facts the agent searches for. Run it without arguments for
// the list of commands.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/juev/memcue/internal/extract"
)

var version = "dev"

// runVersion prints the version, or checks it against a minimum: an adapter that needs session-card or -min-chars
// calls `memcue version -minimum 0.4.0` and gets a non-zero exit with the reason on an older binary.
func runVersion(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print the version as JSON")
	minimum := fs.String("minimum", "", "exit non-zero when this build is older than the given release")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *minimum != "" && !versionAtLeast(version, *minimum) {
		return fmt.Errorf("memcue %s is older than the required %s: update memcue", version, *minimum)
	}
	if *asJSON {
		return json.NewEncoder(out).Encode(struct {
			Version string `json:"version"`
		}{version})
	}
	_, err := fmt.Fprintln(out, version)
	return err
}

// versionAtLeast compares release versions numerically, component by component. A dev or unparsable version counts as
// new enough: the caller is then running a working tree or a build memcue cannot judge, and refusing it would block
// development for no safety gain.
func versionAtLeast(current, minimum string) bool {
	if current == "" || current == "dev" {
		return true
	}
	cur, ok1 := parseVersion(current)
	min, ok2 := parseVersion(minimum)
	if !ok1 || !ok2 {
		return true
	}
	for i := range cur {
		if cur[i] != min[i] {
			return cur[i] > min[i]
		}
	}
	return true
}

// parseVersion reads a clean release X.Y.Z, with an optional leading v. A build suffix (a VCS pseudo-version, a
// -dirty marker, the dsh- tag prefix) means a source build, which the caller treats as the current tree: only a
// released number can be judged against the minimum.
func parseVersion(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

func init() {
	// go install leaves the version unset; the module version it built is in the build info.
	if bi, ok := debug.ReadBuildInfo(); ok && version == "dev" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		version = strings.TrimPrefix(bi.Main.Version, "v")
	}
}

const usage = `usage: memcue <command>

commands:
  session-start   SessionStart hook: tells the agent where its memory is: memcue session-start [-part N] (1 to 3, one share of the text for each hook)
  session-card    session-start without the note: the card for a host that has its own instructions: memcue session-card [-dream-tools]
  search    find the facts that fit the words, with their IDs: memcue search [-n N] [-min-score F] [-in WHERE] [-kind K] [-since DAY] [-expired] [-all-words] [-json] [-cwd DIR] [-session ID] TEXT
  add       store a fact, indexed at once: memcue add [-cwd DIR] [-keywords K1,K2] [-origin O] [-expires YYYY-MM-DD] [-source S] [-kind K] [-scope S | -shelf SLUG] -- TEXT
  correct   new version of a fact: memcue correct [-keywords K] [-expires YYYY-MM-DD|-no-expires] ID [--] TEXT
  retire    take a fact out of recall: memcue retire ID
  reopen    bring a retired fact back as it was before the retire: memcue reopen ID
  facts     list the active facts
  reindex   rebuild the search index from the live facts: memcue reindex [-dry-run] [-json]
  dream     the work of a dream for the session's model, and its answer: memcue dream -prepare [-since DATE|-all] | -list | -apply FILE | -run [-all] [-j N]
  notes     which entries of the built-in memory note files are already facts, read-only: memcue notes [-dir DIR] [-json] [-min-share F]
  migrate   turn built-in memory notes into facts: memcue migrate [-dry-run] [-list] [-all] [-j N] [NOTE.md...]; move facts onto project keys: memcue migrate -repos [-apply] [-root DIR]... [OLD=NEW...]; move the base to a text history: memcue migrate -text
  import    store the facts of migrate -dry-run output: memcue import FILE.json...
  merge     git merge driver: memcue merge BASE OURS THEIRS
  dump      git textconv: memcue dump FILE
  install   set up the git repository of the base for sync: memcue install [-repo DIR]
  version   print the version: memcue version [-json] [-minimum X.Y.Z]

environment:
  MEMCUE_FACTS    fact base (default $CLAUDE_CONFIG_DIR/memory/facts.db or ~/.claude/memory/facts.db)
  MEMCUE_STATE_DIR  state directory; default $XDG_STATE_HOME/memcue, with a per-base subdirectory for a non-default base
  MEMCUE_MODEL    model of migrate, dream -run and the search queries add asks for (default claude-opus-5-5)
  MEMCUE_API_URL  OpenAI-compatible endpoint for those calls instead of claude -p (with MEMCUE_API_KEY); add asks
                  for search queries only when it is set
  MEMCUE_API_KEY  key for MEMCUE_API_URL
  MEMCUE_SEARCH_LOG 1 logs every search and the facts it found (search.log in the state directory; holds the query)
  MEMCUE_NUMBER_TERMS  versions, dates, ports and addresses are exact search terms by default; 0 turns that off
  MEMCUE_EVENT_TTL_DAYS  days a fact of kind event stays recalled (default 7; 0 keeps it without an expiry)
  MEMCUE_CARD_BUDGET  most characters of the session card (default 3500); the oldest user-stated rule is
                  dropped first
  MEMCUE_DREAM_EVERY  how long after a dream the session start says the next one is due (default 1h; dream.log in
                  the state directory)
  MEMCUE_MEMORY_DIR  directory of the built-in memory notes memcue notes reads by default
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if m := os.Getenv("MEMCUE_MODEL"); m != "" {
		extract.Model = m
	}
	extract.API.URL, extract.API.Key = os.Getenv("MEMCUE_API_URL"), os.Getenv("MEMCUE_API_KEY")
	var err error
	switch os.Args[1] {
	case "session-start":
		fs := flag.NewFlagSet("session-start", flag.ContinueOnError)
		part := fs.Int("part", 0, "the share of the text this hook carries, 1 to 3; 0 is the whole text in one block")
		if err = fs.Parse(os.Args[2:]); err == nil {
			err = runSessionStartPart(os.Stdin, os.Stdout, *part)
		}
	case "session-card":
		fs := flag.NewFlagSet("session-card", flag.ContinueOnError)
		dreamTools := fs.Bool("dream-tools", false, "also say when a dream is due, naming the memcue_dream_* tools")
		if err = fs.Parse(os.Args[2:]); err == nil {
			err = runSessionCardWith(os.Stdin, os.Stdout, *dreamTools)
		}
	case "hook-extract", "extract-session", "extract-messages":
		// Nothing is extracted any more (#494), but a plugin older than that still calls these after a turn or at
		// the end of a session: take its input and exit 0, so it logs no failure for every turn.
		_, _ = io.Copy(io.Discard, os.Stdin)
	case "search":
		err = runSearch(os.Args[2:], os.Stdout)
	case "add":
		err = runAdd(os.Args[2:], os.Stdout)
	case "correct":
		err = runCorrect(os.Args[2:], os.Stdout)
	case "candidates", "approve":
		err = errNoApproval(os.Args[1])
	case "retire":
		err = runRetire(os.Args[2:], os.Stdout)
	case "reopen":
		err = runReopen(os.Args[2:], os.Stdout)
	case "facts":
		err = runFacts(os.Stdout)
	case "reindex":
		err = runReindex(os.Args[2:], os.Stdout)
	case "dream":
		err = runDream(os.Args[2:], os.Stdout)
	case "notes":
		err = runNotes(os.Args[2:], os.Stdout)
	case "migrate":
		err = runMigrate(os.Args[2:], os.Stdout)
	case "import":
		err = runImport(os.Args[2:], os.Stdout)
	case "merge":
		err = runMerge(os.Args[2:], os.Stderr)
	case "dump":
		err = runDump(os.Args[2:], os.Stdout)
	case "install":
		err = runInstall(os.Args[2:], os.Stdout)
	case "version":
		err = runVersion(os.Args[2:], os.Stdout)
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "memcue:", err)
		os.Exit(1)
	}
}

func claudeDir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}
