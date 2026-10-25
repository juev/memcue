# Pull at 0.14.0 on a live installation: what the floor does to the answer, October 2026

Status: **interim measurement, 2026-10-25.** Same installation and the same twenty prompts as
[pull-hermes-010](pull-hermes-010-2026-10.md), four releases later. 0.14.0 was installed and the gateway restarted
before the run, so every number below is the behaviour of the new binary and the new plugin together.

Verdict in one line: **better, and the improvement is where the research said it would be** — on the queries an agent
writes the search now returns mostly facts that answer it (4 % unrelated, was 15 %), in a third fewer characters and
at a quarter of the latency. The price is the one [#407](https://github.com/juev/memcue/issues/407) predicted and did not measure on user prompts: on long
messages the floor cuts deeper and takes useful facts with the noise.

## Setup

- memcue 0.14.0, binary and plugin. Two releases shape the comparison: **0.13.0 → 0.14.0** adds the search controls of
  #409 (`-in`, `-kind`, `-since`, `-expired`, `-all-words`, `-min-score`) and makes the default place depend on the
  working directory — the project's facts alone inside a repository, the whole base elsewhere; **#407** made `-n` a
  maximum, dropping every fact whose BM25 is under 0.6 of the best one's.
- The base: **391 live facts** by id (`status = 'active'`, `review IN ('', 'provisional')`), 176 characters on
  average, schema 5, index 391 of 391 ids (no gap, no duplicate row), 4 candidates stored but not indexed, one live
  fact past its expiry.
- One binary of the previous arm is kept as an upgrade backup, so both arms run on **one copy of the base**: 0.14.0
  against 0.10.0, the version [pull-hermes-010](pull-hermes-010-2026-10.md) measured. Same base, same prompts, same
  day, top ten per prompt, three searches each, the slowest of the three reported.
- Two query sets, because they answer different questions:
  - **8 queries written the way the agent writes them**, short and named after the thing asked for. This is what the
    search actually gets in pull.
  - **The 20 user prompts** of the 0.10 note, unchanged, so that note and this one are comparable. A user's message is
    not what the search gets: it is long, and a floor built on the share of the best score punishes long messages.
- One rater, the agent that answered them, grading every delivered fact from its text alone: `U` answers the query,
  `R` is on the topic without answering, `N` is unrelated. One pass, no second labeller.

## Tests

| test | result |
|---|---|
| plugin suite (`tests/test_provider.py`, 73 cases) | all pass, 5.1 s |
| `go test ./...` | 9 packages, all ok |
| latency, end to end, 60 searches (process start included) | p50 **9.7 ms**, p90 12.7 ms (0.10.0: 23.9 / 27.5) |
| latency inside the binary, 31 logged calls of the live gateway | p50 2.5 ms, p90 3.3 ms |
| facts per search, agent queries | **3.25** (0.10.0: 5.00) |
| characters per search, agent queries | **606** ≈ 189 tokens (0.10.0: 868 ≈ 271) |
| characters per search, user prompts | **550** ≈ 172 tokens (0.10.0: 860 ≈ 269) |
| unrelated questions | no fact, both arms — "no stored facts match" |

The latency win is not the floor alone: 0.12.0 and 0.13.0 took three passes over the store's open path (index repair,
then a trusted count) and the whole call got half as expensive. The floor halves the answer as well.

## Agent queries (8): precision up, one useful fact lost

| | 0.14.0 | 0.10.0 |
|---|---|---|
| facts delivered | 26 | 40 |
| useful (U) | **11 (42 %)** | 12 (30 %) |
| on topic (R) | 14 | 22 |
| unrelated (N) | **1 (4 %)** | 6 (15 %) |
| queries with a useful fact | 8 of 8 | 8 of 8 |

The floor removed fourteen of the fifteen facts that did not answer the query, and one that did: a query about the
extraction threshold would have got, besides the threshold fact, the `extract -dry-run` procedure — the floor left the
threshold alone. Two examples of what changed on the same base:

- "free space on the backup NAS, restic backups of the host": 0.14.0 returns the two facts that name the repository
  path and the repository itself; 0.10.0 returned those two plus three facts on snapshots and a lock.
- "`write_file` refuses to overwrite" — the exact error name and the tool: 0.14.0 returns one fact, the right one;
  0.10.0 filled the other four slots with two further `write_file` facts that answer other questions and two facts with no relation to it.

## User prompts (20): the floor costs useful facts

| | 0.14.0 | 0.10.0 |
|---|---|---|
| facts delivered | 65 | 100 |
| useful (U) | 22 (34 %) | 30 (30 %) |
| on topic (R) | 23 | 33 |
| unrelated (N) | 20 (31 %) | 37 (37 %) |
| prompts with a useful fact | **13 of 20** | 15 of 20 |

The share of useful facts rises and the unrelated share falls, but **8 of the 30 useful facts of the top five are
gone**, so two prompts that had a useful fact lost it:

- "how much free space on the NAS, what weighs most, how big are the backups" — kept a fact about the size of a
  memory file; dropped both restic facts that named the NAS path and the backup repository.
- "what is that message about a file change error" — kept an unrelated "file not modified since" fact; dropped the
  fact that names the `stale_write_blocked` error the user was asking about.

