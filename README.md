# memcue

Long-term memory for Claude Code. memcue keeps a base of short, self-contained facts in one SQLite file and works
through hooks:

- **Before each prompt** it looks for facts that share enough rare words with the prompt and gives the agent at
  most two of them, or nothing. Most prompts get nothing; that is intended.
- **At session start, and again after a compaction or `/clear`** it sends a card: global rules and the decisions
  of the current repository, newest first, within about 1.2 thousand tokens. The card is how a rule that never
  shares a word with a prompt still reaches the agent.
- **When a session is compacted or ends** it sends the new part of the transcript to Claude, which writes down what
  will still matter later: decisions, how systems are set up, confirmed causes, what did not work. Facts are only
  added; nothing is compared or overwritten.
- **When you ask** ("remember…", "that is outdated") the agent adds, corrects or retires a fact through the bundled
  skill.

Search is lexical (SQLite FTS5 with Snowball stemming for English and Russian), runs offline in one binary and
takes about 10 ms per prompt. A fact keeps where and when it was learnt and where it holds (`scope`), but facts from
every repository are searched: what you learnt in one project is recalled in another. On equal scores a fact of the
current host comes first; nothing is excluded, because working on a server from a laptop is normal. An uppercase
ticket key such as `PROJ-1234`, `#123` or `!45` is kept as one exact term, so a prompt naming it recalls that ticket
and not another number; bare numbers, ports and versions are still left out of the index. A versioned name such as
`PostgreSQL-16` is not a ticket: it keeps `PostgreSQL` and drops `16`.

The base can live in a git repository and be synced between machines; memcue ships a merge driver for it. The same
base works for [Hermes Agent](plugins/hermes/README.md) and for
[DeepSeek Harness](plugins/deepseek-harness/README.md).

memcue is young: version 0.1 has been measured on one developer's memory and sessions (see [Results](#results)) and
not yet run for long in daily work.

## Requirements

