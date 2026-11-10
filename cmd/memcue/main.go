// Command memcue keeps a fact base for Claude Code and finds the facts the agent searches for. Run it without arguments for
// the list of commands.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"

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
  session-start   SessionStart hook: repository pitfalls, memory instructions and hints: memcue session-start [-part N] (part 1 carries the text; 2 and 3 print nothing, for an older plugin)
  session-card    session-start without the note: repository pitfalls and maintenance hints: memcue session-card [-dream-tools] [-pitfalls-only | -pitfalls-json | -hints-only]
  search    find the facts that fit the words, with their IDs: memcue search [-n N] [-min-score F] [-in WHERE] [-kind K] [-since DAY] [-expired] [-all-words] [-json] [-cwd DIR] [-session ID] TEXT
  sessions  passages of earlier Claude Code sessions that hold the words, newest first, when search finds nothing: memcue sessions [-n N] [-before TIME] [-cwd DIR] [-session IDS] [-json] TEXT
  add       store a fact, indexed at once: memcue add [-cwd DIR] [-origin O] [-expires YYYY-MM-DD] [-source S] [-kind K] [-scope S | -shelf SLUG] -- TEXT
  # add -batch FILE [-force] atomically adds a content-only JSON FactInput array
  correct   new version of a fact: memcue correct [-expires YYYY-MM-DD|-no-expires] ID [--] TEXT
  retire    take a fact out of recall: memcue retire ID
  reopen    bring a retired fact back as it was before the retire: memcue reopen ID
  connect   explicit tailnet lifecycle: up [-bootstrap] | status | serve [-bootstrap] | login | off
  history   immutable fact versions: memcue history [-json] ID
  facts     list the active facts
  reindex   rebuild the search index from the live facts: memcue reindex [-dry-run] [-json]
  dream     the work of a dream for the session's model, and its answer: memcue dream -prepare [-since DATE|-all] | -list | -apply FILE | -run [-all|-background] [-j N]
  notes     which entries of the built-in memory note files are already facts, read-only: memcue notes [-dir DIR] [-json] [-min-share F]
  migrate   turn built-in memory notes into facts: memcue migrate [-dry-run] [-list] [-all] [-j N] [NOTE.md...]; move facts onto project keys: memcue migrate -repos [-apply] [-root DIR]... [OLD=NEW...]; move the base to a text history: memcue migrate -text; copy the whole history into an empty PostgreSQL base: memcue migrate -postgres [-apply]; load the base into an empty memcue server: memcue migrate -server [-apply]
  import    store the facts of migrate -dry-run output: memcue import FILE.json...
  merge     git merge driver: memcue merge BASE OURS THEIRS
  dump      git textconv: memcue dump FILE
  install   set up the git repository of the base for sync: memcue install [-repo DIR]
  server    serve the base of MEMCUE_FACTS to replicas on the tailnet: memcue server [-bootstrap]
  version   print the version: memcue version [-json] [-minimum X.Y.Z]

environment:
  MEMCUE_POSTGRES_DSN  explicit PostgreSQL backend; empty keeps SQLite/text history offline
  MEMCUE_SERVER   tailnet name of a memcue server: work on this machine's replica of it, synced by the helper
  MEMCUE_TAILSCALE_MODE  off (default) or embedded; see docs/remote-backends.md
  MEMCUE_FACTS    fact base (default $CLAUDE_CONFIG_DIR/memory/facts.db or ~/.claude/memory/facts.db)
  MEMCUE_STATE_DIR  state directory; default $XDG_STATE_HOME/memcue, with a per-base subdirectory for a non-default base
  MEMCUE_MODEL    model of migrate and dream -run (default claude-opus-5-5)
  MEMCUE_API_URL  OpenAI-compatible endpoint for migrate and dream instead of claude -p (with MEMCUE_API_KEY)
  MEMCUE_API_KEY  key for MEMCUE_API_URL
  MEMCUE_CLAUDE_TOKEN_FILE  file of a Claude Code OAuth token (claude setup-token) claude -p runs with; ~/ is the
                  home directory, and a missing file leaves claude -p on its own login
  MEMCUE_SEARCH_LOG 1 logs every search and the facts it found (search.log in the state directory; holds the query)
  MEMCUE_CALLER   name of the hook or plugin that runs a search, recorded in search.log
  MEMCUE_NUMBER_TERMS  versions, dates, ports and addresses are exact search terms by default; 0 turns that off
  MEMCUE_EVENT_TTL_DAYS  days a fact of kind event stays recalled (default 7; 0 keeps it without an expiry)
  MEMCUE_DREAM_EVERY  how long after a dream the next one is due (default 24h; dream.log in the state directory)
  MEMCUE_DREAM_BACKGROUND  0 keeps the session start from running a due dream in the background, on the endpoint
                  or on claude -p; it then says the dream is due instead
  MEMCUE_MEMORY_DIR  directory of the built-in memory notes memcue notes reads by default
