# memcue for DeepSeek Harness

Long-term memory for [DeepSeek Harness](https://github.com/deepseek-ai/deepseek-harness) (`dsh`) on top of a memcue
fact base:

- **Before each step** the prompt goes to `memcue hook`; at most two matching facts come back and are appended as a
  user-role message with source `memcue` and form `recall`. memcue itself keeps a fact to one delivery per session.
- **When a turn ends** the messages it added go to `memcue extract-messages` in the background, once
  `minNewChars` of new text has gathered. The read offset per session lives in `<stateDir>/<session>.json`, so a
  message is sent once even though a turn boundary repeats.
- **A system-prompt section** says the memory is memcue and names the two tools; the built-in memory files are not
  written.
- **Two tools**: `memcue_search` (facts by words, `search -json`) and `memcue_add` (one fact, `add`).

The binary owns the base, the extraction prompt and the model it uses. This plugin is only the seam between it and
the harness, so everything that works for Claude Code and Hermes works here on the same file.

## Install

1. Put the `memcue` binary where the dsh process can run it and note its absolute path.

2. Add the plugin to a profile patch — an absolute path to `src/index.ts` is what the loader takes:

   ```yaml
   - insert:
       - id: memcue
         name: '/absolute/path/to/memcue/plugins/deepseek-harness/src/index.ts'
         config:
           binary: '/usr/local/bin/memcue'
           facts: '/home/you/.local/share/memcue/facts.db'
   ```

   Write it into `$DSH_HOME/profiles/<name>/cordis.patch.yml` for one profile or `$DSH_HOME/cordis.patch.yml` for
   all of them; `--patch ./extra.yml` applies one overlay to a single run. `cordis.patch.yml` in this directory is a
   template.

3. Extraction needs a model. memcue calls an OpenAI-compatible endpoint when one is configured, which is the cheap
   route inside dsh — export before starting the harness:

   ```sh
   export MEMCUE_API_URL=https://api.deepseek.com
   export MEMCUE_MODEL=deepseek-flash
   export MEMCUE_API_KEY=...            # read by the binary, never by this plugin
   ```

   Without it memcue falls back to `claude -p`, which wants a Claude Code install.

## Configuration

All fields are optional; `MEMCUE_BIN` and `MEMCUE_FACTS` from the environment are the fallbacks.

- `binary` — path or name of the memcue executable (default `memcue`).
- `facts` — the fact base; set as `MEMCUE_FACTS` for every child process (default: the binary's own default).
- `extract` — `false` turns session extraction off (default `true`).
- `minNewChars` — new text a turn must add before extraction runs (default `2000`).
- `stateDir` — where this plugin keeps per-session read offsets (default `$XDG_STATE_HOME/memcue/dsh`).
- `searchLimit` — default for `memcue_search` (default `5`).

## Testing

```sh
node --test plugins/deepseek-harness/test/
```

The suite mounts the plugin against a plain object context and a stub binary, so it needs no model, no base and no
running harness — only a checkout where `@deepseek-ai/dsh-llm` and `@deepseek-ai/dsh-tools` resolve.

## What differs from the Hermes plugin

- Hermes' `MEMORY.md` mirror and its four memory tools have no counterpart here: dsh has no built-in memory files, so
  there is nothing to mirror, and `memcue_add` covers the write path.
- Recall arrives as a durable message in the session log rather than a `<memory-context>` block, so it replays with
  the session and survives a harness restart.
- The Claude Code hooks bridge in dsh cannot do this: it runs `SessionStart` and `UserPromptSubmit`, but it ignores
  `PreCompact`/`SessionEnd` and always sends an empty `transcript_path`, so extraction never starts and the base
  never grows.
