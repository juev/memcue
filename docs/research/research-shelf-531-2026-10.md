# Capturing research findings and finding them again, October 2026 (#531)

Status: **exploratory, measured 2026-10-27 on synthetic data only**, memcue at `05cf464` (0.21.1), the binary built
from this tree (`go build ./cmd/memcue`, sha256 `b30c4b50fe4bdedc…`), Claude Code 2.1.301. 232 agent runs and one
deterministic search experiment. Hermes was **emulated, not run** ([why](#arms)); DSH was not run. Nothing here
changes code, defaults or instructions of the product.

Revised twice the same day after independent reviews ([first](#revision-after-review), [second](#second-revision)): **the stand was not a
sandbox**, local path details reached Anthropic, the run records were anonymized after the fact, the runnable stand
was removed (a full rerun from this repository is no longer possible), and every answer and every capture run was
graded a second time by fresh model instances (not a person) and adjudicated by the author.

In one paragraph: **with today's text, Claude Code stored none of 30 labelled findings from outside sources (0 of 12
runs), and in later sessions with no cue or only "we researched this" (R2, R4, R8) it never reached the research shelf
(0 of 9 Opus runs). The Hermes emulation stored 15 of
30 on the shelf but, in a new session over what it had stored itself, never read the shelf (0 of 3; once it asked for
`all,shelf`, which memcue silently reads as `all`). A scoped instruction-only candidate stored 30 of 30 in both hosts,
reached the shelf in every run that needed it (18 of 18 in each host) and returned the agent's own stored findings in
all 6 chained runs. Its cost: more stored facts in Claude Code than in Hermes (49 against 32 over the same 24 capture
runs; 17 of the 49 cover nothing the labels asked for), one pitfall misfiled onto the shelf, and about 15% more cost
per Claude Code run.** The candidate changed where the agent looked more than how well it answered: on the second
grading the candidate arms gave exactly as many fully correct recall answers as the base arms (Opus, 27 recall runs
per arm: 18 in both Claude Code arms, 17 in both Hermes arms). What changed is that "I don't know" answers turned into
answers from the shelf, and many of those carried advice the sources do not support (a one-partition Quarrel queue),
so they count as partial. Across all 232 runs one answer is wrong (`hm-cand__C1w__r1` misreports a source), 58 are
partial and 17 unresolved; the first grading had called none wrong. Two code defects surfaced on the way (`-in
all,shelf` reads no shelf; `correct ID -- TEXT` stores the `--`). These are counts on 17 invented scenarios written by
the same agent that wrote the candidate text, from a stand that was not a sandbox: they show a direction and the
mechanism behind it, not a rate for real work.

Contents: [findings log](#intermediate-findings-log) · [questions](#questions) ·
[integrations today](#what-the-integrations-do-today-05cf464-0211) · [method](#method) · [results](#results) ·
[defects, hypotheses, intent](#observed-defects-hypotheses-and-intended-behaviour) · [options](#options) ·
[recommendation](#recommendation) · [open decisions](#unresolved-design-decisions) · [limits](#limits) ·
[confounds](#confounds-not-resolved) · [isolation check](#isolation-check) · [what can be repeated](#what-can-be-repeated)
· [what was not done](#what-was-not-done) · [revision after review](#revision-after-review)

## Intermediate findings log

Kept in the order they were found, as they were written at the time; later sections refer to them by number.

1. *(code reading, 05cf464)* Only the Hermes tool schemas mention the shelf: `memcue_add.shelf` ("For what was read
   from outside sources") and `memcue_search.in` (`shelf:research`). The Hermes system-prompt block, the Claude Code
   session-start note, the Claude Code `memcue` skill and the DSH plugin say nothing about it. Since #494 removed the
   extraction, nothing else writes the shelf: a fact reaches it only if the model passes `-shelf`/`shelf` on its own.
2. *(code reading)* `memcue add` refuses `-kind other`; a research finding stored by the model has no kind unless it
   picks one of rule, decision, pitfall, environment, procedure, event. Every shelf fact in the runs had none.
3. *(Experiment A, observed)* The shelf hint comes only with an empty answer. Ten fixed queries over the seeded base:
   in Hermes and in the CLI outside a repository, 6 of 10 got a non-empty answer made of unrelated ordinary facts and
   no hint, while the shelf held the relevant fact (the motivating case of #531, reproduced on synthetic data). In a
   repository (the Claude Code default `project,host,global`) 7 of 10 came back empty, with both hints.
4. *(Experiment A, observed)* The hint counts lexical matches, not relevance: "Kestrel runner log build-07" in a
   repository got "1 fact on the shelf matches" for the Ostrich benchmark fact, which shares only "log".
5. *(pilot, 2 runs)* With the candidate text, Claude Code stored three findings on the shelf with sources and marked
   the vendor benchmark as a claim; the Hermes emulation with the current text stored nothing on the same scenario.
   Two runs were a smoke test of the stand, not a result. (In the full grid that Hermes cell stored one fact in 2 of 3.)
6. *(first grid, design flaw found)* In C1–C3 the sources were files committed to the workspace repository. The
   Claude Code text says "do not store what the repository … shows", so a baseline that stores nothing there may be
   following its rule, not failing it. C1w and C2w were added after the first grid: the same pages, served by a
   `web_fetch` tool (a local stand-in, no network) and absent from the repository. C1–C3 are kept and reported, but
   read with this confound. (Result: the Claude Code baseline stored nothing in C1w/C2w either, so the confound does
   not explain it.)
7. *(first grid, observed side defect)* An agent ran `memcue correct -keywords … ID -- "text"` (the `add … -- TEXT`
   form) and the stored fact began with `-- `. Reproduced without a model: `memcue correct ID -- TEXT` exits 0 and
   stores `-- TEXT`; `memcue correct -- ID TEXT` stores `TEXT` (`runCorrect` joins `fs.Args()[1:]`,
   `cmd/memcue/facts.go:365`). It hit 4 of the 6 CLI corrections in the runs. Not part of #531; noted for a separate
   issue.
8. *(first grid, grading)* The rule pre-labels disagreed with a reader on 5 of 8 deliberately hard cases and on
   visibly wrong runs (an "I can't tell" answer pre-labelled correct), so every run's answer was read by hand.
9. *(first grid, observed)* The Hermes `memcue_add` text has no "what the repository shows" exclusion, the Claude Code
   text has one. On C3, where the cause is written in a notes file of the repository, the Hermes emulation stored the
   pitfall 3 of 3, Claude Code 0 of 3. Which of the two is intended is an open decision, not a finding of this issue.
10. *(chain, observed)* A shelf fact the agent had stored itself was missed once because it scored below the default
    0.6 floor of the best shelf fact (checked: `-min-score 0` returns it). The Hermes below-floor hint fires only when
    one fact or none comes back; here two did, so nothing said a third existed.
11. *(chain, observed and reproduced)* `-in all,shelf` (and any `-in` list containing `all`) silently leaves the shelf
    out: `internal/recall/search.go:94-97` returns at the first `all` with the shelf excluded and drops the rest of the
    list, without an error or a hint. `global,shelf:research` and `project,host,global,shelf:research` do read it. The
    Hermes `in` description lists `all` and `shelf` side by side; the Hermes arms asked for `all,shelf` 6 times in 6
    runs. In 4 of those runs (recall) the agent later searched `shelf` alone and found the fact; in the chain run it
    did not, and answered that it had nothing; the sixth was a search before adding a pitfall, with nothing on the
    shelf to find.
12. *(review, records)* The stand was not a sandbox: one agent ran `find /` and two ran `hostname`, so local path
    details and the host name went to Anthropic and into the records. The records were anonymized and the runnable
    stand removed ([revision](#revision-after-review)).
13. *(review, second grading)* Graded again by five fresh model instances that did not see the first labels, then
    adjudicated: one answer is wrong and 47 more drop from correct to partial. 38 of the 48 changes are one issue —
    advice to give a Quarrel queue a single partition, a setting the Quarrel page never names. Finding 8's "read by
    hand" had missed it, and had missed a misreported source.
14. *(review, `init` events)* Three plugins built into Claude Code (`cc-plugin-agents-md`, `cc-plugin-telemetry`,
    `cc-plugin-plugin-authoring`) were loaded in every run. The first version of this report said "no user plugin";
    that was true of user plugins and silent about these. Their effect is not known
    ([confounds](#confounds-not-resolved)).

## Questions

From #531, narrowed to what a stand can answer:

1. **Capture.** Does an agent store an independently useful finding it reads in outside material, does it put it on
   the research shelf, does it name the source and the weight of the claim, and does it leave out the unsupported, the
   ephemeral and the already stored? Does a more explicit instruction change that, and at what cost in noise?
2. **Recall and use.** When a later task needs such a finding, does the agent search, does it name `shelf:research`,
   does the search return the fact, does the agent check it against the material at hand, and does the answer use it
   correctly? Each step is counted on its own, so a missed search, a missed shelf, a ranking miss and a wrong use are
   told apart.
3. **Integrations.** How do Hermes and Claude Code differ in defaults and in the text the model gets, and how far does
   a result carry to DSH and others?

The design decisions #531 asks to keep are taken as given: one research shelf, the shelf out of ordinary search,
the model decides what to store and when to search, no search on every tool result, no second model, no new
permanent evaluation system.

## What the integrations do today (05cf464, 0.21.1)

Read from the code, then checked by Experiment A where marked.

| | Claude Code | Hermes | DSH |
|---|---|---|---|
| Search default scope | CLI default: `project,host,global` in a repository, `all` elsewhere (`internal/recall/search.go:81-90`) | provider adds `-in all` when the model names no `in` (`plugins/hermes/memcue/__init__.py:890-896`) | CLI default, no `-in` added (`plugins/deepseek-harness/src/index.mjs:465-467`) |
| Shelf in the default | no | no (`all` excludes shelves, `search.go:94-97`) | no |
| Shelf hint | text and `-json` hints, empty answer only (`cmd/memcue/facts.go:622-636`) — observed | `hint` field, empty answer only — observed | `hint` field, empty answer only (read, not run here) |
| Text that names the shelf | none: session-start note (`cmd/memcue/session.go:24-29`) and `memcue` skill are silent | tool schemas only: `memcue_add.shelf` "For what was read from outside sources", `memcue_search.in` lists `shelf:research`; the system-prompt block is silent | none; `memcue_add` has no `shelf` parameter |
| Capture text | rules, corrections, decisions, pitfalls, environment; "do not store … what the repository … shows … your own guesses" | the same kinds in the system prompt and `memcue_add`; excludes progress, "what a command shows", installation state — **not** what the repository shows (finding 9) | the same kinds; "do not store … what the repository or a command shows" (`index.mjs:36-39`) |
| Recall-during-work text (#521/#522) | in the session-start note | in the system prompt and `memcue_search` | absent |

Two boundaries follow from the table and were confirmed in Experiment A:

- **The hint is the only bridge from ordinary search to the shelf, and it fires only on an empty answer.** Hermes
  searches `all`, which comes back non-empty whenever any ordinary fact shares a word (8 of 10 queries on this stand's
  12-fact base; a real base has thousands), so a Hermes model that does not already know about the
  shelf learns about it only from the schema text. Claude Code's repository default is narrower and comes back empty
  more often, so it sees the hint more often — together with a second hint to widen to `-in all`, whose answer may
  then be non-empty and hint-less.
- **Claude Code has no route to the shelf at write time.** Nothing in its instructions says `-shelf`; a Claude Code
  model stores a finding on the shelf only if it reads `memcue add -h`.

## Method

What is kept in [`scripts/capture531/`](scripts/capture531/): the fixtures, every run's compact record (anonymized,
see below), the first-pass manual labels, the second-pass labels and the grader. The scripts that built the stand and
ran the agents were **removed after the review** ([why](#revision-after-review)); this section describes what they
did, so that the results can be read, not so that they can be rerun. Nothing kept is a service or a permanent
harness.

### Data and isolation

- **Synthetic fixtures.** Every system, host, repository and number in the fixtures is invented (Brindle, Quarrel,
  Larkspur, Ostrich, Vellum, Kestrel, Tandem, build-07, mq-02). No real prompt, transcript or fact was used as input.
- **Not a sandbox.** Each run had its own directory with a copy of the seeded base (`MEMCUE_FACTS`), its own memcue
  state and XDG data and state directories, and the workspace as a git repository with a fake
  `example.com/acme/<name>` remote. That kept memcue's reads and writes in the run directory. It did not confine the
  agent: `claude -p` ran as the user, with the user's real home directory and Claude Code configuration directory,
  the rest of the user's environment inherited, and `--permission-mode bypassPermissions`. A short deny list (`ssh`,
  `scp`, `curl`, `wget`, `sqlite3`, `claude`, `git push`, `sudo`, WebFetch, WebSearch) was the only restriction on
  Bash. The agents did use that freedom: one run (`cc-cand__R8__r2`) ran `find /` over the whole filesystem and got
  back paths in the repository checkout that held the fixtures, two (`cc-cand__R7__r1`, `r2`) ran `hostname` on the
  real host, and several read and wrote files by absolute path. No run read the fixtures' labels or reference answers
  (checked: no tool input names the fixture or result files; the `find` above listed but did not open them).
- **Claude Code settings.** `--setting-sources ""`, `--strict-mcp-config`, `--no-session-persistence`,
  `CLAUDE_CODE_DISABLE_AUTO_MEMORY=1` and, in the Claude Code arms, the plugin from this tree via `--plugin-dir`.
  Every run's `init` event lists **three more plugins**, in all four arms: `cc-plugin-agents-md`,
  `cc-plugin-telemetry` and `cc-plugin-plugin-authoring`, with path `builtin`. They are bundled in the Claude Code
  2.1.301 binary, not installed by the user, and were present in all 232 runs and all four arms. What they may have
  changed is not known; see [confounds](#confounds-not-resolved). The `init` event lists no user MCP server.
- **Live bases.** The stand pointed memcue at scratch bases; the live Hermes and Claude Code bases were checked only
  by a before/after count, which shows less than it seems ([Isolation check](#isolation-check)).
- **What reached Anthropic.** Everything the agent saw went to Anthropic through `claude -p`: the prompts, the
  fixtures, the memcue text and every tool call and result. That included **local path details**: the stand's
  absolute paths (which name the user's home directory) in the system context and in most tool results, the
  repository checkout path and a listing of its fixture directory (the `find /` run), and the machine's real host name
  (the two `hostname` runs). Claude Code's own analytics were not disabled in these runs (`analytics_disabled: false`
  in the stream), and the built-in `cc-plugin-telemetry` was loaded; what they sent and where was not checked. Anthropic
  was the model provider of all four arms, the Hermes emulation included. No credential and no memory fact of the user
  appears in the records; the records do not show the system prompt, so that part is not verified.
- **Anonymized records.** The kept records replace the repository path with `<REPO>`, the stand root with `<STAND>`
  and the host name with `<real-hostname>`; the synthetic part of each path (`runs/<arm>/<scenario>/r<N>/ws`) is kept,
  so the runs stay apart. The replacement changed no mechanic or label (the grader's table before and after is
  identical in every field). `grade.py privacy` counts four shapes of local identifier (an absolute path under `/opt`,
  `/home`, `/Users`, `/root`, `/srv` or `/mnt`; the stand directory name; a worktree directory; an e-mail address
  outside `example.com`) in `results/`, `fixtures/`, `testdata/` and this report, and prints file, shape and count,
  never the matched text. On a scratch copy of the raw records it reported 315 matches; on the published files it
  reports none. That is a narrow check, not evidence that nothing private is left: it does not look for a bare host or
  user name, an IP address, a token, a person's name or a path outside those roots. The host name was replaced by
  value, and a one-off search of the published files for it found no occurrence; nothing else was searched for.
- **Fixture domains are not reserved.** The web pages are filed under `docs.brindle.dev`, `quarrel.io` and
  `ostrich.dev` (the other two use the reserved `.example` and `example.com`). Those names may belong to real third
  parties; the pages are invented, offline fixtures that the local `web_fetch` stand-in served from `fixtures/web/`
  ([note](scripts/capture531/fixtures/web/README.md)). The URLs appear in the records in 67 `web_fetch` calls, 16
  stored facts and 10 Bash commands; the deny list blocked WebFetch, `curl` and `wget`, not every way to reach the
  network, and the 10 commands were not checked one by one. Do not fetch these URLs.

### Arms

| Arm | Agent loop and model | Memory text and tools |
|---|---|---|
| `cc-base` | Claude Code 2.1.301, Opus 5.5 | the plugin as shipped: three SessionStart hooks (`memcue session-start -part 1..3`), skills `memcue` and `memcue-dream`, `memcue` on `PATH` |
| `cc-cand` | same | the same plus the candidate text: a fourth SessionStart hook adds [`candidate-cc-note.md`](scripts/capture531/fixtures/candidate-cc-note.md) to the note and the skill gets [`candidate-cc-skill.md`](scripts/capture531/fixtures/candidate-cc-skill.md) appended |
| `hm-base` | Claude Code 2.1.301, Opus 5.5 — **an emulation of Hermes, not Hermes** | no Claude Code plugin; the system prompt gets the Hermes provider's own `system_prompt_block()` (with its session card) and the memcue tools are the provider's schemas and `handle_tool_call` behind a small MCP server (removed with the stand), so the `in: all` default, the hints and the tool text are those of Hermes |
| `hm-cand` | same | the same block plus [`candidate-hermes.md`](scripts/capture531/fixtures/candidate-hermes.md) |

The candidate texts are one scoped instruction-only option, the same in substance for both hosts: store
findings from outside sources on the research shelf with their source and the weight of the claim, search the shelf
before adding, search the shelf when a task returns to a researched subject, and check a shelf fact against the
material at hand. They change no code and no default.

**Why Hermes is emulated.** A real Hermes run needs a model provider; this host's Hermes authenticates with an OAuth
credential whose refresh token rotates, so a second Hermes home with a copy of it could log the live gateway out, and
running in the live home would use the live base and state. Neither is allowed here. The emulation keeps what Hermes
sends about memory (system block, tool names without the MCP prefix, descriptions, parameters, defaults, hints) and
replaces the agent loop and the model (Hermes on this host runs another vendor's model). Tool names reach the model as
`mcp__memcue__memcue_search` and so on. The emulation's results describe Claude models reading Hermes's memory text,
not Hermes.

### Scenarios

Fixed and labelled before any run in [`fixtures/scenarios.json`](scripts/capture531/fixtures/scenarios.json), over
the facts of [`fixtures/seed.json`](scripts/capture531/fixtures/seed.json): seven ordinary facts (noise and controls)
and five `shelf:research` facts, one of them stale.

| ID | Kind | What it tests | Labelled outcome |
|---|---|---|---|
| C1 | capture, research | compare two queue libraries from four files in `./sources` (docs, docs, vendor benchmark, forum post) | store D1 Brindle's silent drop, D2 Quarrel's per-partition order, D3 the benchmark's weakness; not the forum claim, not a "decision" |
| C1w | capture, research | C1 with the four sources as web pages (`web_fetch`), none in the repository | as C1 |
| C2 | capture, limitation | is a compressor fit for binary archives, from a benchmark page and a README in `./sources` | store D1 the benchmark covered only text logs under 1 MB, D2 the dictionary is needed to decompress |
| C2w | capture, limitation | C2 with web pages | as C2 |
| C3 | capture, troubleshooting | a script fails; the cause is in a notes file and the config | the pitfall (batch over 4096) — but see findings 6 and 9 |
| C4 | capture, duplicate | the Brindle finding again, already on the shelf | no second copy |
| C5 | capture, stale | the changelog now says HTTP/3 exists; the shelf says it does not | correct or retire the stale fact |
| C6 | capture, control | count TODO comments | store nothing |
| R1 | recall, explicit cue | "Brindle or Quarrel? We looked into both earlier" | find S1, S2 on the shelf; answer from them |
| R2 | recall, no cue | the same question without "earlier" | as R1, or say there is nothing to go on |
| R3 | recall, empty ordinary answer | "We looked into Ostrich before": ordinary search has nothing, the shelf has S4 | find S4, apply its limitation |
| R4 | recall, stale vs current | the workspace has the 2.0 changelog; the shelf has the stale 1.8 fact | answer from the current material |
| R5 | recall, no answer anywhere | a Quarrel setting nothing records | say it is unknown, invent nothing |
| R6 | recall, control | an awk one-liner | no memory needed |
| R7 | recall, ordinary fact | a host fact (not this host) | find O2; in a repository this needs `-in all` |
| R8 | recall, stale, no current material | "We researched this": the shelf has the 1.8 fact; `go.mod` pins v2.0.1 | report the finding as possibly outdated, ideally noticing v2.0.1 |
| R9 | recall, explicit shelf | "Check the research shelf" | find S3 |

Opus 5.5 ran every scenario three times in each of the four arms (17 × 4 × 3 = 204 runs). Two additions:

- **Chain** (12 runs): R2 again, in a new session of the same arm, over the base that arm's own C1w run left behind
  (repeat *n* reads the base of C1w repeat *n*). This is the only place where what is recalled is what the agent
  itself stored, not a seeded fact.
- **Sonnet 5.5 subset** (16 runs): C1w, C2w, R2, R8 × `cc-base`, `cc-cand` × 2, to see whether the direction depends
  on the model.

### Grading

- **Mechanics from the record, not from a model**: a search is a `memcue search` command or a `memcue_search` call;
  the shelf is read when its `-in`/`in` names a shelf and no `all` (a list with `all` reads no shelf, finding 11); a
  target fact is retrieved when its seeded ID appears in a tool result; a hint is seen when a tool result contains "on
  the shelf match"; what was stored is the diff of the run's base before and after.
- **Capture items** are pre-labelled by regular expressions over the stored facts and then checked by reading every
  new or changed fact. One override was needed (a pitfall stored on the shelf, counted as a placement error). A fact
  that covers no labelled item is counted as "extra": on-topic detail the labels did not ask for, or noise. The counts
  in this report are the second pass's reading of each fact (`facts_second`), not the pre-labels: the pre-label took
  four Quarrel back-pressure facts for D2 and counted 13 extras where the second pass counts 17.
- **Answers** are pre-labelled by rules and then **read by hand, every run**. Labels: correct, partial, unresolved
  (says it does not know, invents nothing), wrong (a false claim or a stale fact applied as current). R8 is correct only
  if the answer notices that the workspace pins a newer version; a general "this may be outdated" is partial.
- **Validation of the rules.** `grade.py selftest` runs 24 crafted cases (negations, paraphrases, empty and no-verdict
  answers) that the rules label as intended, and 8 cases written to break them, where the rules agree with a reader on
  only 3. That is why the rules are only pre-labels.
- **First pass (kept as written).** The author read every answer and every stored fact; the manual label differs from
  the rule's in 29 of the 232 runs. Those labels are in [`results/manual.json`](scripts/capture531/results/manual.json)
  and are not edited. That pass called no answer wrong, and the review showed it was too lenient (finding 13).
- **Second pass.** Five fresh Claude Opus 5.5 instances, one per group of scenarios, graded all 232 answers and every
  stored or changed fact of the 104 capture runs from the full records and the source files, without seeing the first
  labels or this report. Their rubric fails closed: a substantive claim not supported by what the agent had (sources,
  tool results, retrieved facts) makes an answer partial, or wrong if the conclusion rests on it or a source is
  misreported; a missing or truncated answer is unreviewable. None was: every record holds the full final answer, no
  tool result reaches the 4,000-character cap of the record, and every run exited 0.
- **Adjudication.** The author settled every disagreement and wrote the reason into
  [`results/regrade.json`](scripts/capture531/results/regrade.json) next to both labels. The first and second pass
  disagree on 37 runs; the final label differs from the first in 48 (47 correct → partial, 1 correct → wrong; none
  went up). Against the second pass the author ruled in 35 runs, in two groups only: 12 R5 answers that say the value
  is unknown are correct, because unknown is R5's reference answer (the second pass used the generic "unresolved");
  and 23 recall answers with unhedged one-partition advice are partial, the rule the second pass applied to C1/C1w but
  not to R1/R2. All one-partition calls were checked by reading every Brindle/Quarrel answer.
- **Fail-closed final label.** `grade.py` takes the adjudicated label only if the answer text it was given still
  matches the record (sha256); otherwise the run counts as `unverified`. That hash, `answer_sha256`, covers the answer
  text only, not the label: an edit to `final` in `results/regrade.json` keeps the hash valid and is taken as the
  final label. The answer binding detects a changed answer, not a changed verdict. The adjudicated capture coverage counts only
  if `capture_sha256`, the sha256 of the canonical JSON of the run name, the record's `fact_changes`, `facts_second`
  and `cov_final`, still matches, and `facts_second` names exactly the record's changed facts; otherwise the run's
  coverage is unverified and counts as storing nothing. Today all 232 answers and all 104 capture runs verify.
  `capture_sha256` was added in the [second revision](#second-revision), computed from the records as kept: it detects
  any later change to the facts or to their adjudication, and does not by itself prove which bytes the second pass
  read. What ties the adjudication to these facts is that the second pass listed every fact by ID and the IDs match
  the records in all 104 runs.
- **What the graders could not see.** The kept records do not hold the session-start context (the Claude Code hook
  output and the Hermes system block). Ten answers state a seeded ordinary fact (`config/queue.toml` eight times,
  `make lint` twice) that no tool result shows; the author checked in the raw stand files, which are not published,
  that in all ten the fact was in the session context. A reader of the published records cannot check that.
- **How independent.** The second pass is the same model as the author, in a fresh context; its errors may correlate
  with the author's. No person graded anything. The 6 second-pass labels marked low confidence are all among the
  changed ones (R9 ×4, `cc-cand__R7__r1`, `hm-base__C1__r3`).

## Results

All counts are runs or labelled items, exact for these fixtures. A cell of 3 is three repeats of one scenario, not
three independent samples of a population: read a difference that holds in every scenario and every repeat as a
direction, and a difference of one run as noise.

### A. Search mechanics, no model ([`results/mechanics.json`](scripts/capture531/results/mechanics.json))

Ten fixed queries over the `rec` base (seven ordinary facts, five shelf facts), through the CLI from the Tandem
repository (the Claude Code default), the CLI from a plain directory, and the real Hermes provider
(`handle_tool_call`). The queries and what each probes were fixed before the run (they are in the `query` and `why`
fields of `mechanics.json`; the generator was removed with the stand); whether a shelf match is relevant was labelled by hand from the
fact texts afterwards: q1 S1–S3, q2 S4, q3 S5, q6 S1, q7 S1, q8 S5, q9 S4, q10 S4 relevant; q4 and q5 have no relevant
shelf fact.

| Path | Hint shown, shelf had a relevant fact | Hint shown, shelf had none | Relevant shelf fact, no hint |
|---|---|---|---|
| CLI in a repository (Claude Code default) | 5 | 2 (q4 "Quarrel retry backoff", q5 "Kestrel runner log build-07") | 3 (q3, q6, q8: a global ordinary fact made the answer non-empty) |
| CLI outside a repository (`all`) | 2 | 0 | 6 |
| Hermes provider (`in: all`) | 2 | 0 | 6 |
| *Simulated: a hint on every shelf match* | *8* | *2* | *0* |

The last row is arithmetic over the same data, not a run of changed code. What it says: a hint that also came with
non-empty answers would point to the shelf in all 8 queries that had a relevant shelf fact, and in 2 that had only a
lexical match. The Hermes `in: shelf:research` answer additionally carried the below-floor hint in 3 of 10 queries.

### B. Capture (Opus, 3 repeats per cell)

Labelled findings stored, out of the opportunities (C1, C1w: 3 per run; C2, C2w: 2; C3: 1):

| Scenario | `cc-base` | `cc-cand` | `hm-base` | `hm-cand` |
|---|---|---|---|---|
| C1 sources in the repository | 0/9 | 9/9 | 4/9 | 9/9 |
| C1w sources as web pages | 0/9 | 9/9 | 6/9 | 9/9 |
| C2 sources in the repository | 0/6 | 6/6 | 1/6 | 6/6 |
| C2w sources as web pages | 0/6 | 6/6 | 4/6 | 6/6 |
| **Outside sources, total** | **0/30** | **30/30** | **15/30** | **30/30** |
| Runs of C1–C2w that put anything on the shelf | 0/12 | 12/12 | 8/12 | 12/12 |
| C3 pitfall documented in the repository | 0/3 | 2/3 (one of them filed on the shelf) | 3/3 | 3/3 |

What was stored, C1–C3, all repeats:

| | `cc-base` | `cc-cand` | `hm-base` | `hm-cand` |
|---|---|---|---|---|
| New facts | 0 | 49 | 11 | 32 |
| … on `shelf:research` | 0 | 48 | 8 | 29 |
| … shelf facts naming their source | — | 48/48 | 7/8 | 29/29 |
| … covering no labelled item (extra) | 0 | 17 | 0 | 0 |
| Vendor benchmark stored / stored as a claim (one run, unverified) | 0 / — | 6 / 6 | 0 / — | 6 / 6 |
| Forum claim "never loses a message" stored as fact | 0 | 0 | 0 | 0 (once recorded explicitly as anonymous and unsupported) |
| A choice stored as the user's decision | 0 | 0 | 0 | 0 |
| Misplaced (repository pitfall on the shelf) | 0 | 1 | 0 | 0 |

The other capture scenarios:

| | `cc-base` | `cc-cand` | `hm-base` | `hm-cand` |
|---|---|---|---|---|
| C4 duplicate added (S1 already on the shelf) | 0/3, **no search at all** | 0/3, shelf searched 3/3 | 0/3, **no search at all** | 0/3, shelf searched 3/3 |
| C5 stale shelf fact corrected | 0/3 (no search) | 3/3 (all 3 with the `-- ` prefix of finding 7) | 0/3 (no search) | 3/3 |
| C6 anything stored on a task with nothing durable | 0/3 | 0/3 | 0/3 | 0/3 |
| Task answers C1–C6 (24 runs): correct, partial, unresolved, wrong | 22,2,0,0 | 18,6,0,0 | 19,5,0,0 | 22,1,0,1 |
| … first pass | 24,0,0,0 | 24,0,0,0 | 24,0,0,0 | 24,0,0,0 |
| … with the one-partition advice set aside | 24,0,0,0 | 23,1,0,0 | 23,1,0,0 | 23,0,0,1 |

The coverage counts are the second pass's, which agrees with the first on every item but one: `hm-base__C2__r3`
stored that the benchmark covered only small text logs (D1) and called Ostrich a "dictionary compressor", which the
first pass's pattern took for D2 (the dictionary is needed to decompress); it is not, so `hm-base` drops from 16 to
15 of 30. The task answers of C1–C6 were 24/24 correct in every arm on the first pass. On the second, the C1/C1w
answers lose most: 12 advise a one-partition Quarrel queue without saying the docs name no such setting (they say
"64 partitions by default" and document only `partition_key`), `hm-base__C1__r3` says the forum and the benchmark
"tell you nothing about ordering or loss" (the forum claims both), and `cc-cand__C4__r3` says Brindle throws away messages "it hasn't delivered yet" (it drops
delivered, unacknowledged ones). The one wrong answer is `hm-cand__C1w__r1`: it says of the forum and the benchmark
"Neither says anything about ordering or loss anyway", while the forum post reads "It never loses a message and it's
ordered", and its conclusion "Quarrel fits, with one setting you have to change" rests on the one-partition setting;
the shelf fact the same run stored records the forum claim correctly.

Read with care:

- `cc-base` searched before or during C1w, C2, C2w and C3 in every run, so the memory was in use; it never stored. The
  web variants rule out finding 6 as the explanation. Its text names rules, decisions, pitfalls and environment, and
  excludes "what the repository … shows" and "your own guesses"; a finding from a document is none of the named
  kinds. The base behaviour is consistent with the text, not a failure to follow it.
- `hm-base` stored on the shelf in 8 of 12 runs without any shelf line in its system prompt: the `shelf` parameter's
  description ("For what was read from outside sources") is the only text that could have led it there. It stored one
  combined fact per run and never the vendor benchmark.
- C4 "no duplicate" in the base arms is not duplicate handling: nothing was searched and nothing was stored.
- The extras of `cc-cand` are on-topic details of the same pages (the WAL interval, the single-writer condition,
  at-least-once redelivery, Quarrel's publisher back-pressure), each a separate fact. They are noise only for a later shelf search, since the shelf is out
  of ordinary search.

### C. Recall and use (Opus, seeded base, 3 repeats per cell)

Target retrieved (its seeded ID in a tool result) / final answer correct, partial, unresolved, wrong. Retrieval and
the answer are separate outcomes: a run can miss the target and still answer correctly from the workspace (R4), or
retrieve it and answer partially (R1).

| | `cc-base` | `cc-cand` | `hm-base` | `hm-cand` |
|---|---|---|---|---|
| R1 explicit cue, Brindle vs Quarrel | 3/3 · 0,3,0,0 | 3/3 · 1,2,0,0 | 3/3 · 0,3,0,0 | 3/3 · 0,3,0,0 |
| R2 same question, no cue | **0/3 · 0,0,3,0** | 3/3 · 0,3,0,0 | 2/3 · 0,2,1,0 | 3/3 · 1,2,0,0 |
| R3 ordinary answer empty | 3/3 · 3,0,0,0 | 3/3 · 3,0,0,0 | 3/3 · 3,0,0,0 | 3/3 · 3,0,0,0 |
| R4 stale shelf fact, current changelog at hand | **0/3** · 3,0,0,0 | 3/3 · 3,0,0,0 | **0/3** · 3,0,0,0 | 3/3 · 3,0,0,0 |
| R5 nothing records the answer | — · 3,0,0,0 | — · 3,0,0,0 | — · 3,0,0,0 | — · 3,0,0,0 |
| R6 control, no memory needed | — · 3,0,0,0 | — · 3,0,0,0 | — · 3,0,0,0 | — · 3,0,0,0 |
| R7 ordinary host fact | 3/3 (O2) · 3,0,0,0 | 3/3 · 2,1,0,0 | 3/3 · 3,0,0,0 | 3/3 · 3,0,0,0 |
| R8 stale shelf fact, newer version in `go.mod` | **0/3 · 0,0,3,0** | 3/3 · 1,2,0,0 | 3/3 · 0,3,0,0 | 3/3 · 0,3,0,0 |
| R9 "check the research shelf" | 3/3 · 3,0,0,0 | 3/3 · 2,1,0,0 | 3/3 · 2,1,0,0 | 3/3 · 1,2,0,0 |
| **Shelf target retrieved, R1–R4, R8, R9** | **9/18** | **18/18** | **14/18** | **18/18** |
| **Answers, all 27 runs** | 18,3,6,0 | 18,9,0,0 | 17,9,1,0 | 17,10,0,0 |
| … first pass | 21,0,6,0 | 25,2,0,0 | 23,3,1,0 | 24,3,0,0 |
| … with the one-partition advice set aside | 21,0,6,0 | 23,4,0,0 | 22,4,1,0 | 22,5,0,0 |

Partial answers other than one-partition advice and the R8 caveat of the first pass: four R9 answers say the shelf
holds "one entry on Quarrel" after a search for the benchmark only (it holds two), and `cc-cand__R7__r1` attributes
the host fact O2 to "my research notes". The second pass marked all five low confidence.

Where each miss happened, by step:

- **Failure to search**: none. Every arm searched in every run that needed memory, and no arm searched in R6.
- **Failure to select the shelf**: all 13 retrieval misses of the table. `cc-base` missed 9 of 18 (R2, R4 and R8,
  3 each), `hm-base` 4 of 18 (R2 once, R4 3 times); no run that searched the shelf missed its target. In `cc-base` R2
  the first query, `memcue search "queue library Brindle Quarrel"` from the repository, was answered by the ordinary
  Tandem decision about `config/queue.toml`; the next ones with `-in all` by the two unrelated host and service facts.
  Every answer was non-empty, so no hint came; four or five searches per run, `-min-score 0` and `-expired` included,
  never named the shelf. R8 is the same through the global Larkspur pitfall. This is the #531 motivating case, end to
  end. In R4 both base arms searched once or twice with the default scope, got no hint and never read the shelf
  (6 retrieval misses); the answers are nonetheless correct, from the changelog and `go.mod` in the workspace. In R4
  the missed fact was the stale one, so the miss cost nothing here; it is still a miss of the step being measured.
- **Retrieval or ranking failure**: none on the seeded base once the shelf was really read. Two in the chain: the
  floor of finding 10, and the `all,shelf` request of finding 11. In four `hm-base` grid runs the same `all,shelf`
  request returned nothing from the shelf (the first search in the three R8 runs, the third of four in
  `hm-base__R1__r2`); each time a later search of `shelf` alone found the target.
- **Failure to apply a found fact**: no wrong recall answer, but use was imperfect. In R1 and R2, 18 of the 20 runs
  that found S1 and S2 added advice the shelf fact does not support (a one-partition queue; S2 says "64 partitions per
  queue by default"). R8 is where use was weakest: 8 of the 9 runs that found the stale fact gave it with a general
  age caveat ("covers 1.8 only, check the current changelog") without looking at `go.mod` in the workspace, which pins
  v2.0.1; one (`cc-cand`) noticed it. No run gave the stale fact as current. In R4, where the current changelog was at
  hand, both candidate arms corrected the stale fact (6/6) and both base arms never saw it.
- **Hints**: a shelf hint appeared in 19 runs. In the 15 where it came before any shelf search, a shelf search
  followed in a later call in all 15 (`cc-base` R1, R3, R5 once and R9; `cc-cand` R7 twice; `hm-base` R3). In the other
  4 (`cc-cand` C4 once, R3 three times) the same command had already searched the shelf, as the candidate text says,
  so they show nothing about the hint. `cc-base` saw it because its repository default came back empty.
- **Noise**: R5 (no answer exists) put irrelevant shelf facts in front of the candidate arms 6 times (and `cc-base`
  once, after a hint); none was used as an answer. `cc-cand` ran two shelf searches in R7, where the shelf had nothing
  to give.

### D. Capture, then recall in a new session (chain, Opus)

R2 over the base each arm's own C1w run left behind:

| | `cc-base` | `cc-cand` | `hm-base` | `hm-cand` |
|---|---|---|---|---|
| Own findings stored in C1w | 0 facts (3 runs) | 6–7 per run | 1 per run | 3 per run |
| Shelf read | 0/3 | 3/3 | **0/3** (one run named it as `all,shelf`, finding 11) | 3/3 |
| Own findings returned | 0/3 | 3/3 (all facts) | 0/3 | 3/3 (2, 3, 3 of 3 facts) |
| Answer correct, partial, unresolved, wrong | 0,0,3,0 | 0,3,0,0 | 0,0,3,0 | 0,3,0,0 |
| … first pass | 0,0,3,0 | 3,0,0,0 | 0,0,3,0 | 2,1,0,0 |

`hm-base` is the instructive cell: its capture worked, its recall did not. It searched three times per run, got the
two unrelated ordinary facts each time and no hint, and declined to choose; in one run its third search asked for
`all,shelf`, which returned the same two facts because memcue ignores the shelf in a list with `all` (finding 11).
All six candidate answers found their own facts and are partial: five for the one-partition advice, and
`hm-cand__R2chain__r1` for finding 10 (its Quarrel fact sat below the floor) and for calling Brindle unfit on both
counts when its own fact says Brindle keeps order with a single writer. Retrieval worked in all six; the answer step is
where they fell short.

### E. Sonnet 5.5 subset (2 repeats per cell)

| | `cc-base` | `cc-cand` |
|---|---|---|
| C1w + C2w: labelled findings stored | 0/10 | 10/10 (all on the shelf, all naming the source, the vendor benchmark marked as a claim) |
| R2 + R8: shelf target retrieved | 0/4 | 4/4 |
| R2 + R8: answers correct, partial, unresolved, wrong | 0,0,4,0 | 0,4,0,0 (first pass 2,2,0,0) |
| C1w + C2w: task answers correct, partial, unresolved, wrong | 3,1,0,0 | 2,2,0,0 |

Same direction as Opus on every cell measured. The partial answers are the same kinds as in Opus: one-partition advice
(both C1w partials of `cc-base` and `cc-cand` r12, both R2 of `cc-cand`), the R8 age caveat without `go.mod`, and
`cc-cand__C1w__r11`, which offers an order-ID `partition_key` as strict per-queue order.

### F. Cost, time and text size

`total_cost_usd` and `duration_ms` as `claude -p` reports them (an API-price estimate; these runs were billed to the
machine's Claude subscription). The Hermes arms are cheaper because they carry no Claude Code plugin skills and no
SessionStart hooks; compare arms within a host.

| | `cc-base` | `cc-cand` | `hm-base` | `hm-cand` |
|---|---|---|---|---|
| Mean cost per run, recall / capture | $0.060 / $0.067 | $0.064 / $0.083 | $0.033 / $0.045 | $0.031 / $0.053 |
| Mean cost per run, all 51 | $0.063 | $0.073 (+15%) | $0.039 | $0.041 (+6%) |
| Median duration | 10.7 s | 11.3 s | 10.6 s | 10.2 s |
| memcue calls per run, recall / capture | 3.70 / 1.00 | 2.74 / 3.79 | 1.93 / 1.08 | 1.63 / 2.38 |
| Text added | — | 1,154 bytes to the session-start note (part 1 is 3,061 bytes today), 1,032 bytes to the skill (read only when the skill is invoked) | — | 1,070 bytes to the system-prompt block |

The percentages are of the unrounded means. All 232 runs together: $12.33 (Opus grid $11.03, Sonnet $0.57, chain $0.74). `cc-cand` makes fewer memcue calls in
recall than `cc-base` because the base arm kept searching in R2 and R8: four or five queries each, none reaching the
shelf.

## Observed defects, hypotheses and intended behaviour

**Observed (in runs or reproduced):**

1. Claude Code has no instruction route to the shelf. Its text never names `-shelf` or `-in shelf:research`; with it,
   0 of 40 labelled outside findings were stored (Opus and Sonnet), and in R2, R4 and R8 (no cue, or only "we
   researched this") recall reached the shelf 0 of 13 times. The README describes the shelf as the place the agent itself puts what it read from outside; the Claude Code
   text does not tell it so. This is a gap between documented design and delivered instructions, not a code bug.
2. A non-empty ordinary answer hides the shelf. Reproduced deterministically (Experiment A) and end to end (R2, R8,
   chain): every run that missed the shelf had only non-empty answers and no hint. The narrow hint trigger is
   intentional (#433, #453); its consequence is now observed.
3. `memcue correct ID -- TEXT` stores the literal `-- ` (finding 7); 4 of 6 CLI corrections in these runs hit it.
4. A shelf fact below the 0.6 floor of the best shelf fact goes unsaid when the answer has two or more facts
   (finding 10; once, in the 6 chained runs that read the shelf).
5. The two hosts disagree on "what the repository shows" (finding 9).
6. `-in all,shelf` and every `-in` list with `all` silently drop the shelf (finding 11): a code defect, reproduced
   without a model, hit in 6 searches of 6 Hermes-arm runs and the cause of one miss.

**Hypotheses (not tested here):**

- That a hint on non-empty answers would be followed as reliably as the empty-answer hint (15 of 15 here, where the
  hint came before any shelf search) and would close defect 2 without the instruction; only the mechanics were
  simulated.
- That Hermes with its real model (another vendor's) and agent loop behaves like the emulation.
- That the candidate text causes the extra one-partition advice. More answers carry it in the candidate arms, but
  mainly because more of their answers reach the shelf and answer at all; the base arms gave the same advice when they
  found the same facts (R1, all arms).
- That the effect carries to real tasks, real pages, longer sessions and bases of thousands of facts, and how fast the
  shelf fills with extras under the candidate text.
- DSH: its `memcue_add` has no `shelf` parameter (read from `plugins/deepseek-harness/src/index.mjs:496-504`), so a
  DSH model cannot shelve through its tools at all. Read, not run.

**Intended and kept:** one shelf, `shelf:research`; the shelf out of `all` and every default; the hint only on an
empty answer; Hermes's `in: all` default (#438); the model deciding what to store and when to search.

## Options

| Option | Expected benefit (evidence) | Drawbacks and risks | Privacy, context, noise | Complexity | Fits the design intent |
|---|---|---|---|---|---|
| 0. Keep as is | none | Claude Code keeps storing no outside findings; both hosts keep missing the shelf when ordinary search answers | none added | none | yes |
| 1. **Instruction-only, scoped** (the tested candidate, refined) | capture 0→30/30 (Claude Code), 15→30/30 (Hermes emulation); shelf reached 9→18/18 and 14→18/18; chain: own findings returned 0→3/3 in both hosts, but every chained answer partial; fully correct recall answers unchanged (18→18, 17→17 of 27), unresolved ones turn into partial ones (measured, synthetic, one stand, see confounds) | more facts (Claude Code +17 extras over 24 runs), one misfiled pitfall, ~15% cost in Claude Code; text, so adherence varies by model and may fade in long sessions | ~1.1 KB more in the Claude Code note at every session start; shelf noise grows with extras; findings are stored in the local base, but their text goes to the model provider when the agent stores them and whenever a search returns them | small: text in `cmd/memcue/session.go`, the skill and the Hermes block; tests that assert the text | yes: nothing automatic, shelf still out of ordinary search |
| 2. Shelf hint on non-empty answers too (binary) | in Experiment A, 8 of 8 relevant shelf matches would be pointed at, against 2 today (simulated, not run); hints that came first were followed 15/15 | 2 of 10 hints would point at lexical noise (q4, q5) and each costs a search; changes a decided behaviour (#433, #453); helps recall only, not capture | one more line in many answers | moderate: `facts.go` hint logic, Hermes and DSH pass it through already | partly: the shelf stays out, but the hint becomes routine |
| 3. Option 1 for capture, option 2 later only if real use shows missed shelf searches | option 1's measured gain now, option 2's only if needed | needs a way to notice misses in real use (search logs exist: `MEMCUE_SEARCH_LOG`) | as option 1 | as option 1 now | yes |
| 4. Search the shelf by default, or merge it into `all` | would remove defect 2 | reverses #433: every page read once becomes noise in every search | most noise | small code, large behaviour change | no |
| 5. Automatic capture (extraction, a second model) | none measured here | reverses #494; a second model and its cost | transcripts go to a model | large | no |

Separate from the choice, and each worth its own issue: defect 6 (`-in all,shelf` reads no shelf: either honour the
list or refuse it); defect 3 (`correct ID -- TEXT`); a `shelf` parameter for DSH;
the floor note of defect 4 (for example a below-floor note on a shelf search whatever the count); and the
cross-host difference of defect 5.

## Recommendation

**Option 3, provisionally: a refined option 1 for both Claude Code and Hermes, measured again on a sandboxed stand
before merging, and the hint (option 2) left for later evidence.** This is exploratory evidence from one synthetic
stand with unresolved confounds; it can justify the next measurement, not a release. Reasons from the data:

- Capture is reachable only through instructions: no hint or search change makes an agent store a finding. Today's
  Claude Code text produced none in 40 opportunities across two models; the candidate produced all of them.
- The candidate's recall line closed every shelf miss of the grid (R2, R4, R8, chain) in both hosts, and the misses
  were all of one kind (shelf never named after non-empty answers), which a text line can address.
- Reaching the shelf did not make answers better on its own: fully correct recall answers did not change, and the one
  wrong answer of the study is in a candidate arm. The candidate adds over-capture (which stays on the shelf and out of
  ordinary search by design) and more answers built on shelf facts, with the same risk of going beyond them that the
  base arms showed when they found the same facts.

Refinements to make before the confirming run, because the grid showed the need (the refined text itself is
**untested**):

1. Keep findings of the work in their usual place: "a pitfall, a host or a fact of this repository goes where it goes
   today, not on the shelf" (fixes the misfiled C3 pitfall).
2. Store "what the conclusion rests on and its limits", not every detail of a page (aims at the 17 extras).
3. Say that pages and documents read for the task are outside sources even when they sit in the working directory —
   or decide the opposite (open decision 2).
4. In the check-before-use line, name the cheap check the R8 runs skipped: "compare the version or date of the finding
   with what the workspace uses".
5. Say that an answer built on a shelf finding should not go beyond what the finding and its source state, and should
   mark an inference as one (aims at the one-partition advice and the misreported forum).

Fix defect 6 together with the text, or word the Hermes line so it never combines `all` with the shelf: the candidate
tells the model to search the shelf, and the Hermes `in` description invites `all,shelf`, which today reads nothing
from it.

The confirming run cannot be made from this repository any more: the runnable stand was removed
([revision](#revision-after-review)). It needs a new stand that confines the agent (a container or VM with its own home
directory and Claude Code configuration, no access to the host's files or live bases, no copy of a live credential),
a grader who did not write the fixtures, and scenarios the candidate's author has not seen. For scale: the Opus grid of the two candidate arms here was $5.82 at
API prices (the first version of this report said "about $4", which was wrong). A claim that the shipped text improves real Hermes needs a run of
real Hermes, which this stand could not do (see [what was not done](#what-was-not-done)).

## Unresolved design decisions

1. Should Claude Code name the shelf at all? The README says the agent shelves outside findings itself; the Claude
   Code capture text, rewritten for #494, does not mention it. This report assumes yes.
2. Is a document in the repository "what the repository shows" (not stored) or an outside source (shelved)? The hosts
   disagree today (finding 9).
3. How wide is a "finding": every stated limit of a page, or only what the task's conclusion rested on? The answer sets
   the shelf's growth.
4. Should a shelf fact get an expiry or a "verify after" date by default? The stale-fact scenarios show that agents
   caveat old findings but rarely check the version at hand.
5. Should a research finding have a kind? `add` refuses `other` (finding 2); search accepts it.
6. Whether the hint should ever fire on non-empty answers (option 2), and with what relevance bound (finding 4).

## Limits

- **Synthetic and small.** 17 scenarios, 12 seeded facts, one invented world, three repeats; a real base has thousands
  of facts and real research reads many long pages. Counts are exact for these fixtures and say nothing about
  frequencies in real work.
- **One author for the design, one model for the grading.** The same agent wrote the fixtures, the labels, the
  candidate texts and the first grades, and adjudicated the second pass. The candidate was written before the grid but
  by an author who knew the scenarios, so it may fit them: it names "a vendor's or an anonymous claim", which is
  exactly what C1 contains. The second pass is the same model in a fresh context, not a person. Unseen scenarios and a
  human grader are needed before a rate is quoted.
- **Labels depend on one judgement.** 38 of the 58 partial answers are partial only because of the one-partition
  advice. Whether that advice is an unsupported claim or a sound reading of "64 partitions by default" is a judgement
  call; the tables show the counts both ways.
- **Hermes is emulated** (model, agent loop and tool-name prefix differ); DSH was not run.
- **Models.** Opus 5.5 for the grid, Sonnet 5.5 for 16 runs. Hermes on this host runs another vendor's model.
- **Single-turn sessions.** Each run is one prompt; a long session, a compaction or a user steering the work were not
  tested, nor was a gap of days between capture and recall (dates in the seed only simulate it).
- **Chain on one scenario** (C1w → R2), three repeats per arm.
- **Cost and time** are whole-run figures from `claude -p`; the text sizes are bytes, not tokens.
- **Not a sandbox.** The agents ran as the user, with the user's home directory, Claude Code configuration and
  environment, and bypassed permissions. They could read or write any path the user can, and some read outside their
  run directory ([data and isolation](#data-and-isolation)). Nothing shows that they changed anything outside it, and
  nothing proves they did not ([isolation check](#isolation-check)).

## Confounds not resolved

- **Built-in Claude Code plugins.** `cc-plugin-agents-md`, `cc-plugin-telemetry` and `cc-plugin-plugin-authoring`
  (path `builtin` in the raw `init` event) were loaded in all 232 runs. They come with the Claude Code 2.1.301 binary;
  the user did not install them. The binary's own description of `agents-md` says that a project without a CLAUDE.md
  gets its AGENTS.md files loaded where CLAUDE.md would be, which includes ancestor directories, and an ancestor
  directory of the stand holds a host-level AGENTS.md. Whether `--setting-sources ""` kept it out is **not known**: the
  stream does not record which instruction files were loaded. Weak evidence that it had no visible effect: none of
  the distinctive strings checked from that file appears in a tool input, an answer or a raw stream, and no answer follows the reply language the user's
  own global instructions set, so those at least were very likely not loaded. `plugin-authoring` adds a skill to the
  session (it appears among the slash commands) that no run invoked; `telemetry` sends Claude Code analytics. All four
  arms carried all three. That does not rule them out as a cause of a difference between arms: what a plugin adds
  meets a different context in each arm (the memcue plugin, its hooks and skills, the candidate text, the Hermes block
  and tools), so its effect may differ by arm as well as shift levels in all of them.
- **Session context not kept.** The compact records keep the hook count, not the hook output, and not the Hermes
  system block; ten answers rely on seeded facts from that context ([grading](#grading)).
- **Same author, scenarios known in advance** (see [limits](#limits)).

## Isolation check

What was measured: before the first run and after the last, a read-only count of the facts in the live Hermes base and
the live Claude Code base, and of the facts naming any synthetic system (Brindle, Quarrel, Ostrich, Larkspur, Vellum,
Kestrel). Both counts were unchanged in both bases, and no fact named a synthetic system.

What that shows, and what it does not:

- It shows that no fact naming a synthetic system was left in either live base at the end, and that the number of rows
  was the same at those two instants.
- It does **not** show that the live bases were untouched: an update in place, a fact added and another removed, a
  change to the search log or to memcue's state directory, or a change between the two counts and undone by the end
  would all pass it. No checksum or file-level comparison was taken.
- It says nothing about the rest of the user's state. Claude Code ran with the user's own configuration directory and
  may have written caches, plugin data or analytics there; other sessions wrote there at the same time, so a change
  could not be attributed to the runs after the fact. That was not checked.
- What does point the agents at the scratch bases: the agent's environment set `MEMCUE_FACTS` to the run's own base
  in every arm, the Hermes-emulation server got the base as an argument, no `memcue` binary is on the user's default
  `PATH`, every Claude Code run's
  stream shows its SessionStart hooks (3 in `cc-base`, 4 in `cc-cand`) succeeding, and every Hermes-emulation run shows
  the `memcue` MCP server connected to the run's base. No tool input in the records names a live base.

## What can be repeated

Only reads of what is kept; nothing below runs an agent, opens a base or needs a login:

```bash
cd docs/research/scripts/capture531
python3 -I -B grade.py selftest   # rule cases, fail-closed answer and capture bindings on tampered copies of
                                  # hm-base__C2__r3, privacy shapes, every kept label verifies; hard cases print agreement
python3 -I -B grade.py privacy    # counts four identifier shapes; 0 is a narrow result, not "nothing private"
python3 -I -B grade.py summary    # every count the report quotes from the records, and what the files cannot reproduce
python3 -I -B grade.py claims     # 257 values hard-coded in grade.py against the computed metrics; exits 1 on a
                                  # mismatch. It does not read this report: a number edited here is not caught
python3 -I -B grade.py table      # one row per run as JSON on stdout, with all three label layers
```

A full rerun is **not available** from this repository: the scripts that built the stand and ran the agents were
removed ([revision](#revision-after-review)). So is Experiment A's generator; its output `results/mechanics.json` is
kept, and `summary` recomputes the table of section A from it and the hand relevance labels above. The fixture workspaces are in `testdata/`, so
their Go files stay out of the module.

## What was not done

- **Real Hermes was not run.** Blocked: Hermes on this host authenticates through a rotating OAuth credential; a
  second Hermes home with a copy risks logging the live gateway out, and the live home would use the live base and
  state. The Hermes columns are an emulation with Hermes's own memory text and tool handling.
- **DSH was not run**; its behaviour above is read from the code.
- **No human grading.** The second pass is by model instances in a fresh context; no person reviewed any label.
- **No sandbox.** The runs were confined by environment variables and a deny list only.
- **No real data.** By design (#531's privacy rule); so no frequency of the motivating case in real work is estimated.
- **The refined candidate text is not measured.** Only the candidate in `fixtures/` was run.
- **No product change.** Follow-up issues #532 and #533 were filed after the first version of this report; this
  report does not restate which of the separate defects listed under [options](#options) each one covers.

## Revision after review

An independent review of the first version found blocking problems. What was done about each, on 2026-10-27, without
new agent runs and without touching live state:

1. **Private details in the records; no sandbox.** The raw records held the stand's absolute paths (naming the home
   directory) in 62 of 232 runs, a listing of the repository checkout from one `find /`, and the real host name from
   two `hostname` calls. Every record was rewritten with `<REPO>`, `<STAND>` and `<real-hostname>` in their place
   (105 paths and 2 host names in 65 files), and the grader's table was compared before and after: identical. The
   text now says that the stand was not a sandbox and that those details went to Anthropic during the runs; that
   cannot be undone by anonymizing the copy kept here.
2. **Unsafe scripts.** `run.py` deleted and recreated run directories (`rmtree` on an overwritten run), inherited the
   user's home and environment and ran the agent with bypassed permissions; `stand.py` and `mechanics.py` deleted and
   overwrote paths given on the command line; and a rerun over an old run name would have kept the old manual label
   for a new answer. Rather than harden them, they were removed with their MCP helpers (`hermes_mcp.py`,
   `web_mcp.py`); the full rerun is therefore unavailable. The grader now binds a final label to the sha256 of the
   answer it judged, so a changed answer falls back to `unverified` (the hash does not cover the label itself,
   see [grading](#grading)), and it no longer cuts answers at 1,500
   characters (25 answers were longer; the records always held them whole).
3. **"No wrong answers" was not backed.** All 232 answers and all 104 capture runs were graded again
   ([grading](#grading)): one answer is wrong (`hm-cand__C1w__r1`, the case the review named), 58 partial; one capture
   item is withdrawn (`hm-base__C2__r3` D2). The first labels stay in `results/manual.json`, the second pass and the
   adjudication in `results/regrade.json`.
4. **R4 misses left out of the step analysis.** The six R4 retrieval misses of the base arms are now counted and set
   apart from the R4 answers, which are correct ([where each miss happened](#c-recall-and-use-opus-seeded-base-3-repeats-per-cell)).
5. **"No user plugin".** Three built-in plugins were present in every run; they are listed as a confound.
6. **Live bases "untouched".** The before/after count is now described for what it shows and what it does not
   ([isolation check](#isolation-check)).

Also corrected while checking every number: the hint was followed in 15 of 15 runs where it came first, not "19 of
19"; the cost of the candidate arms' grid was $5.82, not "about $4"; "recall without a cue reached the shelf 0 of 10"
is 0 of 13 over R2, R4 and R8. Every other count in the results was recomputed from the records and holds.

## Second revision

A second independent review, the same day, again without new agent runs or live state:

1. **Capture adjudication bound to its facts.** Each of the 104 capture runs in `results/regrade.json` now carries
   `capture_sha256` ([grading](#grading)); a missing or mismatched hash, or a per-fact reading that does not name
   exactly the stored facts, makes the run's coverage unverified. `selftest` checks the intact `hm-base__C2__r3`
   record and adjudication, which must verify, and ten tampered variants of them (edited, moved, added and removed
   facts; missing hash; another run's name; edited coverage or reading under the old hash; missing coverage; a hash
   recomputed over a fact the second pass never read), plus the intact and an edited answer; every tampered variant
   fails closed. No case edits a label under a kept answer hash: that change is not detected ([grading](#grading)).
2. **Summary recomputes the counts.** `summary` now prints every count the report takes from the records, by section,
   and lists what it cannot reproduce. `claims` compares 257 expected values, hard-coded in `grade.py` as copied from
   the report, with the metrics `summary` computes; it does not parse this report, so it does not catch a number
   edited here after the copy, or a number in the report missing from its list. Source counts come from
   the second pass, not the pre-label (hm-cand naming its source 29/29, not the pre-label's 24). The chain's targets
   are the agent's own facts from the C1w run it reads, not the seeded IDs, which the chain base does not hold: the
   old summary printed "target 0/3" for all four chain cells, which was no evidence of anything.
3. **Numbers corrected:** extras 17, not 13 (the pre-label had matched four back-pressure facts as D2); the cost
   difference +15% and +6%, not +16% and +5% (the first figures divided rounded means); the `all,shelf` search came
   first in three `hm-base` R8 runs, not in R1.
4. **Privacy check described for what it is,** with its shapes and blind spots; it no longer prints the matched text.
   The non-reserved fixture domains are flagged. The provider and telemetry transmissions are stated, the options
   table no longer says findings "stay local", and the built-in plugins are no longer said to be unable to explain a
   difference between arms.
5. **Follow-ups:** #532 and #533 now exist.
