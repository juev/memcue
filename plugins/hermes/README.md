# memcue for Hermes Agent

A memory provider for [Hermes Agent](https://github.com/NousResearch/hermes-agent) that keeps long-term memory in a
memcue fact base:

- **Before each turn** the user's message goes to `memcue hook`; at most two matching facts come back, or nothing.
  Hermes puts them into the turn's `<memory-context>` block. Each fact comes once per session, and again after the
  context is compressed, which drops what the session already got. A cron run gets none: it is a background task's
  notice, whose wrapper matches facts by accident.
- **Writes of the built-in memory tool are mirrored.** A new `MEMORY.md` or `USER.md` entry becomes a fact; when the
  agent edits that entry the fact gets a new version, when it removes it the fact is retired. Which fact an entry
  became is kept in `$HERMES_HOME/memcue/written.json`.
- **Tools** `memcue_search`, `memcue_add`, `memcue_correct`, `memcue_retire` let the model find and fix facts.
  Subagents, cron and flush runs get only `memcue_search` and do not write.
- **Session extraction** (optional): the new messages of a session go to `memcue extract-messages` in the
  background, which sends them to an OpenAI-compatible model with the same prompt Claude Code sessions get, and adds
  the facts it finds. A chat of a messaging gateway can stay one session for days, so the messages are not held until
  the session ends: they go once 20 000 characters of new text gather (`extract_threshold`), after 30 minutes without
  a turn (`extract_idle_minutes`), before the context is compressed, when the session ends and when Hermes shuts
  down. Each message is sent once; fingerprints of the sent ones are kept in `$HERMES_HOME/memcue/sessions/`, which
  also survives compression rewriting the message list. Each run is a line in memcue's `extract.log`
  (`$XDG_STATE_HOME/memcue`, by default `~/.local/state/memcue`).

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

4. Optional: turn on session extraction with an OpenAI-compatible endpoint. The key is read from the environment
   variable you name, so it stays where Hermes keeps its other keys:

   ```bash
   hermes config set memory.memcue.extract_url https://api.deepseek.com
   hermes config set memory.memcue.extract_model deepseek-flash
   hermes config set memory.memcue.extract_key_env DEEPSEEK_API_KEY
   ```

5. Restart Hermes (the gateway, if you run one).

Check: `hermes memory status` should list `memcue` as available. Run the plugin's tests with the binary you
installed:

```bash
MEMCUE_BIN="$HERMES_HOME/bin/memcue" python3 "$HERMES_HOME/plugins/memcue/tests/test_provider.py"
```

## The fact base

By default the base is `$HERMES_HOME/memcue/facts.db`, inside Hermes' home, so `hermes backup` covers it. Set
`memory.memcue.facts` (or `MEMCUE_FACTS` in Hermes' environment) to use another file, for example one shared with
Claude Code. A shared base can be synced through git like the Claude Code one: run `memcue install` once in the
repository that holds it.

Entries written to `MEMORY.md`/`USER.md` before the provider was switched on are not copied. Add the ones worth
keeping with `memcue add`, or let the agent do it with `memcue_add`.
