# memcue for DeepSeek Harness

Long-term memory for [DeepSeek Harness](https://github.com/deepseek-ai/deepseek-harness) (`dsh`) on top of a memcue
fact base:

- **Before each step** the prompt goes to `memcue hook`; at most two matching facts come back and are appended as a
  user-role message with source `memcue` and form `recall`. memcue itself keeps a fact to one delivery per session.
- **At the first prompt and after every compaction** `memcue session-card` supplies the card of global rules and
  repository decisions as a user-role notice. The card is a view of the base, not a copy; after a compaction it also
  resets the session's delivered facts, so a fact dropped with the old context may come again.
- **When a turn ends** the messages it added go to `memcue extract-messages` in the background, once
  `minNewChars` of new text has gathered. The checkpoint per session lives in `<stateDir>/<session>.json`: it holds
  the last acknowledged event and a pending batch, written atomically before the model call. A failed extraction
  keeps the batch and retries it on the next turn; a successful one advances the checkpoint, so a turn boundary
  does not send the same text twice. Extraction reads only direct human messages: compaction summaries, cron and
  goal notices, hook contexts and subagent sessions are left out, and a forked session starts at its inherited
  prefix instead of re-extracting the parent's history. Tool calls travel with the assistant message and their
  results follow as `RESULT`, the same pair Claude Code transcripts carry. When the session is disposed, the
  remaining tail is sent even if it is below `minNewChars`: there will be no next turn to collect it.
- **A system-prompt section** says the memory is memcue and names the fact tools; the built-in memory files are not
  written.
- **Four tools**: `memcue_search` (facts by words, with their ids), `memcue_add` (one new fact), `memcue_correct`
  (a new version of a stored fact) and `memcue_retire` (stop recalling a fact; the history keeps it).

The binary owns the base, the extraction prompt and the model it uses. This plugin is only the seam between it and
the harness, so everything that works for Claude Code and Hermes works here on the same file.

## Install

The repository root is a dsh bundle wrapper: its `package.json` declares `dsh.bundle.patch`, pointing at this
directory's `cordis.patch.yml`. The plugin directory is also its own package, for npm publication and local
development. The entry is plain ESM (`src/index.mjs`) on purpose: dsh installs a bundle under `node_modules`, where
Node refuses to strip TypeScript types, so a `.ts` entry cannot be loaded from an installed package.

1. Put the `memcue` binary where the dsh process can run it and note its absolute path.

2. Install the bundle from the repository:

   - **Desktop / Web:** open **Plugins** in the sidebar, choose **Add plugin → Install a third-party plugin**, and
     enter `github:juev/memcue`.
   - **CLI:** `dsh plugin --profile web add github:juev/memcue`.

   No `path:` parameter and no local directory are needed. From a local checkout, `dsh plugin add /path/to/memcue`
   installs the same root package.

   The plugin directory is also a standalone package, published as
   [`memcue-dsh`](https://www.npmjs.com/package/memcue-dsh). `dsh plugin add memcue-dsh` installs it without the
   repository; a local checkout can use `dsh plugin add /path/to/memcue/plugins/deepseek-harness`.

   The plugin manager adds the package to the profile's `dsh.profile.bundles`; the install dialog's **Enable now**
   switches the bundle on. A repository install is a Git dependency, so a `git pull` reaches the next start without
   reinstalling.

3. Configure the row by id in the profile's own patch layer, `$DSH_HOME/profiles/<name>/cordis.patch.yml`:

   ```yaml
   - id: memcue
     config:
       binary: /Users/you/.local/bin/memcue
       facts: /Users/you/.claude/memory/facts.db
       model: deepseek-flash
       apiUrl: https://api.deepseek.com
       apiKey: sk-...
   ```

   `binary` is worth setting explicitly: an application launched from Finder may not have `~/.local/bin` on `PATH`.
   `facts` may be omitted when the base is memcue's default (`~/.claude/memory/facts.db`).

## Choosing the extraction model

`model` is the model memcue uses for the extraction. No `MEMCUE_*` environment variable needs to be exported: the
plugin forwards the configured values to every memcue child process as `MEMCUE_MODEL`, `MEMCUE_API_URL` and
`MEMCUE_API_KEY`.

- **With `apiUrl`** memcue calls an OpenAI-compatible `/chat/completions` endpoint: `model: deepseek-flash`,
  `apiUrl: https://api.deepseek.com`, `apiKey: sk-...`.
- **Without `apiUrl`** memcue falls back to `claude -p --model <model>`: `model: claude-opus-5-5` uses the Claude
  Code login already on the machine and needs no API key.

A field left out falls back to the process environment, then to memcue's own default. A configured value wins over
the ambient one, so a stale `MEMCUE_MODEL` in the launching environment does not override the profile patch. With
neither set, memcue's default `claude-opus-5-5` runs through `claude -p`.

`apiKey` is stored in the profile patch under the private `$DSH_HOME` directory, but it is still a secret in a
configuration file: keep that file out of version control. To avoid storing it, omit `apiKey` and keep
`MEMCUE_API_KEY` in the environment that launches dsh, or use the `claude -p` route with no key at all.

## Configuration

All fields are optional; `MEMCUE_BIN` and `MEMCUE_FACTS` from the environment are still the ambient fallbacks.
A configured field wins over the environment.

- `binary` — path or name of the memcue executable (default `memcue`).
- `facts` — the fact base; set as `MEMCUE_FACTS` for every child process (default: the binary's own default).
- `extract` — `false` turns session extraction off (default `true`).
- `minNewChars` — new text a turn must add before extraction runs (default `2000`).
- `stateDir` — where this plugin keeps per-session read offsets (default `$XDG_STATE_HOME/memcue/dsh`).
- `searchLimit` — default for `memcue_search` (default `5`).
- `maxToolResultChars` — how much of one tool result goes to extraction (default `1500`). Tool output is kept on
  purpose: a fact sourced from a tool can only quote its evidence from a `RESULT` block, and memcue's write policy
  drops such a fact when the quote is missing.
- `model` — extraction model; forwarded as `MEMCUE_MODEL` (default: memcue's own, `claude-opus-5-5`).
- `apiUrl` — OpenAI-compatible base URL; forwarded as `MEMCUE_API_URL` (default: unset, so `claude -p` runs).
- `apiKey` — key for `apiUrl`; forwarded as `MEMCUE_API_KEY` (default: unset, so the ambient value or no key is used).

Bad values are rejected at load time with the field name, so a typo does not silently fall back to a default.

## Testing

```sh
node --test plugins/deepseek-harness/test/*.test.mjs
```

The suite mounts the plugin against a plain object context and a stub binary, so it needs no model, no base and no
running harness — only a checkout where `@deepseek-ai/dsh-llm` and `@deepseek-ai/dsh-tools` resolve. It also checks
the bundle metadata and that the bundle patch points at the loadable ESM entry.

## Releasing

The standalone package is published as `memcue-dsh`.

CI publishes on a `dsh-v*` tag or by manual dispatch through `.github/workflows/publish-dsh.yml`. It uses npm
Trusted Publishing (OIDC): the workflow has `id-token: write`, no token, and no `NPM_TOKEN` secret. The trusted
publisher is configured for the `juev/memcue` repository and the `publish-dsh.yml` workflow, with direct publish and
stage-publish allowed. Manual dispatch has a `dry_run` input: it packs a throwaway prerelease with `--dry-run` and
uploads nothing.

A maintainer can still publish manually; npm asks for the 2FA code:

```sh
cd plugins/deepseek-harness
npm publish --access public
```

A fork needs its own trusted publisher, or a granular `NPM_TOKEN` on a `NODE_AUTH_TOKEN` environment variable. A
granular token without the 2FA bypass cannot run `npm publish`: use `npm stage publish` and approve the stage with
`npm stage approve <id>`.

## What differs from the Hermes plugin

- Hermes' `MEMORY.md` mirror has no counterpart here: dsh has no built-in memory files, so there is nothing to
  mirror. The write path is `memcue_add`, `memcue_correct` and `memcue_retire`.
- Recall arrives as a durable message in the session log rather than a `<memory-context>` block, so it replays with
  the session and survives a harness restart.
- The Claude Code hooks bridge in dsh cannot do this: it runs `SessionStart` and `UserPromptSubmit`, but it ignores
  `PreCompact`/`SessionEnd` and always sends an empty `transcript_path`, so extraction never starts and the base
  never grows.
