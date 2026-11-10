# memcue for Codex CLI

Codex searches and writes the existing memcue fact base through shell commands. A reviewed SessionStart hook supplies
standing instructions and maintenance hints. UserPromptSubmit and PreToolUse (Bash) hooks deliver the repository-pitfall
snapshot of the target cwd; a session receives it again only when it changes, or after startup, resume, clear or
compaction. The hooks do not search facts by the task or inject a rules card. The working model
chooses searches and writes; these instructions do not establish model compliance or recall quality.

This is a local CLI integration. It needs Python 3.9+, a memcue binary from 0.30.0 or newer, and Codex CLI 0.167.0.
No MCP server, Claude installation, memory service, API key or second model is needed for memcue search/write or dream
prepare/apply. Codex itself needs its normal authenticated model connection for an agent turn.

Validation platform: Linux x86_64, Python 3.13.5, Go 1.27.0, memcue 0.30.0 built from this checkout, Codex 0.167.0.
Package installation, native hook review and CLI tests are verified. A local deterministic Responses fixture also checks
lifecycle delivery; it is not a real model. The owner will verify authenticated Codex CRUD, dream, lifecycle and sandbox
behavior locally after merge. These checks remain unverified; independent review, regression tests and CI are still
required before merge. Keep #670 open pending the owner's result. The installed 0.166.0 was
only discovered, not validated. macOS, Linux arm64, Windows, remote sessions and cloud use have not been verified.

## Choose the base and configuration

Install/build memcue by the repository's normal instructions. Keep this plugin in a stable local directory, or use
the local marketplace below. It never downloads a binary. Create a dedicated JSON configuration with absolute paths:

```json
{
  "binary": "/absolute/path/to/memcue",
  "facts": "/absolute/path/to/codex-memory/facts",
  "state_dir": "/absolute/path/to/codex-memory/state",
  "host": "my-workstation",
  "background_dream": false
}
```

Put it at `${CODEX_HOME:-$HOME/.codex}/memcue.json`, or set `MEMCUE_CODEX_CONFIG` to its absolute path in the environment
that starts Codex. The directory must already exist. Protect it according to your local privacy requirements; do not
check personal paths or memory into a project. `dream_every` is an optional Go duration such as `24h`; the CLI default
is one day. Other inherited `MEMCUE_*` variables are deliberately removed for child commands. This includes endpoint,
API credentials, background dream, log overrides, number-term settings and event TTL overrides. Event TTL therefore
uses the CLI default of seven days unless an add explicitly passes `-expires`.

`facts` without a `.db` suffix selects a new text history directory; `*.db` follows the native legacy SQLite rules,
including a sibling directory after a migration. No base defaults to the Claude directory. To share an existing base,
explicitly set `facts` to that base and choose its corresponding state directory; back it up first. This setup does
not migrate or rewrite the base. Native CLI opens may update indexes/cache as usual; inspect compatibility before
sharing with older binaries. State holds the text cache, the dream schedule and a digest of the last pitfall snapshot each session received; hooks and later calls must use this same file.

The hook prints the exact configured launcher command into the model's instructions. Every skill call uses that
launcher with `--config /absolute/path/to/memcue.json`; it reloads configuration each time. A hook child's exported
environment does **not** reach later model shell commands. Do not substitute bare `memcue` or depend on the hook to
change PATH. Explicit `--config` wins over `MEMCUE_CODEX_CONFIG`, which wins over the default location.

