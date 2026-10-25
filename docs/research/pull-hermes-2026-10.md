# Pull in Hermes: what the live base gives, October 2026

Status: **measured 2026-10-25 on one live Hermes installation after the 0.8.0 upgrade.** The mechanism works and is
almost free: a search costs about 3 ms inside the binary, there is no model call, and the agent does use it. Two things
do not work yet: the base still describes the machinery 0.8.0 removed, and the tooling that reported on the old push
mode reports zeros for the new one.

[pull-2026-10](pull-2026-10.md) measured pull against push end to end on Claude Code with Opus and did not measure any
other harness. This note is the other side: the same release, a weaker model on another harness, real prompts of the
user rather than replays, and what the live base actually returns.

## Setup

- memcue 0.8.0; the plugin loads it into a Hermes gateway, the binary is called per tool invocation
  (`memcue search -json -cwd <workspace> -session <id> -- <query>`), ten facts by default.
- The base: 432 live facts by id (807 ids with their versions, schema 5), no duplicate groups, 3 candidates, average
  fact 176 characters, 191 `environment`, 133 `pitfall`, 26 `decision`, 19 `rule`.
- 20 real prompts of the user from the installation's session store: distinct, over 60 characters, no cron and no
  delegation wrappers, the newest 20 that survive that filter.
- One rater, the agent that answered them, grading every delivered fact from its text alone: `U` answers the prompt,
  `R` is on the topic without answering it, `N` is unrelated. No second pass, no second rater, so the shares below are
  a direction, not a benchmark.
- The stand: [search_stand.py](scripts/search_stand.py).

## What the search returns

Top-k sweeps over the same 20 prompts, each run with its own `-n`:

| top-k | prompts with a useful fact | useful | related | noise | useful share | noise share | chars | ~tokens |
|---|---|---|---|---|---|---|---|---|
| 1 | 7/20 | 7 | 5 | 8 | 35% | 40% | 3 437 | 1 074 |
| 2 | 8/20 | 11 | 11 | 18 | 28% | 45% | 7 403 | 2 313 |
| 3 | 10/20 | 15 | 19 | 26 | 25% | 43% | 11 317 | 3 537 |
| 5 | 14/20 | 22 | 30 | 48 | 22% | 48% | 18 808 | 5 878 |
| 10 | 14/20 | 25 | 55 | 120 | 12% | 60% | 37 964 | 11 864 |

The useful facts are all in the top five: the ten-fact default doubles the characters (18 808 → 37 964) to deliver
three more useful facts and reach the same 14 prompts. Everything below the fifth fact is two thirds noise.

Latency: 2.5–3.5 ms inside the binary, 19 ms per prompt end to end including the process start. The push mode with the
judge was 311–450 ms and a paid call per turn.

The default is noise by design: `Search` is FTS5 over the query's terms in BM25 order and none of the gates of
`Recall` — "the agent reads what it asked for" (`internal/recall/search.go`). Two consequences measured here:

- **A generic query gets generic facts.** Two different prompts, both about labeling tasks, returned the *same* ten
  facts: the words that matched were the common ones, and the ranking cannot tell that neither prompt wants them.
- **The query is the lever.** Rephrasing three of the worst prompts the way the session-start note asks — system name,
  synonyms, both languages — moved the useful fact into the top five for two of them; the third stayed noise. This is
  the hypothesis of [pull-2026-10](pull-2026-10.md), and it holds where the words exist in the base at all.

## Adoption

Four user prompts arrived after the upgrade. Three of the four sessions searched (2, 3 and 3 calls); the fourth was a
cron run, which the card and the note skip by contract. Three sessions is not a rate — it says the note is read and the
tool is called, not that a model weaker than Opus will call it when the prompt only looks unrelated. That number needs
weeks of `search.log`, and it is the number the whole pull design rests on.

## What does not work yet

1. **The base has no fact about 0.8.0.** Counted over the 432 live facts: 0 mention `0.8.0`, while 35 describe the
   removed `recall`/`recall.log`, 21 name the Jev judge or the rerank layer, 29 pin versions 0.5–0.7 and 6 name the
   `min_coverage`/`MAX_FACTS` knobs, all of them gone. An agent that searches memory is told how facts are pushed with
   a judge — the opposite of what the release does. One fact still points at the offline stand directory and the `eval`
   verb; both are gone.
2. **The push-era report still runs.** The weekly review reads `recall.log`, which stopped at 2026-10-23, so it reports
   the last push week as the current one and recommends tuning gates that no longer exist; its "active facts" are rows
   of versions, not facts (1 783 against 432 by id).
3. **No cheap regression test.** `eval` went with the reranker. What is left is the end-to-end replay: 40 prompts, an
   Opus run per arm, about $34 an arm, and a grader call on top. Nothing answers "did this change make the search
   worse" in a minute, which is what the stand above does for characters, coverage and, with labels, for precision.

## What follows

- The ten-fact default is worth a measured change, not a guess: five facts keep the prompt coverage and halve the
  characters. Whether the agent reads five better than ten is not measured here — only what it is handed.
- The facts about the removed machinery are candidates for retirement, and the facts of the release are missing
  outright; the dream is the tool for the first part, nothing does the second automatically.
- The weekly review needs to move to `search.log` and `usage.log`: searches per session, empty searches, delivered
  facts, the literal-match proxy of `usage.log`.
- The stand is the gate the `eval` verb used to be: prompts and labels stay outside the repository, the script and the
  numbers do not.

## Not measured

- Whether a weaker model searches as readily as Opus over weeks, and whether its queries are as good.
- Whether five facts are read better than ten by the model (only the delivered volume is measured).
- Any base but this one: 432 facts, mostly about one host, so a lexical hit is cheap here and a large base may behave
  differently.
- The end-to-end quality of an answer: this note scores the facts handed over, not the answer built from them.
