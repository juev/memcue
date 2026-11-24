# memcue for Kimi Code

Pull-mode long-term memory over the memcue CLI. The agent searches and writes facts itself; the plugin system prompt
delivers the standing policy, and hooks deliver maintenance hints and repository pitfall snapshots. Nothing is extracted
from the transcript afterwards, and no background model is started.

This is a native Kimi Code plugin (`kimi.plugin.json`), not an MCP server or a converted Claude Code plugin.

## Requirements

- Kimi Code with plugins and classic hooks. The local acceptance target is 2.7.2.
- Python 3.9+ on `PATH` (`python3`).
- A memcue binary. The adapter never downloads one: it uses `~/.local/bin/memcue` unless `binary` is configured.
  Install a release binary yourself (see the repository README).

## Install

From a checkout containing this integration, install the plugin in Kimi Code:

```text
/plugins install /absolute/path/to/memcue/plugins/kimi-code
```

Create `${KIMI_CODE_HOME:-$HOME/.kimi-code}/memcue.json`, or point `MEMCUE_KIMI_CONFIG` at an absolute path:

```json
{
  "host": "my-machine"
}
```

Run `/reload` or start a new session. The plugin is installed per user and applies to all projects. Local installations
are copied to `$KIMI_CODE_HOME/plugins/managed/memcue-kimi/`; reinstall after changing the source directory.

`host` is required: an explicit nonempty name without whitespace, used for fact scope. The other keys are optional:

- `binary`: absolute path to the memcue executable (default `~/.local/bin/memcue`).
- `facts`: absolute path to the fact base (default memcue's shared base for the machine).
- `state_dir`: absolute path to memcue's state directory (default `$XDG_STATE_HOME/memcue`).
- `server`: server name for server mode, mapped to `MEMCUE_SERVER` on every call.
- `tailscale_control_url`: tailnet control URL, mapped to `MEMCUE_TAILSCALE_CONTROL_URL` on every call.
- `dream_every`: a Go duration such as `24h` for the dream schedule.
- `background_dream`: must be `false` (the default). This adapter uses model-driven dream only.

A given path must be absolute; an empty or relative one is an error, never the default. Explicit inherited remote
settings such as `MEMCUE_POSTGRES_DSN`, `MEMCUE_SERVER` and the other documented `MEMCUE_TAILSCALE_*` transport values
remain supported. The JSON `server` and `tailscale_control_url` values win over inherited ones, so hooks and later shell
calls select the same server. Other inherited `MEMCUE_*` settings are removed, including endpoint credentials and
background dream configuration.

For the author's server-mode setup, the configuration is:

```json
{
  "host": "denis-evsyukov",
  "server": "memcue-server",
  "tailscale_control_url": "https://scale.evsyukov.org"
}
```

## What the plugin delivers

- **System prompt:** the pull-mode policy and the configured launcher in the managed plugin copy. Plugin system-prompt
  contributions participate in prompt rebuilds, including compaction.
- **Session start:** the `memcue` Skill is loaded into the main agent. Invoke it explicitly as
  `/skill:memcue-kimi:memcue`; maintenance uses `/skill:memcue-kimi:memcue-dream`.
- **`SessionStart` (`startup|resume`) and `PostCompact` (`manual|auto`):** maintenance hints from
  `memcue session-card -hints-only`, with dream instructions rewritten for the configured launcher. These events also
  reset the repository-pitfall snapshot so the next prompt receives it again.
- **`UserPromptSubmit` and `PreToolUse` (`Bash`):** the repository pitfall snapshot for the current working directory,
  sent again only when it changes or after a lifecycle event. `PreToolUse` honours the Bash tool's `cwd` parameter.
  Snapshots are deduplicated per session in `kimi-pitfall-snapshots.json` in the shared state directory.

Hook context is printed as plain stdout. Invalid input, missing binaries, malformed responses and command errors produce
a bounded diagnostic on stderr and exit zero, without blocking Kimi Code. Ordinary launcher failures exit nonzero. A
mutation whose response is lost is reported as UNKNOWN OUTCOME; inspect persisted state before retrying, never replay
automatically.

## Commands

Append arguments to the configured launcher printed in the system prompt:

```text
search -json -in all "<words>"
add -kind rule -scope global -source user -- "<fact>"
correct -expected-version VERSION ID "<corrected fact>"
retire -expected-version VERSION ID
reopen ID
dream -prepare
dream -apply /absolute/path/to/answer.json
```

`dream` accepts only `-prepare [-all | -since DATE]`, `-list`, or `-apply FILE`. The working model answers the packet;
no Claude executable, API credentials or second model is needed. In server mode the server dreams on its own.

The adapter does not expose `memcue sessions`: native history search does not read Kimi Code transcripts yet. This does
not affect fact search, writes or dream.

## Hooks disabled

Render the standing policy with the launcher for the source checkout:

```bash
python3 -I /absolute/path/to/memcue/plugins/kimi-code/scripts/memcue.py \
  --config /absolute/path/to/memcue.json instructions
```

The command prints instructions and never edits files. Without the plugin there are no automatic maintenance hints or
pitfall snapshots.

## Tests

```bash
make test-kimi   # synthetic adapter + real-CLI suites; requires bin/memcue, never a live base
```

The suites cover Kimi payloads, plain-text hook output, lifecycle and dedup, subprocess environment, fresh-process CRUD,
history and provenance, repository scopes, dream prepare/apply, timeout and an installed copy without repository files.
They do not measure model compliance or recall quality. Real Kimi runtime checks and their limits are recorded in the
integration PR.
