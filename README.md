# memcue

Long-term memory for Claude Code. memcue keeps a base of short, self-contained facts in one SQLite file and works
through hooks:

- **Before each prompt** it looks for facts that share enough rare words with the prompt and gives the agent at
  most two of them, or nothing. Most prompts get nothing; that is intended.
- **At session start, and again after a compaction or `/clear`** it sends a card: global rules and the rules and
  decisions of the current repository, the ones you stated first, within about 1.2 thousand tokens. The card is how a
  rule that never shares a word with a prompt still reaches the agent.
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

memcue is young: the 0.4 line has been measured on one developer's memory and sessions (see [Results](#results)) and
not yet run for long in daily work.

## Requirements

- macOS or Linux.
- Claude Code with the `claude` command on `PATH`: extraction runs `claude -p`, unless an OpenAI-compatible
  endpoint is configured instead (see [Configuration](#configuration)).
- Go 1.27 or later, to build from source.

## Compatibility

This release is `0.6.0`; the fact-base schema is 5 (see [Upgrade](#upgrade)). The three plugin manifests carry the same
version, and a release bumps all of them together.

| memcue | schema | Claude Code plugin | DSH plugin | Hermes plugin |
|---|---|---|---|---|
| 0.6.0 | 5 | 0.6.0 | 0.6.0 (`dsh >=0.2.0-rc.2 <0.3.0`) | 0.6.0 (`hermes-agent` 0.15.2) |
| 0.5.3 | 5 | 0.5.3 | 0.5.3 (`dsh >=0.2.0-rc.2 <0.3.0`) | 0.5.3 (`hermes-agent` 0.15.2) |
| 0.5.2 | 5 | 0.5.2 | 0.5.2 (`dsh >=0.2.0-rc.2 <0.3.0`) | 0.5.2 (`hermes-agent` 0.15.2) |
| 0.5.1 | 5 | 0.5.1 | 0.5.1 (`dsh >=0.2.0-rc.2 <0.3.0`) | 0.5.1 (`hermes-agent` 0.15.2) |
| 0.5.0 | 5 | 0.5.0 | 0.5.0 (`dsh >=0.2.0-rc.2 <0.3.0`) | 0.5.0 (`hermes-agent` 0.15.2) |
| 0.4.0 | 4 | 0.4.0 | 0.4.0 (`dsh >=0.2.0-rc.2 <0.3.0`) | 0.4.0 (`hermes-agent` 0.15.2) |

The DSH peer range is enforced by the harness compatibility check; the Hermes row names the version the provider
tests ran against. The binary and the adapters check each other's release: the DSH and Hermes plugins ask
`memcue version -json` at startup and turn the session card and extraction off with one warning on a binary older
than 0.4.0 (recall keeps working), and the Claude Code `SessionStart` hook checks `memcue version -minimum 0.4.0` and
prints the same hint. A build from source reports a suffixed version and is treated as the current tree. Install the
plugin and the binary from the same release.

## Install

1. Optional: install the binary yourself and put `memcue` on `PATH`. Download it for your system from
   [Releases](https://github.com/juev/memcue/releases), or build it:

   ```bash
   go install github.com/juev/memcue/cmd/memcue@latest
   ```

   Without this step the plugin installs the binary of its own release (see [Binary from the plugin](#binary-from-the-plugin)).

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
   `.gitattributes`. The driver is a small script in `.git` that finds the binary through `MEMCUE_BIN`, then `PATH`,
   then the path it had when `memcue install` ran, so an upgrade that moves the binary does not break the next merge.
   Without a repository memcue works the same; the base is just not synced. A merge carries no vectors.

### Binary from the plugin

All three plugins (Claude Code, DSH, Hermes) find the binary in this order: the one configured explicitly
(`MEMCUE_BIN`, or `binary` in the DSH and Hermes config); `memcue` on `PATH` when it is the plugin's release or newer;
the copy the plugin installed; an older `memcue` on `PATH`, so recall keeps working. When none of them is the plugin's
release, the plugin downloads that release from GitHub, checks the archive against the release's `checksums.txt`,
and installs it:

| Host | Where | When |
|---|---|---|
| Claude Code | `${XDG_DATA_HOME:-~/.local/share}/memcue/<version>/memcue` | in `SessionStart`, before the first answer of the session; the directory goes on the session's `PATH`, so the skill finds `memcue` too |
| DSH | the same directory, shared with Claude Code | in the background at plugin start |
| Hermes | `$HERMES_HOME/memcue/bin/<version>/memcue`, which outlives the container | in the background at the first `initialize` |

A configured binary is never replaced. Set `MEMCUE_AUTO_INSTALL=0` to install nothing; the plugin then says that the
binary is missing, as before. The checksum guards against a broken download, not against a replaced release:
`checksums.txt` comes from the same release. Claude Code logs failed installs to
`${XDG_STATE_HOME:-~/.local/state}/memcue/install.log` and tries again after an hour, since `SessionStart` waits for
the download; DSH and Hermes download in the background and log failures as warnings. The Claude Code script
needs `curl`, `tar` and `sha256sum` or `shasum`. A binary installed this way is not on your shell's `PATH`: to run
`memcue install` or other commands by hand, call it by its path or add the directory to `PATH`.

## Upgrade

The fact base schema is 5: it adds the search queries of a fact, which the index holds next to its words. memcue
0.4.0 refuses a version-5 base with `update memcue`, because its merge, its writes and its index rebuild would drop the
queries: upgrade every machine that shares the base, the Hermes container included, before the first memcue 0.5.0
opens it. Opening a version-4 base adds the column and rebuilds the index once; `memcue dump` (the git
textconv) still reads older revisions of the file, with no queries.

Two things a base upgraded from 0.4 still needs. The facts stored before 0.5 have no queries; each dream now gives them
queries, up to 500 a dream, and `memcue dream -all` does the whole base at once. And facts that describe memcue 0.4
itself (its variables, verbs and versions) stay recallable and win lexically, because they are written in memcue's own
words: no dream or vet compares a fact with the release it is about. Find them and decide each one:

```sh
memcue facts | grep -E 'MEMCUE_EMBED|MEMCUE_(VET|RERANK|TRIAGE)_(URL|MODEL|KEY)|EMBED_MODE|memcue (embed|label|audit)|0\.4\.0'
```

`memcue retire ID` a fact that describes the removed state as current, `memcue correct ID TEXT` one that is partly
true, and keep one that records what was measured or decided then. Since 0.5.3 `vet` finds most of these itself (see
[Review and vet](#review-and-vet)); facts about the host's own config files are still found only this way.

Schema 4 was the schema of 0.3.0 and 0.4.0; 0.2.1 and older refuse a newer base with `update memcue`: upgrade
every machine that shares the base before the first new memcue writes to it. If you ran `memcue install` before
0.4.0, run it once more after the upgrade: the merge driver is now a script in `.git` that survives a binary upgrade,
while an older install pinned the absolute path of the binary it ran from. A mixed-version sync is not safe: an
older binary refuses the version-4 base instead of reading it, and 0.2.1 does not know `fact_vectors`. Opening a version-3 base creates
`fact_vectors` and rebuilds the derived index once; version 4 adds the optional vectors on top of the version-3
stems, which keep the word of a versioned name such as `PostgreSQL-16`.

## Use

Nothing to do in a session: facts arrive with prompts and are extracted at the end. Extracted facts are candidates:
recall gives them only after someone approves them — `memcue vet` by a rule or a model, or by hand (`memcue candidates`,
`memcue approve ID`; `memcue retire ID` rejects one). A fact you add yourself with `memcue add` is active at once. An explicit `correct` of
an approved fact also writes a new active version: it is a deliberate correction, not a new candidate. Only new
extracted and mirrored facts wait for review. A fact comes once per session
(again after a compaction or `/clear`), and a background task's notice gets none. A prompt decides by the terms its facts know:
a short question ("dream memcue") needs two matching terms, not the three a longer prompt does, so it stops asking
for more words than the base holds. By hand (`add` without `-origin` names the repository of the working directory and
today; outside a repository the origin stays empty, because a scratch directory's name says nothing about where the
fact came from). A fact of `kind: event` gets an expiry of `MEMCUE_EVENT_TTL_DAYS` days (7 by default) unless `-expires`
says otherwise: what happened in one run is not the knowledge a later session needs:

```bash
memcue search -json postgres port    # find facts and their IDs (a JSON array with -json)
memcue add -keywords "demo, postgres" -origin "demo 2026-10-19" "The demo database is PostgreSQL 18 on port 55432."
memcue add -expires 2026-10-21 "Check how the nightly dream ran on 21 October."
memcue correct -expires 2026-10-28 ID "…"   # -no-expires clears it
memcue correct ID "The demo database is PostgreSQL 18 on port 55433."
memcue retire ID                     # no longer recalled; history keeps it
memcue reopen ID                     # undo a retire: back as it was before, approved or waiting for review
memcue candidates                    # extracted facts waiting for approval, newest first (-json for a JSON array)
memcue vet -dry-run                  # what the rules and the judge would decide about the queue
memcue vet                           # decide it: approve what belongs in the base, retire what does not
memcue vet -no-model                 # only the deterministic rules; undecided candidates stay for review
memcue approve ID [ID ...]           # take candidates into recall
memcue facts                         # every active fact, with expires= when it has one
memcue recall -cwd . "text"          # what a prompt would get
memcue recall -explain "text"        # every step: terms, lexical gate, judge scores, final cut
memcue recall -fact ID "text"        # where this fact dropped out for this prompt
memcue dream -since 2026-10-19 -list # groups of similar facts a dream would review
memcue dream -since 2026-10-19 -dry-run
memcue dream -all -dry-run -plan plan.json # review the whole base, keep what the model decided
memcue dream -apply plan.json              # carry it out later, without the model
```

`memcue add` and `memcue correct` refuse text that looks like a token or a password; `-force` stores it anyway.
`migrate` and `import` skip such facts and report them without echoing the value, because the base may be synced
through git.

`memcue vet` is the automatic half of the same review, so the queue does not have to be cleared by hand. Three rules
decide first, without a model: a mirror of the built-in memory stays only while the notes still hold its text
(`-memory DIR` or `MEMCUE_MEMORY_DIR` names them; without that directory the rule is skipped rather than retiring every
mirror), an origin whose tier is exactly `test` (as in `test 2026-10-19`; a repository such as `test-utils` is not a
test run) is retired as a test trace, and a candidate whose words are all in an
approved fact is retired as covered. The rest goes to the `keep / drop / unsure / split` judge, twelve candidates a
call, each with the approved facts nearest to it, so a duplicate or a contradiction of an approved fact is visible in the
answer. `split` is for a candidate that still holds several claims, one of which can go stale while another stays
true: the candidate is retired, each part is stored as a candidate with its origin, scope, kind and source (not its
keywords, which were chosen for the whole text), and the same run reviews the parts. A part is not split again in that
run, and a split into fewer than two parts leaves the candidate in the queue. `vet.log` records the parts and their ids.
Everything else the judge cannot settle stays **provisional** by default when the model gave an explicit `unsure`
answer: that answer becomes a provisional fact for the TTL, and `unsure` is still an entry in the report rather than
an approval. A failed or empty answer does not: `vet` never turns a call that did not decide into a provisional fact,
so the candidate waits for the next run. A provisional fact is recalled after approved
facts, marked in the log, and expires after `MEMCUE_VET_TTL_DAYS` without a person. Set
`MEMCUE_VET_POLICY=quarantine` or run `memcue vet -quarantine` to keep the old review queue, and `-no-model` keeps
every candidate the rules cannot decide in that queue as well. Every verdict goes to `vet.log` in the state
directory with the rule or model and its reason; the page, the `approve` command and the page's undo (`store.Reopen`)
stay the manual way, unchanged.

The review runs on the main model: `MEMCUE_API_URL` when it is set, otherwise `claude -p`, as the extraction does. The
facts a session just stored are reviewed in the extraction process, after storing them and before the dream
(`MEMCUE_VET=0` turns that off; without an endpoint and without `claude` on `PATH` nothing is reviewed); that process is
already detached from the session, so a turn never waits for the verdict. A run that finds another one busy is skipped
rather than queued, and its failures go to `vet.log`, not to the extraction.

Duplicates are the review's job too: it sees each candidate's nearest approved facts and drops a candidate that
repeats one. A separate Jev pass over those neighbours ran before the review until #266; on the 41-candidate sample of
#240, labelled by `claude -p`, the review alone retired 10 of the 11 drops and 1–2 of 29 facts worth keeping, and with
the Jev pass 8–10 and 1–4, for about 420k more input tokens
([docs/research/vet-neighbours-2026-10.md](docs/research/vet-neighbours-2026-10.md)).

The SessionStart card is a view of the active facts, not a copy: a rule or decision leaves it when corrected,
retired or expired. `card.log` records the ids it carried, so a rule shows as used even though recall never served
it.

The card holds about 3,500 characters, a dozen facts, and a labelled base has more rules than that. They are chosen
in this order:

1. The facts the user stated (`source` `user` or `user_confirmed`) come first, then the rest; newest first within
   each. The date alone says little on a migrated base, where most rules share the day of the migration.
2. The rules and decisions of the current repository may take half of the card before anything else is placed.
3. The rest of the card is filled in the order of step 1, global rules and the repository's facts together, so a
   repository whose stated decisions come first may take more than half.

Measured on the author's base (65 global rules after the labelling of #219, 129 facts for the largest repository): the
global card is unchanged; a repository with 49 facts, none of them stated, gets 6 lines instead of 4; a repository
with 11 stated decisions keeps its 10 lines. Ordering by recall hits was rejected: the rules recall already serves
are the ones the card is not needed for.

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

Before grouping, a dream asks the main model which of those facts still hold several claims, twenty facts a call: a
fact one part of which can go stale while another stays true. Such a fact is retired and its parts take its place,
approved, with its origin, scope, kind, source and expiry (not its keywords, which were chosen for the whole text).
The parts are grouped in the same dream, so a part that repeats an older fact is retired as its duplicate. A failed
split call leaves its facts as they are and is counted in `dream.log` (`split_errors`); a split with fewer than two
parts, or of a fact that changed while the model ran, is refused and logged. `vet` splits candidates the same way, so
this step mostly matters for the facts stored before it: `memcue dream -all -dry-run -plan FILE` proposes the splits of
the whole base, and `-apply FILE` writes them.

Then every fresh fact without **search queries** gets them: 6–10 short phrases a user would name the task with, the
general name of what the fact is about and how the problem or question sounds, which the index searches next to the
words and keywords of the fact. Extraction writes them with every fact it stores, and `add` and `correct` ask for them
in one call when `MEMCUE_API_URL` is set (not for a candidate; a failed call leaves the fact to the dream). A correction
asks again whenever the text changed, in the same run: the words the fact was found by no longer describe it, and a
dream's own corrections are expanded after it judges the groups. So the dream fills only the facts written without an
endpoint, or of `import`, and the parts
of a split. Facts stored
before extraction wrote queries are never fresh again, so each dream also expands up to 500 of them, the newest first,
without reviewing their text: a base upgraded from 0.4 fills over a few dreams, and `dream -all` does it at once,
together with the review of the whole base. The call asks the main model, 25 facts at a time; on `api.deepseek.com` it turns reasoning off,
since the queries were measured that way and reasoning took 97–99 % of the output of such calls. A failed call leaves its
facts for the next dream (`expand_errors` in `dream.log`). On the gold set of 180 prompts the queries added 7–9 prompts
that got a useful fact from the judge ([docs/research/one-model-2026-10.md](docs/research/one-model-2026-10.md)).

A dream keeps the facts and versions it saw in `dream.seen`, next to `dream.at`. A fact pulled from another machine
after the last dream is new to the next one even when it was written earlier, and so is a correction made since.
Without `dream.seen` (a machine that last dreamt with an older memcue), the next dream goes by when facts were written
and writes the file.

A dry run with `-plan FILE` writes the actions that passed the checks, with the version of every fact of their
groups. `-apply FILE` carries them out without asking the model again, so the run that changes the base is the one
that was reviewed, and it is paid for once. A group with a fact changed since the plan (a new version, or retired) is
refused whole and logged, since its actions were decided on the old text; the other groups apply, and the dream is
marked as of the dry run. The plan lists the splits under `splits`, each with the version the model read; edit the
parts or delete an entry before `-apply`. Splits apply first, so a group the dry run formed around a compound fact is
refused as changed, and the parts are left for the next dream to group with their neighbours. The queries are under
`expansions`; they apply after the splits, and the version they write does not count as a change of the groups.

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
| `MEMCUE_FACTS` | the fact base; a leading `~/` or `$HOME/` is expanded, and the DSH and Hermes adapters require the result to be absolute | `$CLAUDE_CONFIG_DIR/memory/facts.db`, else `~/.claude/memory/facts.db` |
| `MEMCUE_STATE_DIR` | state directory for logs, locks, the dream schedule and session offsets; a non-default base gets a per-base subdirectory under `$XDG_STATE_HOME/memcue` | unset |
| `MEMCUE_MODEL` | model of the extraction and review | `claude-opus-5-5` |
| `MEMCUE_API_URL` | OpenAI-compatible endpoint for the main model instead of `claude -p`, e.g. `https://api.deepseek.com`; set `MEMCUE_MODEL` with it | unset |
| `MEMCUE_API_KEY` | key for that endpoint | unset |
| `MEMCUE_JUDGE_URL` | the one Jev decisions endpoint, e.g. `https://openrouter.ai/api/alpha/decisions` | unset: no judge |
| `MEMCUE_JUDGE_MODEL` | model of that endpoint, e.g. `typesafe/jev-1.14` | unset: no judge |
| `MEMCUE_JUDGE_KEY` | key for that endpoint | unset: no judge |
| `MEMCUE_JUDGE_RERANK_MIN` | probability below which a recall candidate is not given | `0.5` |
| `MEMCUE_JUDGE_RERANK_CANDIDATES` | facts the judge reads, in the search's BM25 order | `20` |
| `MEMCUE_JUDGE_RERANK_TIMEOUT` | one rerank request | `2s` |
| `MEMCUE_RERANK` | which reader orders the recall candidates: `judge` (Jev), `listwise` (the main model, needs `MEMCUE_API_URL`) or `off` (the words and the coverage gate); any other value keeps the words | the words: a configured Jev endpoint does not turn the judge on (#331) |
| `MEMCUE_MAIN_RERANK_CANDIDATES` | facts the listwise reader reads, in the search's BM25 order | `20` |
| `MEMCUE_MAIN_RERANK_TIMEOUT` | one listwise request | `2s` |
| `MEMCUE_JUDGE_TRIAGE_TIMEOUT` | one triage request | `2s` |
| `MEMCUE_JUDGE_TRIAGE_MIN` | probability of an action that sends a dream group to the main model | `0.2` |
| `MEMCUE_AUTO_INSTALL` | `0` stops the plugins from downloading the binary of their release (see [Binary from the plugin](#binary-from-the-plugin)) | on |
| `MEMCUE_EXTRACT` | `0` turns automatic extraction off in every host; it wins over the DSH `extract` setting and the Hermes `extract_url` | on |
| `MEMCUE_HOST` | name of this machine for fact scope and the recall tie-break; set it where the host name is unstable, such as a container | `os.Hostname()` |
| `MEMCUE_RECALL_LOG` | `1` logs what each prompt got; the log keeps prompt text | off |
| `MEMCUE_MIN_COVERAGE` | share of a prompt's terms a fact must hold; above 0.25 starts losing facts of other repositories; with `MEMCUE_RERANK=judge` it gates the words' own answer and the fallback only | 0.15 |
| `MEMCUE_NUMBER_TERMS` | `1` indexes versions, dates, ports and addresses as one exact term (`1.27` becomes `nm1x27`, which never matches `1.20`), so a prompt that names a version finds the fact naming it instead of being silenced: 0 of 11 version-shaped prompts before, 10 of 11 after, no change on 26 ordinary questions ([docs/research/number-terms-2026-10.md](docs/research/number-terms-2026-10.md)) | off |
| `MEMCUE_MAX_FACTS` | facts one prompt may get; the cut is applied after the ranking, so a larger value adds context, not coverage: on the 68 prompts that needed memory a third fact left the number with a useful fact unchanged, 58 of 68 either way ([docs/research/max-facts-2026-10.md](docs/research/max-facts-2026-10.md)) | `2` |
| `MEMCUE_EVENT_TTL_DAYS` | days a fact of `kind: event` stays recalled; `0` keeps event facts without an expiry | `7` |
| `MEMCUE_DREAM` | `1` runs a dream after an extraction once an hour | off |
| `MEMCUE_VET` | `0` stops deciding the facts a session extracted; otherwise they are decided right after storing them and before the dream, on `MEMCUE_API_URL` or `claude -p` | on |
| `MEMCUE_VET_POLICY` | `provisional` (default) turns `unsure` into provisional facts; `quarantine` keeps the human queue | `provisional` |
| `MEMCUE_VET_TTL_DAYS` | how long a provisional fact is recalled | `14` |
| `MEMCUE_MEMORY_DIR` | directory of the built-in memory notes the mirror rule checks | unset: the rule is skipped |
| `XDG_STATE_HOME` | where `memcue/recall.log`, `memcue/extract.log`, `memcue/vet.log`, `memcue/card.log`, `memcue/judge.pause`, per-session read offsets and the facts each session got are kept | `~/.local/state` |

With `MEMCUE_RERANK=judge` and the Jev endpoint configured, the judge reads the first `MEMCUE_JUDGE_RERANK_CANDIDATES` facts the words found, in the search's BM25 order,
whether the lexical gate (`MEMCUE_MIN_COVERAGE` and the number of matched terms) let them pass or not: the gate was
tuned for words alone. Recall does not read vectors: on a gold set of 180 prompts, twenty facts in BM25 order gave a
useful fact to 119–124 prompts, as many as the vector candidates and the judge together (121–124), with noise of 17–23
facts against 17–18 ([docs/research/one-model-2026-10.md](docs/research/one-model-2026-10.md)). `memcue embed` and
`MEMCUE_EMBED_*` are removed (#250). Opening a base deletes the vectors an older memcue stored and compacts the
file once (#260); the empty `fact_vectors` table stays, so the schema and older binaries are unaffected.

Without `MEMCUE_RERANK` the words decide, even with the Jev endpoint configured for the dream (#331); so does a value
memcue does not know. The words serve the facts that pass the gate in the search's BM25 order, approved before
provisional; the coverage share only gates. On 409 blind-labelled Claude Code prompts where the two orders differ,
BM25 order gave a useful fact to 205 prompts against 180 for a coverage sort, with 30% noise against 41% (#338,
[docs/research/search-chain-2026-10.md](docs/research/search-chain-2026-10.md)). After 0.6.0 a setup that relied on the implicit judge needs `MEMCUE_RERANK=judge`. `local`, the
built-in tree reader of 0.6, is removed (#324) and is such a value now: end to end, on 40 real prompts in `claude -p`,
it scored as the words do (0.95 against 0.96; the judge 1.01)
([docs/research/local-readers-2026-10.md](docs/research/local-readers-2026-10.md)).

With `MEMCUE_RECALL_LOG=1`, `recall.log` has one JSON line per prompt: the session, the host, the first 500
characters of the prompt and the facts it got, if any, with their coverage, scope and whether a fact of another host
displaced one of this host; `"judge":"paused"` marks a prompt the judge never read because the shared breaker had
paused it after a failure, so the words alone decided. It stays on your machine and is what you judge recall by; it is off by default because it
keeps the text of your prompts. When a fact you expected did not come, `memcue recall -fact ID "prompt"` names the step
that lost it: the search (not among its results), the lexical gate (too few terms or low coverage; with a judge,
"below the gate, sent to the judge" means it was read anyway, and "over the judge's candidate limit" that it came too
late in the BM25 order), the judge (score below the minimum) or the cut to the first facts. `extract.log` has one JSON line per extraction run: session, part of the transcript
read, facts stored, and the cost Claude Code reports or the tokens the endpoint reports.

`cost_usd` in `extract.log`, `vet.log` and `dream.log` holds only what a caller reports in dollars: `claude -p` and
the Jev endpoint. A call to `MEMCUE_API_URL` reports tokens, not a price, so its `cost_usd` is 0 and its spend is
`tokens` (`input`, of which `cached` came from the prompt cache, and `output`) times the provider's price; memcue
keeps no price table, since prices change and differ by account (#258).

The extraction prompts (`internal/extract/*.md`) are written in Russian. Facts come out in the language of the
session or note either way.

## Cost

Extraction is the part that costs most. On ten recorded sessions it cost $0.18–1.33 per session on Opus,
about $5 per million characters of transcript; tool output is the bulk of it. Sonnet was half the price but kept
73 % of the facts worth keeping against 88 % for Opus, and nothing at all in two of the ten sessions. DeepSeek
(`deepseek-flash` through `MEMCUE_API_URL`) cost $0.17 for all ten and kept 74 % against 87 % for Opus. Set
`MEMCUE_EXTRACT=0` to keep recall and turn extraction off in every host.

A review is the second model call, and a much smaller one: twelve candidates with a few neighbours each per call, and
no candidate at all on a session that taught nothing. `vet.log` records the tokens of every call, and the cost when
`claude -p` reports one; a rules-only run (`-no-model`) costs nothing. The review uses `MEMCUE_MODEL` and
`MEMCUE_API_URL`, or `claude -p` without it; there is no separate review model. On `api.deepseek.com` the review
turns reasoning off: it agreed with a `claude -p` reference as often and used a quarter of the output, while split,
dream and extraction lost quality without it and keep it
([docs/research/reasoning-off-2026-10.md](docs/research/reasoning-off-2026-10.md)).

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
`memcue retire ID` rejects one. A fact written by `memcue add` is active at
once, and an explicit `correct` of an approved fact writes a new active version. A retired fact cannot be corrected:
retirement is final, so a changed fact is added as a new fact, and `memcue reopen ID` is the way back from a
rejection. The local review page (`memcue review`) is removed (#325): its template alone cost 2.3 MB of the binary. The full rules are in [Use](#use).

`vet` also checks the approved facts that this binary finds suspect: a fact that names a `MEMCUE_*` variable or a
`` `memcue X` `` verb it does not have, a memcue version older than its own (a release build only, either right after
the word or a few words later behind "бинарь", "версия", "сборка", "релиз" or "тег"), or a reminder whose dates have all
passed. Code can find such a fact but cannot tell a stale one from a record of history, so the main model reads each
with what was found and retires those that describe a past state as current. The model is asked at temperature 0 and
every retirement needs a second, independent answer that agrees with the first: a wrong retirement cannot be undone
from the binary, and one bad sample must not take a true fact out of recall. A fact the two answers disagree about is
kept and logged as such. Every fact asked about is remembered with its version and the binary's version in `vet.checked`
in the state directory, so a kept fact is not asked about again until its text or the binary changes. `-no-model` only
lists the suspects; `-dry-run` asks and changes nothing.

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
