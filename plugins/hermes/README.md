# memcue for Hermes Agent

A memory provider for [Hermes Agent](https://github.com/NousResearch/hermes-agent) that keeps long-term memory in a
memcue fact base:

- **Before each turn** the user's message goes to `memcue hook`; at most two matching facts come back, or nothing.
  Hermes puts them into the turn's `<memory-context>` block. Each fact comes once per session, and again after the
  context is compressed, which drops what the session already got. A cron run gets none: it is a background task's
  notice, whose wrapper matches facts by accident.
- **The session card** (global rules and the decisions of the current repository) rides with the first turn and
  again after a compression, through the same `memcue session-card` call Claude Code and DSH use. It stays in the
  conversation instead of being repeated in the system prompt; `session_card: false` turns it off.
- **Session rotation** through `/new`, `/reset`, `/branch` or `/resume` rebinds the provider to the new session id
  after Hermes has ended the old one; a reset clears its buffers, and an undo forgets the seen fingerprints so the
  truncated part can be extracted again.
- **Writes of the built-in memory tool are mirrored.** A new `MEMORY.md` or `USER.md` entry becomes a candidate
  fact (source `memory`) and waits for approval with `memcue review`/`approve`. With the default
  `suppress_mirrored: true`, an approved mirrored fact is still left out of `prefetch`, because the same entry already
  rides in the built-in memory block; set `suppress_mirrored: false` to let it be recalled as well. When the agent
  edits that entry the fact gets a new version, when it removes it the fact is retired. Which fact an entry became is
  kept in `$HERMES_HOME/memcue/written.json`. An explicit `memcue_correct` of an approved fact stays active: it is a
  correction, not a new candidate.
- **Tools** `memcue_search`, `memcue_add`, `memcue_correct`, `memcue_retire` let the model find and fix facts.
  Subagents, cron and flush runs get only `memcue_search` and do not write.
- **Session extraction** (optional): the new messages of a session go to `memcue extract-messages` in the
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

Mirrored built-in memory writes and extracted facts are **candidates**: the base stores them, recall ignores them, and
`memcue vet`, `memcue approve ID...` or `memcue review` decides. A fact written by `memcue_add` is active at once;
`memcue_correct` of an approved fact is a deliberate correction, not a new candidate. The shared rules are in
[Review and vet](../../README.md#review-and-vet).

Hermes runs one external memory provider at a time, so memcue replaces the one you have (holographic, byterover,
…). The built-in `MEMORY.md`/`USER.md` stays on either way.

## Install

1. Put the `memcue` binary where the Hermes process can run it. In the official Docker image that is a Linux binary
   (`uname -m` in the container tells amd64 or arm64), for example in `$HERMES_HOME/bin/memcue`. It is a static
   binary with no Python dependencies.

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
   `MEMCUE_HOST`, and recall uses it to show a fact's scope and to prefer facts of this host on equal scores:

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

5. Optional: keep `usage.log`, which records for each reply whether it quoted an identifier of the facts it was
   given. Both settings live in the provider config, so the environment of the gateway or its container stays as it
   is; `state_dir` defaults to `$XDG_STATE_HOME`, else `~/.local/state`, and the log goes to `<state_dir>/memcue/`:

   ```bash
   hermes config set memory.memcue.usage_log true
   hermes config set memory.memcue.state_dir "$HERMES_HOME/memcue/state"
   ```

   `MEMCUE_USAGE_LOG=1` in the gateway's environment still turns the log on as well.

6. Restart Hermes (the gateway, if you run one).

Check: `hermes memory status` should list `memcue` as available. Run the plugin's tests with the binary you
installed:

```bash
MEMCUE_BIN="$HERMES_HOME/bin/memcue" python3 "$HERMES_HOME/plugins/memcue/tests/test_provider.py"
```

## The fact base

By default the base is `$HERMES_HOME/memcue/facts.db`, inside Hermes' home, so `hermes backup` covers it. Set
`memory.memcue.facts` (or `MEMCUE_FACTS` in Hermes' environment) to use another file, for example one shared with
Claude Code. A shared base can be synced through git like the Claude Code one: run `memcue install` once in the
repository that holds it. The binary's logs, locks and dream schedule follow the base: the default Hermes base gets
its own `$XDG_STATE_HOME/memcue/<hash>` directory, so it does not share a dream clock with a Claude Code/DSH base.

Entries written to `MEMORY.md`/`USER.md` before the provider was switched on are not copied. Add the ones worth
keeping with `memcue add`, or let the agent do it with `memcue_add`.