`

func main() { os.Exit(runMain()) }

func runMain() (code int) {
	// Return stdout write errors through the mutation outcome boundary.
	signal.Ignore(syscall.SIGPIPE)
	inv := newInvocation()
	restoreStderr := inv.bindStderr()
	defer restoreStderr()
	var err error
	rec := newOutcomeRecorder(inv)
	out, diagnostics := io.Writer(rec.stdout), io.Writer(rec.stderr)
	defer func() { rec.finish(err, code); inv.close() }()

	if len(os.Args) < 2 {
		_, _ = fmt.Fprint(diagnostics, usage)
		return 2
	}
	if err = remoteCommandAllowed(os.Args[1]); err != nil && !migratesToPostgres(os.Args[1:]) {
		_, _ = fmt.Fprintln(diagnostics, "memcue:", err)
		return 1
	}
	if err = serverCommandAllowed(os.Args[1:]); err != nil {
		_, _ = fmt.Fprintln(diagnostics, "memcue:", err)
		return 1
	}
	if m := os.Getenv("MEMCUE_MODEL"); m != "" {
		extract.Model = m
	}
	extract.API.URL, extract.API.Key = os.Getenv("MEMCUE_API_URL"), os.Getenv("MEMCUE_API_KEY")
	extract.ClaudeTokenFile = os.Getenv("MEMCUE_CLAUDE_TOKEN_FILE")
	switch os.Args[1] {
	case "connect":
		if len(os.Args) > 2 && os.Args[2] == "up" {
			err = runConnectContext(inv.ctx, os.Args[2:], out)
		} else {
			err = runConnect(os.Args[2:], out)
		}
	case "session-start":
		fs := flag.NewFlagSet("session-start", flag.ContinueOnError)
		instructionsOnly := fs.Bool("instructions-only", false, "omit automatic pitfalls when separate whole-fact hooks deliver them")
		part := fs.Int("part", 0, "the share of the text this hook carries, 1 to 3; 0 is the whole text in one block")
		if err = fs.Parse(os.Args[2:]); err == nil {
			err = inv.runSessionStartWith(os.Stdin, out, *part, !*instructionsOnly)
		}
	case "session-card":
		fs := flag.NewFlagSet("session-card", flag.ContinueOnError)
		hintsOnly := fs.Bool("hints-only", false, "maintenance hints without automatic pitfall context")
		pitfallsJSON := fs.Bool("pitfalls-json", false, "whole-fact repository snapshot for native function hooks")
		pitfallsOnly := fs.Bool("pitfalls-only", false, "only automatic repository pitfalls, without instructions or maintenance hints")
		dreamTools := fs.Bool("dream-tools", false, "also say when a dream is due, naming the memcue_dream_* tools")
		if err = fs.Parse(os.Args[2:]); err == nil {
			if *pitfallsJSON {
				err = inv.runSessionPitfallsJSON(os.Stdin, out)
			} else if *pitfallsOnly {
				err = inv.runSessionPitfalls(os.Stdin, out)
			} else {
				err = inv.runSessionCardMode(os.Stdin, out, *dreamTools, !*hintsOnly)
			}
		}
	case "hook-extract", "extract-session", "extract-messages":
		// Nothing is extracted any more (#494), but a plugin older than that still calls these after a turn or at
		// the end of a session: take its input and exit 0, so it logs no failure for every turn.
		_, _ = io.Copy(io.Discard, os.Stdin)
	case "search":
		err = inv.runSearch(os.Args[2:], out)
	case "sessions":
		err = runSessions(os.Args[2:], out)
	case "add":
		err = inv.runAdd(os.Args[2:], out)
	case "correct":
		err = inv.runCorrect(os.Args[2:], out)
	case "candidates", "approve":
		err = errNoApproval(os.Args[1])
	case "retire":
		err = inv.runRetire(os.Args[2:], out)
	case "reopen":
		err = inv.runReopen(os.Args[2:], out)
	case "history":
		err = inv.runHistory(os.Args[2:], out)
	case "facts":
		err = inv.runFacts(out)
	case "reindex":
		err = inv.runReindex(os.Args[2:], out)
	case "dream":
		err = inv.runDream(os.Args[2:], out)
	case "notes":
		err = inv.runNotes(os.Args[2:], out)
	case "migrate":
		err = inv.runMigrate(os.Args[2:], out)
	case "import":
		err = inv.runImport(os.Args[2:], out)
	case "merge":
		err = runMerge(os.Args[2:], diagnostics)
	case "dump":
		err = runDump(os.Args[2:], out)
	case "install":
		err = runInstall(os.Args[2:], out)
	case "server":
		err = runServer(os.Args[2:])
	case "version":
		err = runVersion(os.Args[2:], out)
	case "-h", "--help", "help":
		_, _ = fmt.Fprint(out, usage)
	default:
		_, _ = fmt.Fprint(diagnostics, usage)
		return 2
	}
	if err != nil {
		_, _ = fmt.Fprintln(diagnostics, "memcue:", err)
		return 1
	}

	return 0
}

func claudeDir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}
