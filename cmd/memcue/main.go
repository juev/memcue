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
  session-card    session-start without the note: the card for a host that has its own instructions
  hook-extract    PreCompact and SessionEnd hook: extracts the session's new facts in the background
  extract-session extract and store the facts of a session since the last run (started by hook-extract)
  extract-messages extract and store the facts of chat messages (OpenAI shape, JSON array on stdin): [-origin O] [-cwd DIR] [-session ID] [-event E] [-min-chars N] [-shelf SLUG [-shelf-only]]
  search    find the facts that fit the words, with their IDs: memcue search [-n N] [-min-score F] [-in WHERE] [-kind K] [-since DAY] [-expired] [-all-words] [-json] [-cwd DIR] [-session ID] TEXT
  add       store a fact: memcue add [-cwd DIR] [-keywords K1,K2] [-origin O] [-expires YYYY-MM-DD] [-candidate] [-source S] [-kind K] [-scope S | -shelf SLUG] -- TEXT
  candidates list the facts that wait for approval: memcue candidates [-json]
  vet       decide the candidates without asking: memcue vet [-dry-run] [-limit N] [-no-model] [-json] [-memory DIR]
  approve   take candidates into recall: memcue approve ID...
  correct   new version of a fact: memcue correct [-keywords K] [-expires YYYY-MM-DD|-no-expires] ID TEXT
  retire    take a fact out of recall: memcue retire ID
  reopen    bring a retired fact back as it was before the retire: memcue reopen ID
  facts     list the active facts
  reindex   rebuild the search index from the live facts: memcue reindex [-dry-run] [-json]
  dream     the work of a dream for the session's model, and its answer: memcue dream -prepare [-since DATE|-all] | -list | -apply FILE | -run [-all] [-j N]
  notes     which entries of the built-in memory note files are already facts, read-only: memcue notes [-dir DIR] [-json] [-min-share F]
  migrate   turn built-in memory notes into facts: memcue migrate [-dry-run] [-list] [-all] [-j N] [NOTE.md...]; move facts onto project keys: memcue migrate -repos [-apply] [-root DIR]... [OLD=NEW...]
  import    store the facts of migrate or extract -dry-run output (a session's facts as candidates): memcue import FILE.json...
  extract   add the facts of a session transcript: memcue extract [-dry-run] [-from N] [-model M] TRANSCRIPT.jsonl
  merge     git merge driver: memcue merge BASE OURS THEIRS
  dump      git textconv: memcue dump FILE
  install   set up the git repository of the base for sync: memcue install [-repo DIR]
  version   print the version: memcue version [-json] [-minimum X.Y.Z]

environment:
  MEMCUE_FACTS    fact base (default $CLAUDE_CONFIG_DIR/memory/facts.db or ~/.claude/memory/facts.db)
  MEMCUE_STATE_DIR  state directory; default $XDG_STATE_HOME/memcue, with a per-base subdirectory for a non-default base
  MEMCUE_MODEL    model of the extraction (default claude-opus-5-5)
  MEMCUE_API_URL  OpenAI-compatible endpoint for the extraction instead of claude -p (with MEMCUE_API_KEY)
  MEMCUE_API_KEY  key for MEMCUE_API_URL
  MEMCUE_EXTRACT  0 switches the extraction hook off
  MEMCUE_SEARCH_LOG 1 logs every search and the facts it found (search.log in the state directory; holds the query)
  MEMCUE_NUMBER_TERMS  versions, dates, ports and addresses are exact search terms by default; 0 turns that off
  MEMCUE_EVENT_TTL_DAYS  days a fact of kind event stays recalled (default 7; 0 keeps it without an expiry)
  MEMCUE_CARD_BUDGET  most characters of the session card (default 3500); the oldest user-stated rule is
                  dropped first
  MEMCUE_DREAM_EVERY  how long after a dream the session start says the next one is due (default 1h; dream.log in
                  the state directory)
  MEMCUE_VET      0 stops deciding the facts a session extracted; on by default (MEMCUE_API_URL, or claude -p when
                  claude is on PATH), after storing them; undecided ones become provisional for
                  MEMCUE_VET_TTL_DAYS
  MEMCUE_VET_POLICY  provisional (default) turns an unsure candidate into a provisional fact; quarantine
                  (or "memcue vet -quarantine") keeps the human queue
  MEMCUE_MEMORY_DIR  directory of the built-in memory notes the mirrors are checked against; empty skips that rule
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
		err = runSessionCard(os.Stdin, os.Stdout)
	case "hook-extract":
		err = runHookExtract(os.Stdin)
		if err != nil { // never block the session
			fmt.Fprintln(os.Stderr, "memcue:", err)
			err = nil
		}
	case "extract-session":
		err = runExtractSession(os.Args[2:])
	case "extract-messages":
		err = runExtractMessages(os.Args[2:], os.Stdin)
	case "search":
		err = runSearch(os.Args[2:], os.Stdout)
	case "add":
		err = runAdd(os.Args[2:], os.Stdout)
	case "correct":
		err = runCorrect(os.Args[2:], os.Stdout)
	case "candidates":
		err = runCandidates(os.Args[2:], os.Stdout)
	case "vet":
		err = runVet(os.Args[2:], os.Stdout)
	case "approve":
		err = runApprove(os.Args[2:], os.Stdout)
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
	case "extract":
		err = runExtract(os.Args[2:], os.Stdout)
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
