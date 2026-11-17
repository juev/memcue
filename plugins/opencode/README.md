# memcue for OpenCode

A plugin for [OpenCode](https://opencode.ai) 2 that gives the coding agent memcue long-term memory. The model
searches and keeps its facts itself through native tools, the memory instruction and the repository's pitfalls reach
it in the system prompt, and `/memcue` switches memory off for a session or starts maintenance.

## Requirements

- OpenCode 2. The plugin uses the v2 plugin API (`@opencode/plugin`); OpenCode 1 does not load v2 plugins and this
  package therefore refuses to load there. Tested against OpenCode 2.2.7.
- memcue 0.36.1 or later. Without a configured binary the plugin uses `memcue` from `PATH` when that is this
  plugin's release or newer; otherwise it installs the matching release into `~/.local/bin/memcue`, checked against
  the release's `checksums.txt`, the same binary the other memcue plugins of the machine use.
  `MEMCUE_AUTO_INSTALL=0` turns the install off.

## Install

```sh
opencode plugin add memcue-opencode
```

Or add it to `opencode.json(c)` yourself, with the options below:

```json
{
  "plugins": [{ "package": "memcue-opencode", "options": { "facts": "~/memory/facts" } }]
}
```

From a checkout, point the `plugins` entry at this directory. Reload OpenCode, then run `/memcue status`.

## Configuration

Every field is optional; an unknown field is reported in the plugin log and memory stays off until it is fixed:

```json
{
  "plugins": [{
    "package": "memcue-opencode",
    "options": {
      "enabled": true,
      "binary": "/usr/local/bin/memcue",
      "facts": "~/memory/facts",
      "stateDir": "~/memory/state",
      "host": "laptop",
      "subagents": { "defaultEnabled": false, "agents": { "worker": true } }
    }
  }]
}
```

- `binary` — a memcue to use as it is, never replaced; an absolute path, `~/...` or a command name.
  `MEMCUE_BIN` does the same.
- `facts`, `stateDir`, `host` — passed to memcue as `MEMCUE_FACTS`, `MEMCUE_STATE_DIR` and `MEMCUE_HOST`.
  Other `MEMCUE_*` variables of OpenCode's environment reach memcue unchanged, so by default OpenCode shares the
  base of the machine's other agents. In server mode (`MEMCUE_SERVER` set) every agent of the machine works on the
  one replica of the server's base, and `facts` does not choose another base.

## What the model gets

- Tools: `memcue_search`, `memcue_add`, `memcue_correct`, `memcue_retire`, and `memcue_dream_prepare` /
  `memcue_dream_apply` for maintenance with the session's own model.
- The shared memory instruction, as a system-prompt part of every request.
- A repository-pitfall snapshot, read once per session (`memcue session-card -pitfalls-json`) and again after a
  compaction. It covers the session's directory; a snapshot without pitfalls adds nothing, and a failed read says so
  instead of reusing an older one.

Each write checks memcue's outcome receipt. A write whose outcome is unknown is reported as such and never retried.

## Commands

| Command | Action |
|---|---|
| `/memcue status` | Show whether memory is on, the role and the binary, without running memcue |
| `/memcue off` | Turn memory off for this session; facts already in the conversation stay |
| `/memcue on` | Turn it back on, if the options allow it |
| `/memcue dream` | Ask the current model to maintain the base |

The command is registered by the plugin's server part; OpenCode lists it with the other commands, so no TUI part is
needed. The status notice is a transcript message, not a model turn.

## Subagents

Memory in a child session (a subagent or a fork) is off by default, because the child repeats the parent's text and
would write the same facts twice. A role gets memory only when `subagents.agents` names the agent it runs and that
entry is `true`; `subagents.defaultEnabled` allows every agent that is not listed.

## Log

OpenCode does not keep a plugin's console output, so the plugin writes its diagnostics, including a configuration
error or an older OpenCode version, to one file:

```
$XDG_STATE_HOME/opencode/memcue/plugin.log
```

A missing plugin entirely is reported by OpenCode itself: its log holds `failed to load plugin`, and a session
continues without memory.

## Verification limits

- The tests run against a mock OpenCode context and a stand-in memcue CLI, so they need neither a model nor an
  OpenCode binary. A live check needs a real session with a configured model; it is not part of CI.
- Reading OpenCode's own transcripts (`memcue sessions`) reads `opencode.db` in read-only mode, and only the text
  parts. The layout is not documented, so the reader checks the shape before trusting it and falls back to the v1
  tables for sessions the migration left behind.
