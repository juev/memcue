# The message rule on long agent queries, November 2026 (#745)

**On the real agent queries longer than eight words, the message rule of #417 cut no useful fact: Current served a
useful fact at top 5 to 22 of 23 queries, one more than each other arm. Such queries are rare: 25 of the 991 default
`memcue search` calls in this machine's Claude Code transcripts (2.5%), and they run 9 to 12 words.** The #742
paraphrase loss does not show on this set. No arm wins on useful facts, so no change is proposed.

## Data

**Queries.** [extract.py](scripts/messagerule745/extract.py) read 1,398 Claude Code transcripts under
`~/.claude/projects` (subagents included). 573 of them call `memcue search`, 1,055 calls in all:

| | calls |
|---|---:|
| query with a shell variable, substitution or glob | 20 |
| session in a temporary directory (research stands, e2e) | 10 |
| explicit `-min-score` or `-all-words` | 34 |
| default floor, eight words or fewer | 966 |
| **default floor, more than eight words** | **25** |

Of the 25, one resolved to no existing directory and was dropped, two had a removed worktree and took the directory of
the tier that `search.log` recorded for the same query; duplicates left **23 distinct queries**, from 2026-10-27 to
2026-11-12, median 9 words. All of them were measured; there was nothing to sample. The flags `-in`, `-kind`, `-since`
and `-expired` stay as the agent wrote them. One query used `-in project`, removed in #712; it runs with the default
place, as the CLI's error advises.

`search.log` gives a cross-check over every agent of the Mac from 2026-10-25. Leaving out a batch of 2,844 long
searches on 2026-10-29 between 14:00 and 16:00 UTC (median 19 words, no agent call in any transcript), agents made
3,410 searches, 39 of them longer than eight words (1.1%). 22 of those 38 distinct queries are in the set above.

**Base.** A `sqlite3 .backup` of the Mac server-mode replica, taken 2026-11-12T11:31:46Z: 8,239 facts, 3,908 active.
The copy lost its replica role stamp, so the CLI opens it as an ordinary base; facts and the FTS index are untouched.
The facts table hash is the same before and after the run ([method.json](message-rule-745/method.json)).

No query or fact text is in the repository.

## Arms

[build_arms.sh](scripts/messagerule745/build_arms.sh) builds three binaries from `7ab7e24`, each changing one
constant of `internal/recall/search.go`. [run.py](scripts/messagerule745/run.py) runs each query as
`memcue search -json -n 5 -cwd <session directory>` with no `MEMCUE_*` variable but the copy and a scratch state
directory, so scope resolution, the #712 widening and the tie order are the CLI's own.

1. **Current**: the default (two shared terms, floor 0.45).
2. **One term**: `messageTerms = 1`, floor 0.45.
3. **Search floor**: `messageWords = 1 << 30`, so no query reads as a message: floor 0.6, no two-term rule.
4. **Wide net**: Current with `-min-score 0`, for reference.

## Labels

[label.py](scripts/messagerule745/label.py) gave `claude-opus-5-5` (through `claude -p`, no tools, MCP or hooks) each
query with the union of all arms' facts in an order shuffled by query ID, with the U/R/N definitions of #682.
117 pairs, one pass, $2.40.

## Results

[analyze.py](scripts/messagerule745/analyze.py), [results.json](message-rule-745/results.json):

| Arm | U at top 5 | U at top 1 | facts | U | R | N | N share | gain / loss vs Current | same list |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| Current | **22** | 18 | 99 | **57** | 33 | 9 | **9.1%** | — | — |
| One term | 21 | 18 | 101 | 56 | 34 | 11 | 10.9% | 0 / 1 | 21 |
| Search floor | 21 | 18 | 90 | 53 | 27 | 10 | 11.1% | 0 / 1 | 15 |
| Wide net | 21 | 18 | 115 | 57 | 41 | 17 | 14.8% | 0 / 1 | 17 |

A useful fact exists among all candidates for 22 queries, so Current reaches the pool ceiling. No query returned
nothing, and no arm widened.

- **One term** changes 2 lists. It brings in 2 R and 2 N facts and drops 1 U and 1 R. The lost query is the same in
  every arm: there the two-term rule removes facts that share one word and lets a fact holding two words, the useful
  one, into fifth place.
- **Search floor** changes 8 lists and drops 4 U and 6 R facts. These facts sit between 0.45 and 0.6 of the best
  score, so the higher floor cuts them. It adds 1 N.
- **Wide net** adds 1 U, 9 R and 8 N. The useful fact it adds sits below the 0.45 floor; neither the one-term nor
  the search arm returns it.

With 23 queries, Current's zero lost queries against the other arms bound the loss at 14% (Wilson 95% upper bound),
and a one-query difference is within noise. The direction is the same in every comparison, though.

## Why #742 and this set differ

The #742 low-overlap queries were written to share exactly one term with their fact, and 60 of 71 ran past eight
words. On this set One term returns no useful fact that Current lacks, so the agents' long queries
did not depend on a single shared term. Term overlap per fact was not counted.

## Limits

- 23 queries from one person's sessions on one base over about two weeks; the transcripts on disk hold no
  long default query before 2026-10-27.
- One labeller, one pass; the labels measure agreement with Opus, not with a person.
- The base is today's, not the base each query saw when it ran.
- Other agents (Codex, Pi, Hermes) were not measured; the search log suggests they also write long queries rarely.

## How to rerun

```bash
D=docs/research/scripts/messagerule745
python3 -I $D/extract.py ~/.claude/projects ~/.local/state/memcue/search.log queries.jsonl
$D/build_arms.sh 7ab7e24 bin
python3 -I $D/run.py queries.jsonl bin base.db state run.json
python3 -I $D/label.py queries.jsonl run.json labels.json
python3 -I $D/analyze.py run.json labels.json results.json
```

`base.db` is a backup of the replica with `delete from stamps where name='role'` applied. The query set, run and
labels stay outside the repository; a rerun on another day reads other transcripts and another base.