Server mode: the launcher passes `MEMCUE_SERVER` and the `MEMCUE_TAILSCALE_*` settings to memcue. With them,
memcue works on this machine's replica of the server and ignores the configured `facts`; the dream runs on the
server. See [server mode](../../docs/remote-backends.md#server-mode-a-memcue-server-on-the-tailnet).

## Install the local plugin

From a checkout containing this integration, with your usual Codex environment:

```bash
codex plugin marketplace add /absolute/path/to/memcue-checkout
codex plugin add memcue@memcue-codex
```

The repository's `.agents/plugins/marketplace.json` points only at `plugins/codex`. Codex installs a local copy under
its plugin cache and adds its marketplace/plugin configuration. It does not replace AGENTS.md. If maintaining your
own marketplace instead, add this plugin's entry to the existing `plugins` array; preserve the other entries.

Restart Codex. Open `/hooks`, inspect the memcue SessionStart command and source, and review/trust that definition.
Codex warns that trusted hooks can run outside the tool sandbox. Review the script and its configured base before
trusting it; later shell/skill calls keep their tool sandbox. Installation/enabling does not trust hooks. Changed definitions require review again. The intended matcher covers
`startup`, `resume`, `clear`, `compact` and `fork`, as defined by Codex 0.167.0's release schemas. A trusted project
configuration layer and trusted hooks are separate requirements. Never bypass trust just to install memory.

If plugin hooks are unavailable in your client, keep the plugin folder in a stable directory and add the equivalent
handler to your existing Codex `config.toml`, preserving existing handlers and settings:

```toml
[[hooks.SessionStart]]
matcher = "startup|resume|clear|compact|fork"

[[hooks.SessionStart.hooks]]
type = "command"
command = 'python3 -I "/absolute/path/to/codex-plugin/scripts/memcue.py" hook'
timeout = 30
additionalContextLimit = 5000
```

Review it through `/hooks`. Choose either plugin delivery or this standalone handler, so you do not deliver the note
twice. Hooks disabled with `[features] hooks = false`, untrusted or changed hooks, and unsupported clients need the fallback.

## Hooks-disabled / standalone fallback

Render the concise snippet with your stable plugin path and configuration:

```bash
python3 -I /absolute/path/to/codex-plugin/scripts/memcue.py \
  --config /absolute/path/to/memcue.json instructions
```

This command prints instructions; it never edits files. Append its output to the appropriate existing AGENTS.md after
review. Preserve the rest of that file and add the marked block only once. For updates, replace only the text between
`<!-- memcue: begin -->` and `<!-- memcue: end -->`. Keep the explicit configuration path in the rendered command.

For standalone skill discovery, link the two `skills/` subdirectories into `$HOME/.agents/skills/`, or the project's
`.agents/skills/`. Create links only when those destination names are unused; do not replace another installed skill.
The fallback also points directly to both SKILL.md files. Hooks-disabled fallback has standing instructions, but no
automatic maintenance hints; invoke the dream skill explicitly when needed.

## Use and sandbox access

Append native arguments to the configured launcher:

```bash
python3 -I /absolute/path/to/codex-plugin/scripts/memcue.py --config /absolute/path/to/memcue.json \
  search -json -kind rule,decision "orchard review"
```

The session cwd determines repository origin and scope, including nested directories; it is never the plugin cache
folder. The hook takes cwd from its input, and later commands use their process cwd. Native non-Git behavior is kept:
a Go module or a non-temporary directory can identify a project; temporary directories have no project. Search defaults
to project/host/global when a project exists, and to all off-shelf facts otherwise. Explicit scope filters retain the
CLI's meaning, including project origins. Research shelves always require explicit lookup. Search JSON is an object
with `facts` and optional `hints`; it does not include source/kind fields. Text history JSONL preserves all versions and
metadata; native `dump FILE` is a human-readable SQLite history report, not a search JSON response.

Search can create/update a base/cache; state writes also need permission. For a base outside the workspace, review the
paths and use Codex's scoped directory approval or `--add-dir /absolute/base-parent --add-dir /absolute/state-dir` when
starting Codex with a workspace-write sandbox. The binary/plugin must be readable/executable too. A denied command
remains a visible error; the adapter does not change approval or sandbox settings and requires no blanket full access.

The hook accepts at most 64 KiB of JSON input and returns `SessionStart` additional context. Invalid input, missing or
incompatible binary, malformed maintenance output and command errors produce a bounded `systemMessage` plus stderr,
exit zero and no blocking decision. Ordinary command failures exit nonzero. Hook version and maintenance children time out after 5 and 10 seconds, leaving room within the handler
limit for diagnostics. Ordinary CLI children time out after 15 seconds (dream prepare/apply: 60 seconds), with a 4 MiB output read limit. A timeout/failure is not evidence a write
was rolled back: inspect persisted history before retrying. `instructions` only renders text and invokes no binary.

## Dream and English-first memory

Use the memcue-dream skill at a natural pause. Prepare the packet, have this session's working model write its answer,
and apply it through the same launcher. An answer whose packet ID is stale is rejected; prepare again and reconsider.
No credentials are needed for those CLI mechanics. Facts changed since preparation can be refused individually.
The first maintenance check initializes a schedule and marks existing facts seen; it does not make an old base due
immediately. With hooks disabled, or if startup ran before the base existed, no schedule may exist yet. If
`dream -prepare` reports "no dream yet on this machine", append `dream -prepare -all` to the configured launcher to
review all active facts, or `dream -prepare -since DATE` for a chosen starting date (`YYYY-MM-DD` or RFC 3339).
Choose the range to match the requested maintenance. No schedule-file edits or direct native CLI call are needed.
Preparation persists the native packet for a later apply; it starts no model. Native due timing and packet bounds
remain unchanged. After applying the first answer, use ordinary `dream -prepare` for later packets.

The launcher always sets `MEMCUE_DREAM_BACKGROUND=0` and removes inherited API configuration, including for add/correct,
so an installed Claude binary cannot start a surprise model. `background_dream: true` is rejected. The adapter permits
only `dream -prepare`, `dream -prepare -all`, `dream -prepare -since DATE`, `dream -list` and `dream -apply FILE`.
Use these argument orders; duplicate flags, mixed `-all`/`-since`, extra arguments and runner flags are rejected. Native validation checks
the date/time value. The adapter has no autonomous runner. Users who separately choose
native endpoint/background execution must configure and invoke the native CLI explicitly with their selected
MEMCUE_FACTS/state/host and credentials. That opt-in path is outside this adapter.

[#669](https://github.com/juev/memcue/issues/669) landed in [#671](https://github.com/juev/memcue/pull/671).
Shared instructions now use English for initial queries and new/corrected fact content, preserving exact text and
legacy-language fallback. Native dream packets and maintenance hints no longer request translations or count
translation-only debt. Existing fact content remains as stored. Auxiliary fields are removed during upgrade. Never correct a fact
solely to change its language. No query expansion or translation task remains. Follow the tasks and schema printed by the installed binary.

## Update and remove

Keep a backup of configuration and any manually maintained instructions before an update. Update only this plugin's
files/source and refresh the local plugin with `codex plugin add memcue@memcue-codex`; restart Codex and review any
changed hooks. Keep `memcue.json`, base, state and other configuration entries. If a fallback contains a previous cache
path, render it again and replace only its marked block. For a standalone folder, copy a new reviewed copy to a new
path, update only your handler/skill links/marked snippet, then remove the old copy when no session uses it.

Use `codex plugin remove memcue@memcue-codex` to uninstall the plugin. Remove only the memcue fallback block, standalone
hook handler and links you added. Keep the base/history and state unless you separately intend to delete memory.
If no longer needed, remove this marketplace using Codex's marketplace command; never replace the entire config file.

## Verification

Build the CLI and run the Python suites with `MEMCUE_BIN` pointing to that build:

```bash
go build -o bin/memcue ./cmd/memcue
MEMCUE_BIN="$PWD/bin/memcue" python3 -I -m unittest discover -s plugins/codex/tests -p 'test_*.py'
python3 -I plugins/test_cross_harness.py
```

The suites exercise the delivered hook policy, launcher/config boundaries, installed copies, fresh-process CRUD,
persisted provenance/history, nested Git and non-Git cwd, global/host/repository scopes, shelf exclusion/lookup,
expiry, fresh first-dream initialization with hooks disabled or startup before a base exists, native date validation,
allowed/rejected dream arguments, model-free prepare/apply, stale-packet refusal, and bounded failures. They do not measure recall quality
or prove real model behavior. Run with synthetic paths and sanitized inherited MEMCUE_* variables; never use a live base.

For the owner's postmerge local acceptance, use a synthetic project and Codex home with the owner's model connection. Review
hooks in `/hooks`; observe delivered context on startup/resume/clear/compaction, then repeat CRUD across sessions and
answer a dream packet with the real working model. Confirm stored history/provenance independently. Repeat with
untrusted and disabled hooks plus the fallback, and with denied directory writes. Do not count a successful agent turn
as a passing test suite. Record exact versions, session IDs, events observed, checks run and any unverified events.

Contracts: [Codex hooks](https://developers.openai.com/codex/hooks),
[skills](https://developers.openai.com/codex/skills), [AGENTS.md](https://developers.openai.com/codex/guides/agents-md),
[plugin packaging](https://developers.openai.com/plugins/build/plugins).

The pinned release contract is Codex 0.167.0, commit `086ccc833bc9cf01024ad2dd6af7dbd421b6bf9a`: see its
[SessionStart input](https://github.com/openai/codex/blob/086ccc833bc9cf01024ad2dd6af7dbd421b6bf9a/codex-rs/hooks/schema/generated/session-start.command.input.schema.json) and
[output schema](https://github.com/openai/codex/blob/086ccc833bc9cf01024ad2dd6af7dbd421b6bf9a/codex-rs/hooks/schema/generated/session-start.command.output.schema.json).
