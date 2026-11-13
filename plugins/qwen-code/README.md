# memcue for Qwen Code

Pull-mode long-term memory over the memcue CLI. The agent searches and writes facts itself; a SessionStart hook
delivers the instructions and maintenance hints, and UserPromptSubmit/PreToolUse hooks deliver the repository's
pitfall snapshot. Nothing is extracted from the transcript afterwards, and no background model is started.

This is a native Qwen Code extension (`qwen-extension.json`). It is not installed through the Claude-plugin
converter: that path reads only `modules` (experimental Mods, a QuickJS sandbox without process execution), so the
classic command hooks here would be lost.

## Requirements

- Qwen Code with classic hooks and extensions (verified on 0.27.2).
- Python 3.9+ on `PATH` (`python3`).
- A memcue binary. The adapter never downloads one: it uses `~/.local/bin/memcue` unless `binary` is configured.
  Install a release binary yourself (see the repository README).

## Install

```bash
qwen extensions install /path/to/memcue/plugins/qwen-code --scope user
```

Create the configuration at `~/.qwen/memcue.json` (or point `MEMCUE_QWEN_CONFIG` at an absolute path):

```json
{
  "host": "my-machine"
}
```

`host` is required: an explicit nonempty name without whitespace, used for fact scope. The other keys are optional
and default to the machine's shared base:

- `binary`: absolute path to the memcue executable (default `~/.local/bin/memcue`).
- `facts`: absolute path to the fact base (default memcue's own, shared by every agent of the machine).
- `state_dir`: absolute path to memcue's state directory (default `$XDG_STATE_HOME/memcue`).
- `dream_every`: a Go duration such as `24h` for the dream schedule.
- `background_dream`: must be `false` (the default). This adapter uses model-driven dream only.

A given path must be absolute; an empty or relative one is an error, never the default.

Review the hooks with `/hooks` in a session. The extension installs three: `SessionStart`
(`startup|resume|clear|compact`), `UserPromptSubmit`, and `PreToolUse` (`^run_shell_command$`).

## What the hooks deliver

- **SessionStart**: the launcher names itself, then the binary's full note (search/write policy, the history
  fallback, the research shelf) and the maintenance hints. With `MEMCUE_AGENT=qwen` the built-in-memory hint reads
  `memory.enableManagedAutoMemory` from `~/.qwen/settings.json`. Qwen Code injects this context into the system
  instruction; it is not written to the transcript, so verify delivery by the model's behaviour or the debug log,
  not by searching the transcript.
- **UserPromptSubmit / PreToolUse**: the repository pitfall snapshot for the current working directory, sent again
  only when it changes or after a lifecycle event (deduplicated per session in `qwen-pitfall-snapshots.json`).
  `PreToolUse` honours the shell command's `directory` parameter.

The launcher runs every child with the configured binary/base/state/host, disables background dream and endpoint
calls, and reports a bounded diagnostic without stopping Qwen Code on failure. A mutation whose response is lost is
reported as UNKNOWN OUTCOME; inspect persisted state before retrying, never replay automatically.

## Commands

Append arguments to the configured launcher (the SessionStart note prints it):

```text
search -json -in all "<words>"
add -kind rule -scope global -source user -- "<fact>"
correct -expected-version VERSION ID "<corrected fact>"
retire -expected-version VERSION ID
sessions "<words>" -session <current session id>
dream -prepare
dream -apply /absolute/path/to/answer.json
```

`dream` accepts only `-prepare [-all | -since DATE]`, `-list`, or `-apply FILE`. The working model answers the
packet; no Claude executable, API credentials or second model is needed. See `skills/memcue-dream/SKILL.md`.

## Hooks disabled

Render the fallback instructions and paste the marked block into `~/.qwen/QWEN.md` (or a project `QWEN.md`):

```bash
python3 -I /path/to/memcue/plugins/qwen-code/scripts/memcue.py --config ~/.qwen/memcue.json instructions
```

The block names the launcher and links the skills; no automatic hints or pitfall snapshots are delivered. The first
dream then needs `dream -prepare -all` or `dream -prepare -since DATE`.

## Built-in memory

memcue replaces Qwen Code's built-in memory; keep facts in one place. Turn the built-in memory off with
`"memory": {"enableManagedAutoMemory": false, "enableManagedAutoDream": false}` in `~/.qwen/settings.json`.
While it is on, the SessionStart hint says so once. `memcue migrate -all` and `memcue notes -dir <memory dir>` read
the Qwen memory files (frontmatter topic files, `MEMORY.md` index skipped) alongside Claude Code's.

## Tests

```bash
make test-qwen   # synthetic adapter + real-CLI suites; requires bin/memcue, never a live base
```

## Verification limits

Verified on macOS with Qwen Code 0.27.2 and a checkout build: extension install, hook delivery (SessionStart into
the system instruction, UserPromptSubmit into the transcript), pitfall dedup, CRUD across processes, dream
prepare/apply with a synthetic packet, the history fallback over synthetic Qwen transcripts, and the model-free
adapter suite. A successful agent turn is not a passed test. Not verified here: other Qwen Code versions, Linux,
the npm/marketplace distribution path, and model compliance/recall quality (the note text is the measured Claude
Code note, delivered verbatim; its Qwen-specific delivery has not been re-measured for compliance).
