# Evaluation

What was measured while memcue was built (October 2026), on one developer's Claude Code memory and sessions. The
samples are small and come from one person's work, so read the numbers as direction, not as benchmarks. The data
itself (real prompts, notes and transcripts) stays private; the method is described so it can be repeated on other
data.

Labelling was done blind by separate Claude agents, not by people: each got the transcript or the prompt and the
candidate facts without knowing which system produced them, and labelled every fact against the source.

**One limit of this file.** Every pair labelled here was proposed by the lexical reader, so these numbers say how good
that reader is and cannot say whether another reader is better — a semantic reader can only keep or lose these wins.
Comparing readers needs a pool that all of them propose into; that experiment is in
[gold-set-2026-10.md](research/gold-set-2026-10.md), and it is the one that changed the design (the words choose the
candidates, a judge orders them; the judge was later removed, #357, and the words order them too).

## Recall: what reaches the agent

**Setup.** The starting point was Claude Code's built-in memory of one user: 312 notes in files, indexed by
`MEMORY.md`, split by repository. The question was whether a hook that picks notes or facts by the words of the
prompt gives the agent what it needs without filling its context with noise.

**Labels.** For a set of real prompts, every item a system injected was labelled USEFUL (helps with this prompt),
RELATED (on topic, not needed) or NOISE; every prompt was also labelled for whether it needed memory at all.

### Holdout: 100 real prompts

Each system was tuned on a separate development set and run once on prompts it had not seen.

| System | Items injected | Useful | Noise | Notes |
|---|---|---|---|---|
| memcue (facts, final) | 55 | 18 (33 %) | 21 (38 %) | useful facts reached 12 of the 41 prompts that needed memory; 16 facts went to prompts that did not |
| memcue v1 (paragraphs of notes) | 53 | 9 (17 %) | 31 (58 %) | |
| holographic 0.17.4 prefetch | 219 | 8 (4 %) | 190 (87 %) | injected something into 61 of 100 prompts |

The last two rows were measured on another 100 prompts of the same kind (a holdout is used once). What changed
between v1 and the final version: the base holds short self-contained facts instead of paragraphs of notes, facts
from every repository are searched, and at most two facts are given.

Lexical search is the limit: a prompt that shares no rare words with the fact it needs gets nothing. 12 of 41 is
the honest coverage of what the prompts needed.

### End to end: does the agent answer better

30 questions whose answers sit in the memory (10 about notes of another repository than the one the session runs
in), each asked once with and once without memcue through `claude -p`, the order alternated. Answers were checked
against expected values.

| | Without memcue | With memcue |
|---|---|---|
| Correct answers | 27 / 30 | 30 / 30 |
| Questions about another repository | 7 / 10 | 10 / 10 |
| Turns per answer, mean | 3.2 | 2.2 |
| Cost per answer, mean | $0.65 | $0.61 |

The hook injected the fact the answer came from in 26 of 30 runs. One run per question: three questions flipped
from wrong to right and none the other way, which is a direction, not a significant result.

The hook takes about 10 ms per prompt with a base of 2335 facts.

### Facts of other projects (October 2026)

Since #345 recall is stricter about a fact scoped to another repository: in a repository's session, such a fact
needs 0.25 of the prompt's weight instead of 0.15. The measurement used 432 Claude Code prompts, those where the place
of a fact changes what is served. The facts were labelled by a blind judge, checked against what the agent then did,
and the disputed ones by hand.

- **The gate** cut the noise facts by 19% and lost a needed fact in 3 prompts.
- **A hard filter**, which leaves such facts out entirely, cut 37% and lost a needed fact in about 20 prompts.

The judge's USEFUL was right about 40% of the time on the disputed pairs. The judge-labelled usefulness elsewhere in
this file was not checked by hand, so it may be inflated the same way. Method and tables: [scope-filter-2026-10.md](research/scope-filter-2026-10.md).

### Pull instead of push (October 2026)

Since #361 no host injects facts with a prompt: the agent searches memcue itself, and the session card stays. On 40
real prompts, each run twice through `claude -p` with Opus on the repository as of the prompt and graded blind in two
passes (score 0–2):

| | No memory | Push (0.7.0) | Pull | Both |
|---|---|---|---|---|
| Score, mean | 0.76 | 0.89 | **1.08** | 0.99 |
| Correct answers (score 2) | 10 | 13 | **22** | 20 |
| Wrong or empty (score 0) | 29 | 24 | **15** | 22 |
| Cost of 80 runs | $30.19 | $31.43 | $33.75 | $31.36 |

Pull against push is +0.19 per prompt, 95% interval [+0.03, +0.37]; push against no memory stays inside the noise.
The agent searched in 62 of 80 runs, two queries in the median run. A search that leaves out the facts of other
projects and of the card scored below the plain search, inside the noise (−0.09 [−0.23, +0.05]), so search keeps every
project. Only Opus in Claude Code was measured. Method and tables: [pull-2026-10.md](research/pull-2026-10.md).

## Writing: how facts get into the base

### Migration of existing notes

312 notes of the built-in memory → 2335 facts on Claude Opus, $39.83. On a sample of 30 notes (205 facts) every fact
was checked against its note: with the first prompt 16 % of facts carried the model's own conclusions; after the
prompt forbade anything not written in the note, 2.5 %; wrong facts: none in either.

### Extraction from sessions

Extraction is removed since #494: the agent writes its facts itself. This section describes it as it was measured.

10 recorded Claude Code sessions from nine repositories, 22–272 thousand characters of transcript each. Every fact
was labelled SUPPORTED (said by the user or confirmed by tool output), INFERRED (a guess, or an agent's proposal
presented as the user's decision) or WRONG (contradicts the transcript, or keeps a state the session later changed),
and DURABLE or EPHEMERAL. The labeller also listed the durable facts each session established; coverage is how many
of those an extractor kept.

First run on Opus, before the prompt was tightened: 101 facts, 3 WRONG and 5 INFERRED. All three wrong facts came
from the longest session, read in two parts: the first part did not know a later part changed the state. The fixes:
a decision is the user's only if the user stated or confirmed it; a fixed problem is recorded with its fix; later
parts of a long session see the facts of earlier parts.

Comparison of models after the fixes, blind (A/B with the model hidden):

| Model | Facts | Wrong / inferred | Coverage of durable facts | Cost of the run |
|---|---|---|---|---|
| Claude Opus | 102 | 0 / 1 | 84 of 96 (88 %) | $4.97 |
| Claude Sonnet | 69 | 0 / 3 | 70 of 96 (73 %), nothing at all in 2 sessions | $2.38 |

Then DeepSeek (`deepseek-flash`, through the OpenAI-compatible endpoint) against Opus on nine of the sessions (the
tenth had grown since the Opus run):

| Model | Coverage of durable facts | Wrong / inferred | Ephemeral facts | Cost of 10 sessions |
|---|---|---|---|---|
| Claude Opus | 81 of 93 (87 %) | 0 / 4 | 5 | $4.97 |
| DeepSeek flash | 69 of 93 (74 %) | 1 / 3 | 24 | $0.17 peak, $0.08 off-peak |

On the first DeepSeek run one long session lost almost everything: asked to return the whole list after its second
part, the model returned only the new facts. Since then a later part returns only new facts and the numbers of
earlier facts it outdates; earlier facts stay unless named. The DeepSeek row is after that change; the one wrong
fact is a misread number (a byte length taken for a vector size).

About 30 of the 101 facts of the first run repeated what the base already held, because the base had been built
from notes the same sessions wrote. With the built-in memory off, extraction is the only writer and that source of
duplicates goes away.

### What to keep (October 2026)

A look through the base found many facts that were not worth keeping:

- retellings of the repository a session ran in (release steps, CI jobs, project generation);
- logs of events ("updated to 0.3.11, containers healthy");
- multi-step procedures.

The session prompt asked for them: the layout of systems, fix recipes, dated changes of state. The new prompt draws
the line of Claude Code's built-in memory. A fact must matter a month later, and the agent must not be able to learn
it with one command from the repository the session ran in, its git history or its instruction files. Anything
outside that repository stays: hosts, services the session administered, external systems and their identifiers,
tools' non-obvious behaviour, measurements behind decisions.

**Method.** Each prompt extracted the same sessions on Opus (`memcue extract -dry-run`). One blind call per session
saw two fact lists as A and B in random order. It labelled each fact with one of:

- KEEP: needed later and not derivable;
- DERIVABLE;
- EVENT;
- PROCEDURE;
- WRONG.

It also listed the KEEP facts each list lacks from the other. Running the old prompt twice gives the noise: the KEEP
facts two runs of one prompt disagree on. The scripts are
[extract_label.py](research/scripts/extract_label.py) and [extract_filter.py](research/scripts/extract_filter.py).

The prompt was tuned on the 10 sessions above, then checked on 10 other sessions from 10 other repositories:

| 10 other sessions | Facts | KEEP | DERIVABLE / EVENT | KEEP facts of the old prompt it lacks |
|---|---|---|---|---|
| old prompt | 102 | 72–79 (0.71–0.77) | 16–18 / 10–13 | — |
| old prompt, second run (noise) | 100 | 76 | 17 / 7 | 6 of 79 |
| new prompt | 66 | 61 (0.92) | 2 / 3 | 13 of 72; the old prompt lacks 7 of its |
| Claude Code's types and "What NOT to save", quoted, with memcue's fact format | 62 | 56 (0.90) | 0 / 6 | 22 of 73 |

On the tuning sessions the old prompt kept 72 of 103 facts and the new one 73 of 80, and each lacked 11–12 KEEP facts
of the other. The noise there was 4–7.

The new prompt writes about a third fewer facts and almost no junk. It loses about one useful fact in ten beyond the
noise: production measurements, external systems, the local Go setup. Claude Code's criteria as they are lose twice
as much. They are written for the memory of one project, and the model applies "file paths, project structure" to
hosts and external systems too.

**Two alternatives were measured and not taken.**

*A filter after a generous extraction.* Jev 1.14 asked two `noul` per fact, with the session's repository in `state`:
"derivable from this repository" and "a log entry true for a day or two". A fact was dropped when either reached the
threshold. Over both sets of old-prompt facts (61 junk, 144 KEEP):

| Threshold | Junk dropped | KEEP lost |
|---|---|---|
| 0.6 | 33 | 12 |
| 0.7 | 30 | 8 |
| 0.8 | 14 | 4 |

At 0.7 the other sessions keep 69 of 72 useful facts and 15 of 30 junk. On top of the new prompt the filter drops as
much KEEP as junk (0.7: 3 of 12 junk, 4 of 134 KEEP), so the two steps do not add up.

*The prompt in English.* A close translation of the new prompt, on the other sessions, wrote 55 facts, all KEEP. The
Russian one wrote 66 with 64 KEEP. The English list lacked 6 KEEP facts of the Russian one; the Russian list lacked 2
of the English. Two runs of the Russian prompt disagree on 3–8, so the difference is within noise. The facts kept the
session's language: 53 of 55 were Russian.

Each extraction of 10 sessions cost about $5 on Opus; each blind comparison about $1.3.

### One claim per fact (October 2026)

A fact that bundles claims goes stale as a whole when one of them changes: "memcue 0.3.0 is installed on loffe, the
binary is in P, releases are named R" kept answering "which version" after the upgrade. In a sample of 40 facts
extracted on 20–21 October, 11 held more than one claim.

Rule 1 of `session_prompt.md` got a test ("can one part go stale while the other stays true? then it is two facts"),
the two common bundles (a system's state as a list; a fix with an unrelated aside) and a guard that splitting is no
reason to drop a part. Ten real sessions of 0.2–2.3 MB were extracted twice with each prompt on deepseek-flash, and a
separate agent labelled every fact single or compound without knowing which prompt produced it:

| Prompt | Facts (2 runs × 10 sessions) | Compound |
|---|---|---|
| before | 120 | 43 (36 %) |
| after | 124 | 24 (19 %) |

Run-to-run spread is large: the same session gave 1 and 3 facts with the old prompt. A first version of the new rule,
without the guard, dropped pitfalls found while investigating (one session went from 7–8 facts to 3–5); with the guard
the count stayed level. The labeller was strict (it counted a backup path or file mode appended to a claim as a second
claim), so the absolute rates matter less than the drop.

### Extraction by patterns

holographic can store user sentences that match patterns such as "I prefer …" or "we decided …". Its patterns found
nothing in the 55 user messages of the 10 sessions and nothing in 280 real prompts, while the labellers found 96
durable facts in those sessions. memcue does not extract by patterns.

## Cost in daily use

Extraction is the only part that calls a model. On the author's volume (about 170 sessions a week) Opus extraction
comes to roughly $120 a week at list prices; on a subscription that is usage of the plan, not money. DeepSeek is about
thirty times cheaper and keeps three quarters of what Opus keeps.

## Latency (October 2026)

The recall hook is the only path a user waits on. Measured on the author's Mac on 2026-10-22 with memcue
0.4.0-main.233e7f1, 2620 active facts, one prompt, five runs each; the embedder (`baai/bge-m3`) and the judge
(`typesafe/jev-1.14`) are both on RouterAI.

| Path | Time per prompt |
|---|---|
| words only: process start, base open, FTS5 | 43–44 ms |
| words + judge, no embedder | 45–46 ms (no lexical candidate passed the gate, so the judge was not asked) |
| words + embedder, no judge | 45–46 ms (the vector reader runs only in front of a judge) |
| words + embedder + judge | 1023, 1079, 1238, 1319, 1817 ms |
| embedder request alone, one prompt | 0.81–1.53 s, of it TLS handshake 0.05–0.86 s |
| judge request alone, 20 facts | 0.37–0.53 s |
| `memcue session-start` (the card) | 54–56 ms |
| `memcue version -minimum 0.4.0` (the plugin's check) | 21–49 ms |

The two requests share one HTTP/2 connection, so there is no second handshake to save. Dream and vet already run
their model calls in parallel; extraction runs in the background after the turn. On loffe, the Hermes host, the hook
with both services took a median of 919 ms over four runs with 228 facts (#213).

memcue itself costs about 45 ms; the rest is waiting on the two remote services. On the probe prompt every served fact
came from the vector pass, so turning the services off would also lose those facts. #232 is about matching their
quality with local signals only.

## Repeating the measurements

`memcue eval` ran a labelled set against the injection of facts with a prompt; it went with that injection in #361,
and the measurements above that used it stay as history (the set format is the `evalSet` type in `cmd/memcue/eval.go`
before #361). The agent's own searches are judged end to end (`docs/research/scripts/e2e`) and by `search.log`
(`MEMCUE_SEARCH_LOG=1`). A cheaper middle ground — one run of `search` over prompts with known answers, minutes not
dollars — is [search_stand.py](research/scripts/search_stand.py), with a first run on a live base in
[pull-hermes-2026-10.md](research/pull-hermes-2026-10.md) and the run at 0.10.0 a day later in
[pull-hermes-010-2026-10.md](research/pull-hermes-010-2026-10.md). `memcue extract -dry-run TRANSCRIPT.jsonl` prints what a
session would add, with tokens or cost, without storing it.

`search_stand.py --cases` (#510) runs the search alone over labelled synthetic cases
([search_stand/synthetic.json](research/scripts/search_stand/synthetic.json)): a new base per arm, short agent
queries, paraphrases and full prompts reported apart, a baseline and candidate flags or builds compared per query. It
runs memcue with a scratch environment but no sandbox, and does not measure whether an agent stores or uses a fact;
that is #511. The first run and its limits
are in [search-synthetic-510-2026-10.md](research/search-synthetic-510-2026-10.md).

Whether an agent stores what it reads in outside sources on the research shelf and finds it again later (#531) was
explored with agent runs over synthetic scenarios, Claude Code and an emulation of Hermes, current text against a
candidate: [research-shelf-531-2026-10.md](research/research-shelf-531-2026-10.md). It is exploratory research, not a
harness. The runs were not sandboxed, so the scripts that made them were removed; what is kept are the anonymized run
records and a read-only grader, which recomputes the report's counts from them, names what it cannot, and cannot
repeat the runs. Its `claims` check compares those counts with expected values hard-coded from the report; it does
not parse the report itself.