This is the long-message effect: every one of the twenty is a whole user turn, so the query holds dozens of common
terms, the best fact's score is inflated by them, and the ratio 0.6 sits above facts that do answer. The decision note
of #407 says as much and leaves user prompts out of the floor decision; measuring them here prices that decision.

## Controls

- `-kind pitfall` returns pitfalls only, `-since 2026-10-25` returns facts created that day, `-expired` is the only
  way to see a fact past its expiry (the default hides it), `-min-score 0` widens the net, `-all-words` requires every
  word.
- `-in all` in a directory that is a repository brings back the whole base, `-in project` only the repository's facts.
- `-in repo:KEY` matches the **stored scope string exactly**: `repo:memcue`, a key that only old versions of the base
  carry, returns nothing with no hint that the live key is `repo:github.com/…/memcue`. A wrong key is
  indistinguishable from an empty base.

## Where the search looks: the binary's default and the Hermes override

The binary takes the project's facts alone inside a repository, and the whole base elsewhere. The Hermes plugin does
not let a call run on that default: since 0.14.0 it appends `-in all` whenever the model names no place, on the ground
that a Hermes chat is not about one repository. So **the scope hid no fact in this run** — both arms searched the whole
base.

The distinction matters for the other harnesses. Through the binary, a question about the host asked from inside a
configuration repository returned **one fact** (about the repository) where `-in all` returned **five**, three of them
the host facts that answer it, and **255 of the 391 live facts carry the host scope**. A harness that runs on the
binary's default — the CLI, the Claude Code session — loses 65 % of this base in any repository session, and the agent
has to know that the answer lives outside the project before it can ask for it.

An earlier version of this note read that as a Hermes effect; it is not one, and the correction is the only change in
this section.

## Adoption

- Seven days: **25 of 56 sessions with a human prompt** called `memcue_search` at least once (45 %); counting cron and
  subagent sessions, 28 of 139 (20 %).
- Today: 9 of 12 human sessions, 27 calls. Two of the 31 logged calls returned no fact at all.
- The card: 15 facts, **2 880 characters (≈ 900 tokens)** at every session start and after every compaction, unchanged
  through the day — the constant cost sits next to the per-call cost that the floor just halved.

## The base after four releases

Nothing in the live base names a version newer than 0.11.0. The fact that says "0.10.0 on this installation" is retired,
the one rewritten for 0.12.0 is still a **candidate**, therefore not indexed, so a query naming the current version
returns no fact at all — correct behaviour of the index, wrong state of the base. Adoption of a release is not finished
when the binary is installed; the facts that describe the installation have to be corrected in the same pass.

## What the answer does not tell the model

The flags reach the model — they are in the tool schema, with `in` named as `all (the default)` and `min_score` as
`0.6` — and in live traffic **they go unused**: none of the roughly twenty calls logged since the release named a
single control. So the gap is not documentation of the flags.

The loop the model already runs is reformulation: sessions in the log hold two to six calls with synonyms, and a
one-word probe that returned nothing was followed by a worded query that found the fact (`v1.106.1` → 0 facts, then
"how to update memcue on hermes" → 2). What the loop cannot do is tell a thin answer from a thin base: when the floor
drops the facts that answer, or the scope hides them, the model has no signal that anything was left out.

Measured, so the design can be judged on numbers rather than taste:

- **A "there are N more facts" counter would fire constantly.** The floor trims the answer in 4 of the 8 agent queries
  and in 9 of the 20 long prompts. A field present on half the calls stops being read.
- **Raising `-n` is not the lever.** Above the floor it adds nothing: of the 35 facts the floor cut from the twenty
  prompts, 8 (23 %) were useful, so a wider `-n` mostly advertises the other 27.
- **Padding the answer with other scopes costs what the floor bought.** Adding facts from other scopes returns the
  payload to the 0.10.0 size — 868 characters per search at 30 % precision against 606 at 42 % — and on this base 65 %
  of the facts are host-scoped, so the padding dominates the answer instead of trailing it.
- **A hint on an empty or single-fact answer would fire rarely and land where the retry loop is blind:** 2 of 31
  logged calls returned nothing, and the thin answers are exactly the floor-and-scope cases above.

## What is worse than at 0.10.0

1. **The floor on long messages.** Useful facts are dropped (8 of 30 in the top five here) and two of twenty prompts
   lose their only useful fact. The floor is a share of the best score, so the longer the query, the higher it cuts.
2. **A harness on the binary's default hides the host.** 65 % of this base is the host scope. Hermes appends `-in all`
   to every call, the CLI and the Claude Code session do not, and inside a repository those facts are invisible unless
   the caller thinks to pass `-in all` (measured through the binary: one fact against five).
3. **`-in repo:KEY` fails silently** on a key that is not exactly the stored scope string.

## Reproduction

```sh
# both arms on one copy of the base, latencies and coverage
cp facts.db /tmp/facts.db
python3 docs/research/scripts/search_stand.py --prompts prompts.json \
    --base /tmp/facts.db --binary ./memcue --sweeps 1,2,3,5,10 --repeat 3
# the floor and the controls
memcue search -json -n 5 -- "<query>"                 # floor on, at most five
memcue search -json -n 5 -min-score 0 -- "<query>"    # the wide net
memcue search -json -n 5 -in all -- "<query>"         # from inside a repository
```

Not measured: a base other than this one, a second labeller, and the queries of another harness. The twenty user
prompts are the same set as the 0.10 note, so the two notes compare directly; the eight agent queries are not.
