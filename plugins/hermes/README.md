# memcue for Hermes Agent

A memory provider for [Hermes Agent](https://github.com/NousResearch/hermes-agent) that keeps long-term memory in a
memcue fact base:

- **No facts come with a message** (#361). The system prompt block tells the model to search the base with
  `memcue_search` before it answers a question that may depend on earlier sessions, several times with synonyms, the
  system's name and both languages if the first try finds nothing.
- **The session card** (global rules, every rule the user stated and the decisions of the current repository) is part
  of the system prompt, through the same `memcue session-card` call Claude Code and DSH use (#363). Hermes caches the
  system prompt and rebuilds it after a compression or a reset, and each rebuild asks for a fresh card; a cron run
  gets none, and `session_card: false` turns it off. A failed card leaves the memory note without it until the next
  rebuild and is reported to the log at most every ten minutes.
- **The project** of a session is the git repository its working directory lies in (Hermes passes the session's
  `cwd`), named by its remote as in Claude Code: it names the origin of the facts a session learns, the `repo`
  scope, the card's decisions and the search tie-break. A session Hermes gives no `cwd` (a one-shot `hermes -z` or a
  cron run) takes the directory Hermes itself falls back to: `resolve_agent_cwd()`, that is `TERMINAL_CWD` or the
  process directory (#354). A session whose directory is one of Hermes's own outside git (`/opt/data`,
  `/opt/hermes`) has no project: its facts keep an empty origin and a `repo` scope is dropped (#347).
- **Session rotation** through `/new`, `/reset`, `/branch` or `/resume` rebinds the provider to the new session id
  after Hermes has ended the old one; a reset clears its buffers, and an undo forgets the seen fingerprints so the
  truncated part can be extracted again.
- **Writes of the built-in memory tool are mirrored.** A new `MEMORY.md` or `USER.md` entry becomes a candidate
  fact (source `memory`, origin `hermes-memory`) and waits for approval with `memcue approve`. When the agent
  edits that entry the fact gets a new version, when it removes it the fact is retired. Which fact an entry became is
  kept in `$HERMES_HOME/memcue/written.json`. An explicit `memcue_correct` of an approved fact stays active: it is a
  correction, not a new candidate.
- **Tools** `memcue_search`, `memcue_add`, `memcue_correct`, `memcue_retire` let the model find and fix facts.
  `memcue_search` takes an optional `limit` and runs `memcue search` with the session's workspace and id, so facts of
  this host and of the session's repository win ties and `MEMCUE_SEARCH_LOG=1` can name the session; `memcue_correct`
  can set `keywords` and an `expires` date, as the CLI does.
  Subagents, cron and flush runs get only `memcue_search` and do not write facts; a cron run also gets the dream
  tools below.
- **Dream tools** `memcue_dream_prepare` and `memcue_dream_apply` let the model tidy the base: the first returns the
  instructions and the facts to split, give search queries and review in groups (`since: all` for the first dream
  on a base), the second takes the JSON answer and reports what memcue applied and refused. See [Dream](#dream).
- **Session extraction** (optional): `MEMCUE_EXTRACT=0` in the gateway's environment turns it off and wins over
  `extract_url`; otherwise the new messages of a session go to `memcue extract-messages` in the
  background, which sends them to an OpenAI-compatible model with the same prompt Claude Code sessions get, and adds
  the facts it finds as candidates for review. A chat of a messaging gateway can stay one session for days, so the messages are not held until
  the session ends: they go once 20 000 characters of new text gather (`extract_threshold`), after 30 minutes without
  a turn (`extract_idle_minutes`), before the context is compressed, when the session ends and when Hermes shuts
  down. Shutdown waits a bounded 20 seconds for the in-flight calls; a longer one keeps its pending batch for the next
  start. Each message is sent once; fingerprints of the sent ones are kept in `$HERMES_HOME/memcue/sessions/`, which
  also survives compression rewriting the message list. Tool output goes too, cut to
  `extract_tool_result_chars` (1500 characters, the same cut memcue applies to a `RESULT` block): a fact sourced from
  a tool can quote its evidence only from tool output, and the write policy drops such a fact without the quote. Each
  run is a line in memcue's `extract.log`, in the binary's state directory: `$XDG_STATE_HOME/memcue/<hash>` for the
  Hermes base (a non-default base gets its own subdirectory), or `MEMCUE_STATE_DIR` when set.

## Review and vet

Mirrored built-in memory writes and extracted facts are **candidates**: the base stores them, search ignores them, and
`memcue vet` or `memcue approve ID...` decides. A fact written by `memcue_add` is active at once;
`memcue_correct` of an approved fact is a deliberate correction, not a new candidate. The shared rules are in
[Review and vet](../../README.md#review-and-vet).

Hermes runs one external memory provider at a time, so memcue replaces the one you have (holographic, byterover,
…). The built-in `MEMORY.md`/`USER.md` stays on either way.

## Dream

memcue starts no model for the dream: Hermes's own model does it, in a cron job. One job every four hours, with its
output kept out of the chats:

```sh
hermes cron create 'every 4h' --name memcue-dream --deliver local \
  'Tidy long-term memory: call memcue_dream_prepare, follow its instructions, answer with memcue_dream_apply, and repeat while it still returns facts. Stop when it says "nothing for the dream".'
```

The first dream on a base needs `since: all` (the whole base) or a date; the job's first run can be told so once,
or run `memcue dream -prepare -all` by hand. A packet holds up to 40 fresh facts and 30 groups, so a large backlog
takes a few runs; the rules are in [Dream](../../README.md#dream).

## Install

1. Optional: put the `memcue` binary where the Hermes process can run it. In the official Docker image that is a Linux
   binary (`uname -m` in the container tells amd64 or arm64), for example in `$HERMES_HOME/bin/memcue`. It is a static
   binary with no Python dependencies. Without a configured `binary` and without a current `memcue` on `PATH`, the
   provider installs the binary of its own release into `$HERMES_HOME/memcue/bin/<version>/` in the background
   (`MEMCUE_AUTO_INSTALL=0` in the gateway's environment turns that off; see
   [Binary from the plugin](../../README.md#binary-from-the-plugin)).

2. Copy this plugin into Hermes' plugin directory:

   ```bash
   cp -r plugins/hermes/memcue "$HERMES_HOME/plugins/memcue"
   ```

3. Select it and, if the binary is not on `PATH`, tell the plugin where it is:

   ```bash
   hermes config set memory.provider memcue
   hermes config set memory.memcue.binary "$HERMES_HOME/bin/memcue"
   ```

   If the container's host name is not stable, name the machine explicitly. The provider forwards it to memcue as
   `MEMCUE_HOST`, and search uses it to prefer facts of this host on equal scores:

   ```bash
   hermes config set memory.memcue.host mac
   ```

4. Optional: turn on session extraction with an OpenAI-compatible endpoint. The key is read from the environment
   variable you name, so it stays where Hermes keeps its other keys:

   ```bash
   hermes config set memory.memcue.extract_url https://api.deepseek.com
   hermes config set memory.memcue.extract_model deepseek-flash
   hermes config set memory.memcue.extract_key_env DEEPSEEK_API_KEY
   ```

5. Optional: keep `usage.log`, which records for each reply whether it quoted an identifier of the facts its searches
   returned in that turn. Both settings live in the provider config, so the environment of the gateway or its container stays as it
   is; `state_dir` defaults to `$XDG_STATE_HOME`, else `~/.local/state`, and the log goes to `<state_dir>/memcue/`:

   ```bash
   hermes config set memory.memcue.usage_log true
   hermes config set memory.memcue.state_dir "$HERMES_HOME/memcue/state"
   ```

   `MEMCUE_USAGE_LOG=1` in the gateway's environment still turns the log on as well.

6. Restart Hermes (the gateway, if you run one).

The provider asks the binary for its version when the card or extraction first runs: 0.4.0 or newer is required, and
an older one turns those two paths off with a warning in the log while search keeps working. A build from source
reports a suffixed version and counts as the current tree.

Check: `hermes memory status` should list `memcue` as available. Run the plugin's tests with the binary you
installed:

```bash
MEMCUE_BIN="$HERMES_HOME/bin/memcue" python3 "$HERMES_HOME/plugins/memcue/tests/test_provider.py"
```

## The fact base

By default the base is `$HERMES_HOME/memcue/facts.db`, inside Hermes' home, so `hermes backup` covers it. Set
`memory.memcue.facts` (or `MEMCUE_FACTS` in Hermes' environment) to use another file, for example one shared with
Claude Code. A leading `~/` or `$HOME/` is expanded; the path must be absolute, so the value names one file rather
than one per process working directory. A shared base can be synced through git like the Claude Code one: run `memcue install` once in the
repository that holds it. The binary's logs, locks and dream schedule follow the base: the default Hermes base gets
its own `$XDG_STATE_HOME/memcue/<hash>` directory, so it does not share a dream clock with a Claude Code/DSH base.
The provider's own state (seen fingerprints, pending batches, the `written.json` mirror map) names the base in a
`base` file: changing `facts` drops that state for the old base and starts the new one fresh, with a warning in the
log.

Entries written to `MEMORY.md`/`USER.md` before the provider was switched on are not copied. Add the ones worth
keeping with `memcue add`, or let the agent do it with `memcue_add`.
