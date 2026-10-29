# memcue for Hermes Agent

A memory provider for [Hermes Agent](https://github.com/NousResearch/hermes-agent) that keeps long-term memory in a
memcue fact base:

- **No facts come with a message** (#361). The system prompt block tells the model to search the base with
  `memcue_search` before it answers a question that may depend on earlier sessions, several times with synonyms, the
  system's name and both languages if the first try finds nothing. It also lets the model search during work, when it
  decides that may help: on starting with a specific host, service or repository, and before it diagnoses an
  unexpected error, with a short query naming the system and the problem, checking the result against the current
  state (#521). It is a hint, not a rule: no search runs on its own.
- **The session card** (global rules, the rules the user stated and the decisions of the current repository) is part
  of the system prompt, through the same `memcue session-card` call Claude Code and DSH use (#363). Hermes caches the
  system prompt and rebuilds it after a compression or a reset, and each rebuild asks for a fresh card; a cron run
  gets none, and `session_card: false` turns it off. `session_card_chars` (passed as `MEMCUE_CARD_BUDGET`; default 0
  keeps the binary's 3500) bounds the card, stated rules included: the binary drops the oldest user-stated rule first
  and ends the card with a line that counts what it left out (#487). A failed card leaves the memory note without it
  until the next rebuild and is reported to the log at most every ten minutes. The provider puts the card in as the
  binary renders it and drops no line of it, even one that repeats `MEMORY.md` or `USER.md`: a word rule that drops
  copies drops another rule in the same words too, the opposite one or one with a condition. A copy costs a few
  lines, and nothing in the provider removes it (#456).
- **The project** of a session is the git repository its working directory lies in (Hermes passes the session's
  `cwd`), named by its remote as in Claude Code: it names the origin of the facts a session learns, the `repo`
  scope, the card's decisions and the search tie-break. A session Hermes gives no `cwd` (a one-shot `hermes -z` or a
  cron run) takes the directory Hermes itself falls back to: `resolve_agent_cwd()`, that is `TERMINAL_CWD` or the
  process directory (#354). A session whose directory is one of Hermes's own outside git (`/opt/data`,
  `/opt/hermes`) has no project: its facts keep an empty origin and a `repo` scope is dropped (#347).
- **The model keeps its memory itself** (#494). The system prompt block and `memcue_add` tell it to store a rule or a
  correction of the user, a decision the code does not show, a pitfall, a host, a service or a pointer when it learns
  it, to search first and correct a stored fact instead of adding a copy. Nothing is extracted from the session
  afterwards. `memcue_add` takes a `source`: a `rule` or `decision` is the user's words by default, the only source
  that reaches the session card; `tool` is what a tool's output showed, `agent` anything else, and memcue refuses
  installation state from either (#475).
- **Session rotation** through `/new`, `/reset`, `/branch` or `/resume` rebinds the provider to the new session id
  after Hermes has ended the old one; a reset forgets what the old session's searches returned.
- **Writes of the built-in memory tool are mirrored.** A new `MEMORY.md` or `USER.md` entry becomes an approved
  global fact (source `memory`, origin `hermes-memory`; #440). When the agent edits that entry the fact gets a new
  version, when it removes it the fact is retired. Which fact an entry became is kept in
  `$HERMES_HOME/memcue/written.json`. The built-in memory is a second memory next to memcue: turn it off once the
  model writes to memcue, with `hermes config set memory.memory_enabled false` and
  `hermes config set memory.user_profile_enabled false`. memcue does not change Hermes's settings. While Hermes still
  injects `MEMORY.md` or `USER.md` and the file holds entries, the provider logs one warning per start; a file Hermes
  no longer injects is no note for it and raises no warning.
- **Tools** `memcue_search`, `memcue_add`, `memcue_correct`, `memcue_retire` let the model find and fix facts.
  `memcue_search` takes an optional `limit` and runs `memcue search` with the session's workspace and id, so facts of
  this host and of the session's repository win ties and `MEMCUE_SEARCH_LOG=1` can name the session; `memcue_correct`
  can set `keywords` and an `expires` date, as the CLI does. The call hands over at most **five** facts unless `limit`
  says otherwise (on a live base ten doubled the characters and reached no prompt five did not), and only those that
  fit the query: a fact far below the best one is left out, so one fitting fact comes alone (#407). `search_facts`
  (default 5, 1–50) sets the most.
  Subagents, cron and flush runs get only `memcue_search` and do not write facts; a cron run also gets the dream
  tools below.
- **Dream tools** `memcue_dream_prepare` and `memcue_dream_apply` let the model tidy the base: the first returns the
  instructions and the facts to split, give search queries, translate and review in groups (`since: all` for the first dream
  on a base), the second takes the JSON answer and reports what memcue applied and refused. See [Dream](#dream).
## Candidates

A mirrored memory write and a fact written by `memcue_add` are indexed at once, and `memcue_correct` writes a new
version. Nothing waits for approval: `memcue add -candidate`, `memcue candidates` and `memcue approve` are removed
(#514). A candidate or provisional fact an older memcue left in the base is live too, unless it is retired or expired
(see [Candidates](../../README.md#candidates)).

Hermes runs one external memory provider at a time, so memcue replaces the one you have (holographic, byterover,
…). The built-in `MEMORY.md`/`USER.md` is not a provider and stays on until `memory.memory_enabled` and
`memory.user_profile_enabled` turn it off.

## Dream

A dream is due once `MEMCUE_DREAM_EVERY` (default `1h`) has passed since the last one and some fact is fresh or lacks
a translation. With an endpoint configured, the `session-card` call that builds the system prompt starts one
`memcue dream -run` pass in the background and the card says nothing of the dream (#569):

```bash
hermes config set memory.memcue.api_url https://api.deepseek.com
hermes config set memory.memcue.model deepseek-flash
hermes config set memory.memcue.api_key_env DEEPSEEK_API_KEY
```

`api_url`, `model` and `api_key_env` reach the binary as `MEMCUE_API_URL`, `MEMCUE_MODEL` and `MEMCUE_API_KEY`, the
key read from the named environment variable of the gateway, so it stays out of `config.yaml`. They also give
`memcue_add` its search queries in one short call. Without them the gateway's own `MEMCUE_API_URL` decides, and a
`claude` on the gateway's `PATH` runs the dream on `claude -p`.

Without either, the card in the system prompt ends with one line that says a dream is due and names the tools (the
provider asks `memcue session-card -dream-tools`, a binary 0.19.0 or newer; #494). The model runs
`memcue_dream_prepare`, answers the packet and hands the answer to `memcue_dream_apply` when the user's task allows;
it never interrupts the user's work for it. Hermes builds the system prompt once and again after a compression or a
reset, so the line is as fresh as the last rebuild. `dream: false` in `memory.memcue` turns off both the background
dream and the line; the tools stay.

The first dream on a base needs `since: all` (the whole base) or a date: ask the agent to call
`memcue_dream_prepare` with `since: all`, or run `memcue dream -prepare -all` by hand. `memcue dream -run` still runs
a dream through an endpoint when you start it yourself; the rules are in [Dream](../../README.md#dream).

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

4. Optional: keep `usage.log`, which records for each reply whether it quoted an identifier of the facts its searches
   returned in that turn. Both settings live in the provider config, so the environment of the gateway or its container stays as it
   is; `state_dir` defaults to `$XDG_STATE_HOME`, else `~/.local/state`, and the log goes to `<state_dir>/memcue/`:

   ```bash
   hermes config set memory.memcue.usage_log true
   hermes config set memory.memcue.state_dir "$HERMES_HOME/memcue/state"
   ```

   `MEMCUE_USAGE_LOG=1` in the gateway's environment still turns the log on as well.

5. Restart Hermes (the gateway, if you run one).

The provider asks the binary for its version when the card first runs: 0.4.0 or newer is required, and an older one
turns the card off with a warning in the log while search keeps working; the dream line needs 0.19.0. A build from source
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
After `memcue migrate -text` the base is the text history `$HERMES_HOME/memcue/facts`, which `hermes backup` still
covers, and its SQLite cache sits in that state directory. The provider opens the text from the next start and keeps
the mirror of the built-in memory, since `facts.db` and `facts` are one base (see the main README, "Moving to the text
history"). A Hermes base outside git does not need the move.
The provider's own state, the `written.json` mirror map, names the base in a `base` file: changing `facts` drops that
state for the old base and starts the new one fresh, with a warning in the log. The `sessions/` and `pending/` an
older provider kept for its extraction are removed at start.

Entries written to `MEMORY.md`/`USER.md` before the provider was switched on are not copied. Add the ones worth
keeping with `memcue add`, or let the agent do it with `memcue_add`.
