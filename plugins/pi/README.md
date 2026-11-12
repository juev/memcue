# memcue for Pi

A [Pi](https://pi.dev) package that gives the coding agent memcue long-term memory. The model searches and keeps
its facts itself through native tools; the repository's pitfalls and the memory instruction reach it in the system
prompt. Subagents get no memory unless a role is enabled explicitly.

## Requirements

- Pi 1.5 or later (`@earendil-works/pi-coding-agent`). The tests run on Pi 1.5.2.
- Node.js 24 or later.
- macOS or Linux.

The plugin needs memcue 0.36.1 or later. Without a configured binary it uses `memcue` from `PATH` when that is this
plugin's release or newer; otherwise it installs the matching release into `~/.local/bin/memcue`, checked against
the release's `checksums.txt`, the same binary the other memcue plugins of the machine use. `MEMCUE_AUTO_INSTALL=0`
turns the install off.

## Install

```sh
pi install npm:memcue-pi
```

From a checkout: `pi install ./plugins/pi`. Restart Pi or run `/reload`, then check `/memcue status`.

## Configuration

The plugin reads the `memcue` block of Pi's `settings.json` (global or trusted project settings). Every field is
optional:

```json
{
  "memcue": {
    "enabled": true,
    "binary": "/usr/local/bin/memcue",
    "facts": "~/memory/facts",
    "stateDir": "~/memory/state",
    "host": "laptop",
    "subagents": {
      "defaultEnabled": false,
      "agents": { "worker": true, "maintainability-reviewer": false }
    }
  }
}
```

- `binary` — a memcue to use as it is, never replaced; an absolute path, `~/...` or a command name.
  `MEMCUE_BIN` does the same.
- `facts`, `stateDir`, `host` — passed to memcue as `MEMCUE_FACTS`, `MEMCUE_STATE_DIR` and `MEMCUE_HOST`.
  Other `MEMCUE_*` variables of Pi's environment reach memcue unchanged, so by default Pi shares the base of the
  machine's other agents. In server mode (`MEMCUE_SERVER` set) every agent of the machine works on the one replica
  of the server's base, and `facts` does not choose another base.
- An unknown or mistyped field is reported at session start, and memory stays off until it is fixed.

## What the model gets

- Tools: `memcue_search`, `memcue_add`, `memcue_correct`, `memcue_retire`, and `memcue_dream_prepare` /
  `memcue_dream_apply` for maintenance with the session's own model.
- A `memcue` system-prompt section with the instruction every memcue host carries.
- A `memcue_pitfalls` section with the current repository's pitfalls, read once per prompt (`memcue session-card
  -pitfalls-json`). Pi sends a section again only when it changes, so the cached prompt stays; a section without
  pitfalls is left out. The scope follows Pi's working directory.

Each write checks memcue's outcome receipt. A write whose outcome is unknown is reported as such and never retried.

## Commands

| Command | Action |
|---|---|
| `/memcue status` | Show whether memory is on, the role and the binary, without running memcue |
| `/memcue off` | Turn memory off for this session; facts already seen stay in the conversation |
| `/memcue on` | Turn it back on, if the settings allow it |
| `/memcue dream` | Ask the current model to maintain the base |

## Subagents

In a pi-subagents child (`PI_SUBAGENT_CHILD=1`) and in a session with a parent session (a fork, a clone or a
foreground subagent) the main entrypoint gives no memory. A role gets memory only when both hold:

1. `memcue.subagents.agents.<role>` is `true` (or `defaultEnabled` is `true` and the role is not listed).
2. The role's subagent loads the plugin's role entrypoint, for example:

```json
{
  "subagents": {
    "agentOverrides": {
      "worker": { "subagentOnlyExtensions": ["<package path>/children/worker.ts"] }
    }
  }
}
```

The package has `children/worker.ts` and `children/scout.ts`. For another role, create a file next to them:

```ts
import { createMemcueExtension } from "../src/extension.ts";

export default createMemcueExtension({ agent: "my-role" });
```

The role is fixed in code, never taken from a model's argument. For an independent reviewer, keep it `false`
and start it with a fresh context. This isolates the integration only: an agent with a shell can still run memcue
or read the base itself.

## Development

```sh
npm install --no-package-lock
npx tsc --noEmit
MEMCUE_BIN=/absolute/path/to/memcue npm test
```

The tests load the extension with the real Pi SDK and an offline model; with `MEMCUE_BIN` they also run the real
binary on temporary bases. `make test-pi` in the repository root does the same with the built binary.
