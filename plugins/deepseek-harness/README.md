# memcue for DeepSeek Harness

Long-term memory for [DeepSeek Harness](https://github.com/deepseek-ai/deepseek-harness) (`dsh`) on top of a memcue
fact base:

- **No facts come with a prompt** (#361). The system-prompt section tells the model to search the base with
  `memcue_search` before it answers a question that may depend on earlier sessions, several times with synonyms, the
  system's name and both languages if the first try finds nothing.
- **At the first prompt and after every compaction** `memcue session-card` supplies the card of global rules and
  repository decisions as a user-role notice. The card is a view of the base, not a copy. A failed card is not
  treated as delivered, so the next step retries it (with one report per session), and the step still runs.
- **The model keeps its memory itself** (#494). The system-prompt section and `memcue_add` tell it to store a rule or
  a correction of the user, a decision the code does not show, a pitfall, a host, a service or a pointer when it
  learns it, to search first and correct a stored fact instead of adding a copy. Nothing is extracted from the
  session afterwards. `memcue_add` takes a `source`: a `rule` or `decision` is the user's words by default, the only
  source that reaches the session card; `tool` is what a tool's output showed, `agent` anything else, and memcue
  refuses installation state from either (#475).
- **A system-prompt section** says the memory is memcue, when to search and write it and which tools fix it.
- **Four tools**: `memcue_search` (facts by words, with their ids; an optional `limit`), `memcue_add` (one new fact),
  `memcue_correct` (a new version of a stored fact, with optional `keywords` and `expires`) and `memcue_retire` (stop
  finding a fact; the history keeps it). `memcue_search` runs `memcue search` with the session's working directory
  and id, so facts of this host and of the session's repository win ties and `MEMCUE_SEARCH_LOG=1` can name the
  session.

The binary owns the base. This plugin is only the seam between it and the harness; the [harness support matrix](../../README.md#harness-support) lists what differs from Claude Code and
Hermes.

## Candidates

A fact written by `memcue_add` is indexed at once, and `memcue_correct` writes a new version of it. Nothing waits
for approval: `memcue add -candidate`, `memcue candidates` and `memcue approve` are removed (#514). A candidate or provisional
fact an older memcue left in the base is live too, unless it is retired or expired (see
[Candidates](../../README.md#candidates)).

## Supported dsh

The plugin asks `memcue version -json` when it loads. Without a configured `binary` or `MEMCUE_BIN`, a missing binary
or one older than the plugin's release is replaced by that release, installed into
`$XDG_DATA_HOME/memcue/<version>/` (`MEMCUE_AUTO_INSTALL=0` turns that off): an older release may not open the base
the new one writes. A binary older than 0.4.0 that stays, a configured one or a failed install, turns the session card
off with one warning (search keeps working). A build from source reports a suffixed version and counts
as the current tree.

Tested with `dsh 0.2.0-rc.2`. Both the repository bundle and the standalone package declare
`>=0.2.0-rc.2 <0.3.0` peer ranges for `@deepseek-ai/dsh-llm` and `@deepseek-ai/dsh-tools`, so dsh's compatibility
check refuses a different minor instead of loading a plugin whose session or event shape it no longer recognises.

## Install

The repository root is a dsh bundle wrapper: its `package.json` declares `dsh.bundle.patch`, pointing at this
directory's `cordis.patch.yml`. The plugin directory is also its own package, for npm publication and local
development. The entry is plain ESM (`src/index.mjs`) on purpose: dsh installs a bundle under `node_modules`, where
Node refuses to strip TypeScript types, so a `.ts` entry cannot be loaded from an installed package.

1. Optional: put the `memcue` binary where the dsh process can run it and note its absolute path. Without a
   configured `binary` and without a current `memcue` on `PATH`, the plugin installs the binary of its own release
   into `${XDG_DATA_HOME:-~/.local/share}/memcue/<version>/`, the directory the Claude Code plugin uses
   (`MEMCUE_AUTO_INSTALL=0` turns that off; see [Binary from the plugin](../../README.md#binary-from-the-plugin)).

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
   switches the bundle on. dsh 0.2.0-rc.2 does not update installed plugins: to move to a new release, uninstall the
   plugin and install it again ([Releasing](../../docs/releasing.md#after-the-release)).

3. Configure the row by id in the profile's own patch layer, `$DSH_HOME/profiles/<name>/cordis.patch.yml`:

   ```yaml
   - id: memcue
     config:
       binary: /Users/you/.local/bin/memcue
       facts: /Users/you/.claude/memory/facts.db
   ```

   `binary` is worth setting explicitly: an application launched from Finder may not have `~/.local/bin` on `PATH`.
   `facts` may be omitted when the base is memcue's default (`~/.claude/memory/facts.db`).

## Model of `add`

`memcue add` asks a model for a fact's search queries in one short call when `MEMCUE_API_URL` is set; without it the
dream fills them later. `model`, `apiUrl` and `apiKey` set that call: the plugin forwards them to every memcue child
process as `MEMCUE_MODEL`, `MEMCUE_API_URL` and `MEMCUE_API_KEY`, and a configured value wins over the ambient one.
Left out, nothing is called. `apiKey` is a secret in a configuration file: keep that file out of version control, or
keep `MEMCUE_API_KEY` in the environment that launches dsh instead.

## Configuration

All fields are optional; `MEMCUE_BIN` and `MEMCUE_FACTS` from the environment are still the ambient fallbacks.
A configured field wins over the environment.

- `binary` — path or name of the memcue executable (default `memcue`).
- `facts` — the fact base; set as `MEMCUE_FACTS` for every child process (default: the binary's own default). A
  leading `~/` is expanded; a relative path is rejected at load, so the value cannot name a different file for each
  host.
- `searchLimit` — default for `memcue_search` (default `5`).
- `model` — model of the search queries `add` asks for; forwarded as `MEMCUE_MODEL` (default: memcue's own).
- `apiUrl` — OpenAI-compatible base URL; forwarded as `MEMCUE_API_URL` (default: unset, so `add` calls no model).
- `apiKey` — key for `apiUrl`; forwarded as `MEMCUE_API_KEY` (default: unset, so the ambient value or no key is used).

Bad values are rejected at load time with the field name, so a typo does not silently fall back to a default. The
`extract`, `minNewChars`, `stateDir` and `maxToolResultChars` fields of the extraction are gone (#494); a profile that
still sets them loads, and the fields are ignored.

## Testing

```sh
node --test plugins/deepseek-harness/test/*.test.mjs
```

The suite mounts the plugin against a plain object context and a stub binary, so it needs no model, no base and no
running harness — only a checkout where `@deepseek-ai/dsh-llm` and `@deepseek-ai/dsh-tools` resolve. It also checks
the bundle metadata and that the bundle patch points at the loadable ESM entry. `make test` runs it together with the
Go and Hermes suites when those packages are installed, and skips it with the install command otherwise.

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
- The session card arrives as a durable message in the session log, so it replays with the session and survives a
  harness restart.
- The Claude Code hooks bridge in dsh runs `SessionStart` but ignores `PreCompact` and `SessionEnd`; this plugin uses
  dsh's own events, so the card comes again after a compaction.
