# memcue

Long-term memory for Claude Code. memcue keeps a base of short, self-contained facts in one SQLite file and works
through hooks:

- **Before each prompt** it looks for facts that share enough rare words with the prompt and gives the agent at
  most two of them, or nothing. Most prompts get nothing; that is intended.
- **When a session is compacted or ends** it sends the new part of the transcript to Claude, which writes down what
  will still matter later: decisions, how systems are set up, confirmed causes, what did not work. Facts are only
  added; nothing is compared or overwritten.
- **When you ask** ("remember…", "that is outdated") the agent adds, corrects or retires a fact through the bundled
  skill.

Search is lexical (SQLite FTS5 with Snowball stemming for English and Russian), runs offline in one binary and
takes about 10 ms per prompt. A fact keeps where and when it was learnt, but facts from every repository are
searched: what you learnt in one project is recalled in another.

The base can live in a git repository and be synced between machines; memcue ships a merge driver for it. The same
base works for [Hermes Agent](plugins/hermes/README.md).

memcue is young: version 0.1 has been measured on one developer's memory and sessions (see [Results](#results)) and
not yet run for long in daily work.

## Requirements

- macOS or Linux.
- Claude Code with the `claude` command on `PATH`: extraction runs `claude -p`, unless an OpenAI-compatible
  endpoint is configured instead (see [Configuration](#configuration)).
- Go 1.27 or later, to build from source.

## Install

1. Install the binary and make sure `memcue` is on `PATH`: download it for your system from
   [Releases](https://github.com/juev/memcue/releases), or build it:

   ```bash
   go install github.com/juev/memcue/cmd/memcue@latest
   ```

2. Install the Claude Code plugin (hooks and skill) from this repository:

   ```text
   /plugin install memcue --marketplace juev/memcue
   ```

3. Turn off Claude Code's built-in memory, so the agent does not keep a second memory in files. Add to
   `~/.claude/settings.json`:

   ```json
   { "autoMemoryEnabled": false }
   ```

   or set `CLAUDE_CODE_DISABLE_AUTO_MEMORY=1`. With the plugin on, each session starts with a short note that tells
   the agent its memory is memcue.

4. Optional: bring over what the built-in memory already holds. Each note goes through the model once; a note
   already in the base is skipped, so the command can be repeated.

   ```bash
   memcue migrate -list -all   # what would be migrated
   memcue migrate -all         # migrate; -j sets how many notes run at once (default 4)
   ```

5. Optional: if the base is in a git repository (for example `~/.claude` is one), set up the merge driver on every
   machine before the base is first committed:

   ```bash
   memcue install
   ```

   It registers the driver in the repository's git config and adds `memory/facts.db merge=memcue diff=memcue` to
   `.gitattributes`. Without a repository memcue works the same; the base is just not synced.

The hooks do nothing while `memcue` is not on `PATH`, so installing the plugin first is harmless.

## Use

Nothing to do in a session: facts arrive with prompts and are extracted at the end. A fact comes once per session
(again after a compaction), and a background task's notice gets none. A prompt decides by the terms its facts know:
a short question ("dream memcue") needs two matching terms, not the three a longer prompt does, so it stops asking
for more words than the base holds. By hand (`add` without `-origin` names the repository of the working directory
and today):

```bash
memcue search -json postgres port    # find facts and their IDs (a JSON array with -json)
memcue add -keywords "demo, postgres" -origin "demo 2026-10-19" "The demo database is PostgreSQL 18 on port 55432."
memcue add -expires 2026-10-21 "Check how the nightly dream ran on 21 October."
memcue correct -expires 2026-10-28 ID "…"   # -no-expires clears it
memcue correct ID "The demo database is PostgreSQL 18 on port 55433."
memcue retire ID                     # no longer recalled; history keeps it
memcue facts                         # every active fact, with expires= when it has one
memcue recall -cwd . "text"          # what a prompt would get
memcue dream -since 2026-10-19 -list # groups of similar facts a dream would review
memcue dream -since 2026-10-19 -dry-run
memcue dream -all -dry-run -plan plan.json # review the whole base, keep what the model decided
memcue dream -apply plan.json              # carry it out later, without the model
```

An expiry is the last day a fact is worth recalling: after it the fact stays in the base and in `facts`, but recall
skips it, so a one-off reminder does not keep coming back months later. A correction keeps the expiry unless it names
a new one.

Extraction never sees the base, so two sessions can store the same fact, or a fact and the one that disproved it.
`memcue dream` groups each fact the last dream did not see with the stored facts close to it in words and asks
the model which to retire as a duplicate or as contradicted, and which to correct with merged text. The new text is
built only from the facts of the group, every group keeps at least one fact, and each change goes to `dream.log`
with its reason; a retired fact stays in the history. With `MEMCUE_DREAM=1` a dream runs after an extraction once a
day; its first run on a machine only marks the start.

Each retirement names the fact that covers it (`by`) and its kind: `duplicate` or `outdated`. Of two duplicates the
one with the smaller id stays, whatever the model prefers, so machines dreaming over one shared base keep the same
copy and never retire both.

A dream keeps the facts and versions it saw in `dream.seen`, next to `dream.at`. A fact pulled from another machine
after the last dream is new to the next one even when it was written earlier, and so is a correction made since.
Without `dream.seen` (a machine that last dreamt with an older memcue), the next dream goes by when facts were written
and writes the file.

A dry run with `-plan FILE` writes the actions that passed the checks, with the version of every fact of their
groups. `-apply FILE` carries them out without asking the model again, so the run that changes the base is the one
that was reviewed, and it is paid for once. A group with a fact changed since the plan (a new version, or retired) is
refused whole and logged, since its actions were decided on the old text; the other groups apply, and the dream is
marked as of the dry run.

## Configuration

| Variable | Meaning | Default |
|---|---|---|
| `MEMCUE_FACTS` | the fact base | `$CLAUDE_CONFIG_DIR/memory/facts.db`, else `~/.claude/memory/facts.db` |
| `MEMCUE_MODEL` | model of the extraction | `claude-opus-5-5` |
| `MEMCUE_API_URL` | OpenAI-compatible endpoint for the extraction instead of `claude -p`, e.g. `https://api.deepseek.com`; set `MEMCUE_MODEL` with it | unset |
| `MEMCUE_API_KEY` | key for that endpoint | unset |
| `MEMCUE_EXTRACT` | `0` turns the extraction hook off | on |
| `MEMCUE_RECALL_LOG` | `1` logs what each prompt got; the log keeps prompt text | off |
| `MEMCUE_MIN_COVERAGE` | share of a prompt's terms a fact must hold; above 0.25 starts losing facts of other repositories | 0.15 |
| `MEMCUE_DREAM` | `1` runs a dream after an extraction once a day | off |
| `XDG_STATE_HOME` | where `memcue/recall.log`, `memcue/extract.log`, per-session read offsets and the facts each session got are kept | `~/.local/state` |

With `MEMCUE_RECALL_LOG=1`, `recall.log` has one JSON line per prompt: the session, the first 500 characters of the
prompt and the facts it got, if any, with their coverage. It stays on your machine and is what you judge recall by;
it is off by default because it keeps the text of your prompts. `extract.log` has one JSON
line per extraction run: session, part of the transcript read, facts stored, and the cost
Claude Code reports or the tokens the endpoint reports.

The extraction prompts (`internal/extract/*.md`) are written in Russian. Facts come out in the language of the
session or note either way.

## Cost

Extraction is the only part that calls a model. On ten recorded sessions it cost $0.18–1.33 per session on Opus,
about $5 per million characters of transcript; tool output is the bulk of it. Sonnet was half the price but kept
73 % of the facts worth keeping against 88 % for Opus, and nothing at all in two of the ten sessions. DeepSeek
(`deepseek-flash` through `MEMCUE_API_URL`) cost $0.17 for all ten and kept 74 % against 87 % for Opus. Set
`MEMCUE_EXTRACT=0` to keep recall and turn extraction off.

## Results

Measured while building memcue; details and method in [docs/evaluation.md](docs/evaluation.md).

- **Recall on 100 unseen real prompts:** 33 % of injected facts useful and 38 % noise, against 4 % useful and 87 %
  noise for the holographic memory plugin's prefetch on a comparable set. Useful facts reached 12 of the 41 prompts
  that needed memory: lexical search misses prompts that share no rare words with the fact.
- **End to end, 30 questions with answers in memory:** 30 of 30 correct with memcue against 27 of 30 without, one
  turn fewer per answer; one run per question, so a direction rather than a significant result.
- **Session extraction on Opus:** no wrong facts in 102, 88 % of the durable facts of ten sessions kept.
- **Migration of 312 notes:** 2335 facts for $40; 2.5 % of facts carried the model's own conclusions after the
  prompt forbade them.

## Hermes Agent

memcue also works as a memory provider for Hermes Agent: recall before each turn, the built-in memory tool mirrored
into the base, tools to fix facts, and optional extraction of sessions through an OpenAI-compatible endpoint. See
[plugins/hermes](plugins/hermes/README.md).

## License

MIT