- macOS or Linux.
- Claude Code with the `claude` command on `PATH`: extraction runs `claude -p`, unless an OpenAI-compatible
  endpoint is configured instead (see [Configuration](#configuration)).
- Go 1.27 or later, to build from source.

## Compatibility

This release is `0.4.0`; the fact-base schema is 4. The three plugin manifests carry the same version, and a release
bumps all of them together.

| memcue | schema | Claude Code plugin | DSH plugin | Hermes plugin |
|---|---|---|---|---|
| 0.4.0 | 4 | 0.4.0 | 0.4.0 (`dsh >=0.2.0-rc.2 <0.3.0`) | 0.4.0 (`hermes-agent` 0.15.2) |

The DSH peer range is enforced by the harness compatibility check; the Hermes row names the version the provider
tests ran against. The Claude Code plugin has no separate version negotiation: install the plugin and the binary from
the same release.

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
   `.gitattributes`. Without a repository memcue works the same; the base is just not synced. A merge carries the
   vectors whose text hash still matches the merged fact; `memcue embed` fills any missing or stale ones.

The hooks do nothing while `memcue` is not on `PATH`, so installing the plugin first is harmless.

## Upgrade

The fact base schema is 4. memcue 0.4.0 reads it; 0.2.1 and older refuse a newer base with `update memcue`: upgrade
every machine that shares the base before the first new memcue writes to it. A mixed-version sync is not safe: an
older binary refuses the version-4 base instead of reading it, and 0.2.1 does not know `fact_vectors`. Opening a version-3 base creates
`fact_vectors` and rebuilds the derived index once; version 4 adds the optional vectors on top of the version-3
stems, which keep the word of a versioned name such as `PostgreSQL-16`.

## Use

Nothing to do in a session: facts arrive with prompts and are extracted at the end. Extracted facts are candidates:
recall gives them only after someone approves them — `memcue vet` by a rule or a model, or by hand (`memcue candidates`,
`memcue approve ID`, the `memcue review` page; `memcue retire ID` rejects one). A fact you add yourself with `memcue add` is active at once. An explicit `correct` of
an approved fact also writes a new active version: it is a deliberate correction, not a new candidate. Only new
extracted and mirrored facts wait for review. A fact comes once per session
(again after a compaction or `/clear`), and a background task's notice gets none. A prompt decides by the terms its facts know:
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
memcue candidates                    # extracted facts waiting for approval, newest first (-json for a JSON array)
memcue vet -dry-run                  # what the rules and the judge would decide about the queue
memcue vet                           # decide it: approve what belongs in the base, retire what does not
memcue vet -no-model                 # only the deterministic rules; undecided candidates stay for review
memcue approve ID [ID ...]           # take candidates into recall
memcue review                        # local page: approve or reject candidates; undo the last action
memcue facts                         # every active fact, with expires= when it has one
memcue recall -cwd . "text"          # what a prompt would get
memcue dream -since 2026-10-19 -list # groups of similar facts a dream would review
memcue dream -since 2026-10-19 -dry-run
memcue dream -all -dry-run -plan plan.json # review the whole base, keep what the model decided
memcue dream -apply plan.json              # carry it out later, without the model
memcue audit -dry-run -plan audit.json     # propose old journal and CI facts to retire
memcue audit -apply audit.json             # carry out the retire decisions after review
```

`memcue review` serves a page on `127.0.0.1` and opens it. It shows each candidate with its kind, scope, source,
evidence, origin and date; filters by kind and source; approves or rejects several at once; and undoes the last
action. Rejecting retires the fact and writes `rejected in review` to `$XDG_STATE_HOME/memcue/review.log`; an
approval is logged there too. The page has no external assets and works offline. `-addr` must stay a loopback
address, and `-no-open` only prints the URL.

`memcue add` and `memcue correct` refuse text that looks like a token or a password; `-force` stores it anyway.
`migrate` and `import` skip such facts and report them without echoing the value, because the base may be synced
through git.

`memcue vet` is the automatic half of the same review, so the queue does not have to be cleared by hand. Three rules
decide first, without a model: a mirror of the built-in memory stays only while the notes still hold its text
(`-memory DIR` or `MEMCUE_MEMORY_DIR` names them; without that directory the rule is skipped rather than retiring every
mirror), an origin whose tier is exactly `test` (as in `test 2026-10-19`; a repository such as `test-utils` is not a
test run) is retired as a test trace, and a candidate whose words are all in an
approved fact is retired as covered. The rest goes to the `keep / drop / unsure` judge, twelve candidates a call, each
with the approved facts nearest to it, so a duplicate or a contradiction of an approved fact is visible in the answer.
Everything else the judge cannot settle stays **provisional** by default when the model gave an explicit `unsure`
answer: that answer becomes a provisional fact for the TTL, and `unsure` is still an entry in the report rather than
an approval. A failed or empty answer does not: `vet` never turns a call that did not decide into a provisional fact,
so the candidate waits for the next run. A provisional fact is recalled after approved
facts, marked in the log, and expires after `MEMCUE_VET_TTL_DAYS` without a person. Set
`MEMCUE_VET_POLICY=quarantine` or run `memcue vet -quarantine` to keep the old review queue, and `-no-model` keeps
every candidate the rules cannot decide in that queue as well. Every verdict goes to
`$XDG_STATE_HOME/memcue/vet.log` with the rule or model and its reason; the page, the `approve` command and the
page's undo (`store.Reopen`) stay the manual way, unchanged.

The review runs on the main model. With `MEMCUE_VET=1` the facts a session just stored are reviewed in the
extraction process, after storing them and before the dream; that process is already detached from the session, so a
turn never waits for the verdict. A run that finds another one busy is skipped rather than queued, and its failures go
to `vet.log`, not to the extraction.

A duplicate can be answered without reading a candidate: only by comparing it with the base. With `MEMCUE_JUDGE_URL`,
`MEMCUE_JUDGE_MODEL` and `MEMCUE_JUDGE_KEY` set to a typed decisions endpoint (OpenRouter
`https://openrouter.ai/api/alpha/decisions` with `typesafe/jev-1.14`, TypeSafe `https://api.typesafe.ai/v1/systemone`
with `jev-latest`), `vet` asks it first: the candidate and the whole approved base in one request, one `score` per
approved fact over three levels — same claim, same subject with a different claim, unrelated — and a candidate whose
best "same claim" reaches `MEMCUE_JUDGE_DUP_MIN` (default 0.5) is retired with the probability and the fact it repeats in
`vet.log`. Every fact gets its own question, not one `choice`: a `choice` names the closest fact and drops the others,
and a base that holds three copies of a fact needs all three. Everything the judge does not call a duplicate still goes
to the review model, which decides whether the fact is worth keeping at all; a failed call decides nothing and leaves
the candidate in the queue. A base larger than `MEMCUE_JUDGE_DUP_MAX_FACTS` is split into requests of that size
(default 400: about 150 input tokens per fact fit a 64k request; `MEMCUE_JUDGE_DUP_LONG_CONTEXT=1` sends the
danger-zone header for 262k). Every chunk is asked and the strongest "same claim" wins, so the cost per candidate
grows with the number of chunks; a failed chunk leaves the candidate undecided. Measured on a base of 235 approved
facts and 41 candidates the review model had labelled: at 0.5 the judge retires 8–11 of the 15 duplicates and 0–1 of
the 26 facts worth keeping, AUC 0.835–0.877 across two runs; a plain yes/no question at the same threshold retires 8
and 1, AUC 0.815–0.822, at 0.3 it still lets 2 facts through. Each candidate costs 1.3 s and $0.0017 —
[docs/research/jev-judge-2026-10.md](docs/research/jev-judge-2026-10.md).

The SessionStart card is a view of the active facts, not a copy: a rule or decision leaves it when corrected,
retired or expired. `card.log` records the ids it carried, so an audit can see a rule as used even though recall
never served it.

`memcue audit` is the one-off clean-up of the migrated base. It proposes active facts that read as a journal entry
(a date next to "обновили", "упал", …) or a retelling of CI or a Makefile, and writes them with a reason, their
`last_recalled` and card delivery to a JSON plan. Mark each proposal `retire` or `keep`, then apply the plan: a
heuristic is applied only when every one of its proposals is labelled, `-min-precision` (default 0.95) refuses a whole
heuristic whose labelled precision is lower, and a fact changed since the plan is not retired on stale text. A partial
review therefore retires nothing of that heuristic, even when its few labels agree. The plan, the measured precision
and every applied or refused decision go to `audit.log`. Usage only orders the review; it never retires a fact by
itself.

An expiry is the last day a fact is worth recalling: after it the fact stays in the base and in `facts`, but recall
skips it, so a one-off reminder does not keep coming back months later. The date is the local calendar day of the
machine that judges it. A correction keeps the expiry unless it names a new one.

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

Most groups need no action, and the Jev judge can tell them apart. With `MEMCUE_JUDGE_URL`, `MEMCUE_JUDGE_MODEL` and
`MEMCUE_JUDGE_KEY` set, a dream first asks the judge about each group and sends on only the groups whose probability of
an action reaches `MEMCUE_JUDGE_TRIAGE_MIN` (default 0.2). The endpoint speaks the typed decisions API of RouterAI,
for example `https://routerai.ru/api/v1/decisions` with `typesafe/jev-1.14`. Triage only classifies: the main model
still writes every change. A group triage fails on goes on, and `dream.log` records how many groups triage dropped.
Without the judge, or with a threshold that is not a probability, there is no triage. Measured on 395 groups against an Opus dry run, 0.2 sent on 191 groups and kept 42 of the 43 Opus acted on.
The research behind it, with the alternatives that did not work (vectors, pairwise questions), is in
[docs/research/dream-triage-2026-10.md](docs/research/dream-triage-2026-10.md). What it costs to ask the
same model about the whole base in one request, with no index at all, is in
[docs/research/jev-judge-2026-10.md](docs/research/jev-judge-2026-10.md).

## Configuration

The three model roles are described in [docs/models.md](docs/models.md). The table is the
contract this release uses; there is no separate review or triage endpoint.

| Variable | Meaning | Default |
|---|---|---|
| `MEMCUE_FACTS` | the fact base | `$CLAUDE_CONFIG_DIR/memory/facts.db`, else `~/.claude/memory/facts.db` |
| `MEMCUE_STATE_DIR` | state directory for logs, locks, the dream schedule and session offsets; a non-default base gets a per-base subdirectory under `$XDG_STATE_HOME/memcue` | unset |
| `MEMCUE_MODEL` | model of the extraction and review | `claude-opus-5-5` |
| `MEMCUE_API_URL` | OpenAI-compatible endpoint for the main model instead of `claude -p`, e.g. `https://api.deepseek.com`; set `MEMCUE_MODEL` with it | unset |
| `MEMCUE_API_KEY` | key for that endpoint | unset |
| `MEMCUE_JUDGE_URL` | the one Jev decisions endpoint, e.g. `https://openrouter.ai/api/alpha/decisions` | unset: no judge |
| `MEMCUE_JUDGE_MODEL` | model of that endpoint, e.g. `typesafe/jev-1.14` | unset: no judge |
| `MEMCUE_JUDGE_KEY` | key for that endpoint | unset: no judge |
| `MEMCUE_JUDGE_RERANK_MIN` | probability below which a recall candidate is not given | `0.5` |
| `MEMCUE_JUDGE_RERANK_CANDIDATES` | candidates taken from each source, lexical and vector | `10` |
| `MEMCUE_JUDGE_RERANK_TIMEOUT` | one rerank request | `2s` |
| `MEMCUE_JUDGE_TRIAGE_TIMEOUT` | one triage request | `2s` |
| `MEMCUE_JUDGE_TRIAGE_MIN` | probability of an action that sends a dream group to the main model | `0.2` |
| `MEMCUE_JUDGE_DUP_TIMEOUT` | one duplicate request | `10s` |
| `MEMCUE_JUDGE_DUP_MIN` | probability of "same claim" that retires a candidate | `0.5` |
| `MEMCUE_JUDGE_DUP_MAX_FACTS` | approved facts that fit one duplicate request | `400` |
| `MEMCUE_JUDGE_DUP_LONG_CONTEXT` | `1` sends the danger-zone header for the duplicate check | off |
| `MEMCUE_EMBED_URL` | OpenAI-compatible embeddings endpoint, e.g. `http://127.0.0.1:11434/v1` | unset: no vector candidates |
| `MEMCUE_EMBED_MODEL` | embeddings model, e.g. `bge-m3` | unset |
| `MEMCUE_EMBED_KEY` | key for a hosted embeddings endpoint; empty for a local server | empty |
| `MEMCUE_EMBED_TIMEOUT` | one embedding request | `2s` |
| `MEMCUE_EMBED_SPACE` | label of the vector space when a server changes model under one name | empty |
| `MEMCUE_VECTOR_POOL` | vectors of the most recent facts scored per prompt; `0` or less scores every vector | `2000` |
| `MEMCUE_EXTRACT` | `0` turns the extraction hook off | on |
| `MEMCUE_HOST` | name of this machine for fact scope and the recall tie-break; set it where the host name is unstable, such as a container | `os.Hostname()` |
| `MEMCUE_RECALL_LOG` | `1` logs what each prompt got; the log keeps prompt text | off |
| `MEMCUE_MIN_COVERAGE` | share of a prompt's terms a fact must hold; above 0.25 starts losing facts of other repositories | 0.15 |
| `MEMCUE_DREAM` | `1` runs a dream after an extraction once a day | off |
| `MEMCUE_VET` | `1` decides the facts a session extracted, right after storing them and before the dream; needs `MEMCUE_API_URL` | off |
| `MEMCUE_VET_POLICY` | `provisional` (default) turns `unsure` into provisional facts; `quarantine` keeps the human queue | `provisional` |
| `MEMCUE_VET_TTL_DAYS` | how long a provisional fact is recalled | `14` |
| `MEMCUE_MEMORY_DIR` | directory of the built-in memory notes the mirror rule checks | unset: the rule is skipped |
| `XDG_STATE_HOME` | where `memcue/recall.log`, `memcue/extract.log`, `memcue/review.log`, `memcue/vet.log`, `memcue/card.log`, `memcue/audit.log`, `memcue/judge.pause`, per-session read offsets and the facts each session got are kept | `~/.local/state` |

The vector reader is a candidate source for the judge, not a standalone reader: without
`MEMCUE_JUDGE_*`, `MEMCUE_EMBED_*` changes nothing in recall. Without `MEMCUE_EMBED_*`, the judge
orders the lexical candidates only.

With `MEMCUE_RECALL_LOG=1`, `recall.log` has one JSON line per prompt: the session, the host, the first 500
characters of the prompt and the facts it got, if any, with their coverage, scope and whether a fact of another host
displaced one of this host. It stays on your machine and is what you judge recall by; it is off by default because it
keeps the text of your prompts. `extract.log` has one JSON line per extraction run: session, part of the transcript
read, facts stored, and the cost Claude Code reports or the tokens the endpoint reports.

The extraction prompts (`internal/extract/*.md`) are written in Russian. Facts come out in the language of the
session or note either way.

## Cost

Extraction is the part that costs most. On ten recorded sessions it cost $0.18–1.33 per session on Opus,
about $5 per million characters of transcript; tool output is the bulk of it. Sonnet was half the price but kept
73 % of the facts worth keeping against 88 % for Opus, and nothing at all in two of the ten sessions. DeepSeek
(`deepseek-flash` through `MEMCUE_API_URL`) cost $0.17 for all ten and kept 74 % against 87 % for Opus. Set
`MEMCUE_EXTRACT=0` to keep recall and turn extraction off.

A review is the second model call, and a much smaller one: twelve candidates with a few neighbours each per call, and
no candidate at all on a session that taught nothing. `vet.log` records the tokens of every call, and the cost when
`claude -p` reports one; a rules-only run (`-no-model`) costs nothing. The review uses `MEMCUE_MODEL` and
`MEMCUE_API_URL`; there is no separate review model.

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

## Review and vet

Facts written by extraction, and facts mirrored from Hermes' built-in memory, are **candidates**: they are stored and
visible in `memcue candidates`, but recall ignores them until someone approves them. `memcue vet` applies the
deterministic rules and the review model to the queue, `memcue approve ID...` takes candidates into recall, and
`memcue review` serves the local batch page; `memcue retire ID` rejects one. A fact written by `memcue add` is active at
once, and an explicit `correct` of an approved fact writes a new active version. A retired fact cannot be corrected:
retirement is final, so a changed fact is added as a new fact, and the review page's undo remains the way back from a
rejection. The full rules are in [Use](#use).

## Harness support

The same base and binary serve all three hosts; the adapters differ at the edges:

| Capability | Claude Code | DeepSeek Harness | Hermes Agent |
|---|---|---|---|
| Recall before a prompt | `UserPromptSubmit` hook | `agent/pre-step` | `prefetch` |
| Session card | `SessionStart` | `session-card` on first step and after compaction | `session-card` with the first turn and after compression |
| Facts again after a compaction / `/clear` | yes | yes | yes |
| Session extraction | `PreCompact` / `SessionEnd` | `turn/end` / `session/disposed` | threshold, idle, pre-compress, session end |
| `add` / `correct` / `retire` | skill and CLI | four tools | four tools |
| Expiry | CLI and skill | `add` / `correct` | `add` / `correct` |
| Mirror of built-in memory | not applicable | no built-in memory files | `MEMORY.md` / `USER.md` mirrored as candidates |
| Candidate review and vet | binary | binary | binary |

State and logs for the default base live in `$XDG_STATE_HOME/memcue`; any other base gets a per-base subdirectory,
`$XDG_STATE_HOME/memcue/<hash>`, so two bases on one machine do not share a dream schedule, a vet lock or a judge
pause. `MEMCUE_STATE_DIR` overrides the whole path. DSH keeps its extraction checkpoints in
`$XDG_STATE_HOME/memcue/dsh`, and Hermes keeps its mirror, seen and pending files under `$HERMES_HOME/memcue`; the
binary's logs, locks and dream schedule always follow the base. The fact base is `$CLAUDE_CONFIG_DIR/memory/facts.db`
(or `~/.claude/memory/facts.db`) by default, `$HERMES_HOME/memcue/facts.db` for Hermes, and memcue's default for DSH
unless `facts` is configured. A shared base is supported; point `MEMCUE_FACTS` or the plugin's `facts` option at the
same file.

## DeepSeek Harness

memcue also works as a native DSH plugin: recall before each step, the session card, extraction at turn end and on
disposal, and four fact tools. See [plugins/deepseek-harness](plugins/deepseek-harness/README.md).

## Hermes Agent

memcue also works as a memory provider for Hermes Agent: recall before each turn, the built-in memory tool mirrored
into the base, tools to fix facts, and optional extraction of sessions through an OpenAI-compatible endpoint. See
[plugins/hermes](plugins/hermes/README.md).

## License

MIT
