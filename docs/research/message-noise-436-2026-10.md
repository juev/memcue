# What a message's noise is made of: two mechanisms tried and dropped, October 2026

Status: **measured on a copy of a live base, 2026-10-26.** The work [#436](https://github.com/juev/memcue/issues/436)
asked for, in its own two steps. Placeholders stand for private hosts and repositories.

The base: **397 live facts**, 176 characters on average. Corpus and grading: the same 20 user prompts and 8 agent
queries as the earlier notes, one grading pass per arm by one rater, `U` answers, `R` is on the topic, `N` unrelated.
All arms run with `-in all`, the host named, top five.

## The starting point (0.15.0)

| | 20 prompts | 8 agent queries |
|---|---|---|
| facts delivered | 74 | 27 |
| useful | 16 | 15 |
| unrelated | 29 | 4 |
| useful facts left out | 1 | 1 |

## Step 1: an index without the extraction's `queries` field

The index of a fact carries its text, its keywords **and** the extraction's list of phrasings, so a fact matches words
its text does not hold: the best fact of the NAS question — the size of an old memory file — matches «сколько»,
«занимает» and «база» only through its list. An index built from the text and the keywords alone, the same base, the same
binary:

| | with the list | without it |
|---|---|---|
| facts delivered (20 prompts) | 74 | 68 |
| useful | 16 | **14** |
| unrelated | 29 | **22** |
| useful facts left out | 1 | **3** |
| facts delivered (8 queries) | 27 | 24 |
| useful | 15 | 14 |
| unrelated | 4 | 2 |

The list costs seven unrelated facts on the prompts and buys back two useful ones, and on a message the answering fact is
sometimes found *only* through the list: the field is load-bearing, and dropping it is a trade, not a gain.

## Step 2: a rerank for a message, only if step 1 held

Step 1 did not hold, but the second mechanism was measured anyway, because it does not need the index to change: take the
BM25 top 10 × n for a message, score each fact by the IDF weight of the query terms it actually holds, order by that and
cut with the same floor.

| | today | reranked |
|---|---|---|
| facts delivered | 74 | 86 |
| useful | 16 | 13 |
| unrelated | 29 | about 40 |
| useful facts left out | 1 | 4 |
| latency inside the call | ~15 ms | ~32 ms |

Coverage of rare words is not what a message needs either: a message names several subjects, and a fact that covers one
of them well still has to beat facts that cover two of them badly.

## What is left

The noise of a message is what BM25 cannot separate: with the tail flat (the second fact at 0.32–0.53 of the best, the
tenth at 0.14–0.39) the share of the best cannot tell a fact that answers one part of a message from one that shares its
vocabulary, and neither the index nor a coverage score changes that. The next mechanism worth trying is a model-based
ranker over the BM25 top ten — the one removed in 0.8.0 as too expensive — and the price to beat is now measured: a
second process of 15–21 ms is acceptable on a thin answer, so a ranker that costs one extra model call per *message* has
to win at least the 29 unrelated facts of 74 to pay for itself. The issue stays open with this written down.

## Reproduction

```sh
cp facts.db /tmp/facts.db
python3 docs/research/scripts/search_stand.py --prompts prompts.json --base /tmp/facts.db --binary ./memcue --sweeps 5 --repeat 3
# an index without the queries field: rebuild with the field left out, then run the same stand on that copy
```
