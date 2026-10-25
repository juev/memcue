# The floor on a message: two changes, October 2026

Status: **measured on a copy of a live base, 2026-10-26.** One change for
[#417](https://github.com/juev/memcue/issues/417). The corpus is the same twenty user prompts and the eight agent
queries as [pull-hermes-014](pull-hermes-014-2026-10.md), so the numbers compare to that note directly; the grading is
one fresh pass by one rater (the agent that wrote these notes), with the same three labels — `U` answers the prompt,
`R` is on the topic, `N` is unrelated — applied to every arm alike. The base: **391 live facts** by id, **386 approved**
(the count vet sees), 176 characters on average. Names of private hosts and repositories are placeholders.

## What changed

1. **The floor of a message is 0.45, not 0.6.** A query of more than eight words reads as a message: it holds dozens of
   words, several of them common in the base, the best fact's BM25 is inflated by every word it shares with the message,
   and a floor that is a share of that best cuts facts answering one part of it. A search — eight words or fewer — keeps
   the 0.6 of #407 unchanged, and an explicit `-min-score` is the caller's own choice and is kept as it is.
2. **A fact has to hold two of the message's terms, not one.** A fact sharing a single word with a message of dozens is
   usually about something else; the fact that answers one part of the message shares several. `-min-score 0` stays the
   wide net: with the floor off, neither rule applies.

## User prompts (20), top five

| | 0.14 (floor 0.6) | this change |
|---|---|---|
| facts delivered | 65 | **73** |
| useful (`U`) | 11 | **17** |
| on topic (`R`) | 31 | 26 |
| unrelated (`N`) | 23 | 27 |
| prompts keeping a useful fact | 10 of 20 | **12 of 20** |
| useful facts lost to the floor | 8 of 19 | **2 of 19** |
| characters delivered | 11 000 | 13 257 (+20 %) |

The two cases the issue names are both fixed: the NAS question now returns **both** restic facts that name the backup
repository and its path, and the «ошибка изменения файла» question returns the fact that names `stale_write_blocked`.
The two still lost are rules about the skills repository, in messages where the useful fact sits at 0.32 and below of a
best fact inflated by a long tail of common words.

## Agent queries (8), top five: unchanged

The same 26 facts, the same ids, the same 5 197 characters as before: every agent query is a few words, so neither
rule can fire. Precision on the corpus an agent writes is exactly what 0.14 gave — 11 useful of 26, one unrelated.

## What was tried and dropped

- **Rare terms only in a long query** (dropping words held by more than 10 % of the base before BM25): the smaller
  term set broadens what matches, so the answer grew to 68 facts, 31 of them unrelated against 23 before, and the
  useful ones rose by two — the opposite of the intent.
- **A floor scaled by the number of terms** (0.6 × 8/n): it changed nothing on the case the issue is about, because
  that message holds only eight terms after the stop words are gone.
- **A gate on one distinctive term** (a fact had to match a term held by at most 15–25 % of the base): no effect on
  this corpus; the useful facts of a message are short and their terms are common.
- **Lowering the floor for every query** instead of only for messages: it cost the agent queries their precision
  (`limit` 5 returned 32 facts instead of 26 on the same eight queries) for nothing the messages needed.

## Where the acceptance is not met

The issue asks for **≤ 20 unrelated of the delivered set** and ≤ 2 useful facts lost. The second holds (2 of 19); the
first does not: 27 unrelated of 73 against 23 of 65 before. The reason is structural and worth writing down: below the
best fact the BM25 tail of a message is flat — the second fact of a message sits at 0.32–0.53 of the best one, the
tenth at 0.14–0.39 — so no share of the best separates a fact that answers one part of the message from one that only
shares its vocabulary. With that, a lower floor buys useful facts and unrelated ones together: the run above buys six
useful for four more unrelated. The floor is now where that trade is best on this corpus; the remaining noise needs a
signal BM25 does not have, which is a ranker, not a floor.

One observation of the same family, measured and **not** turned into an issue: the index of a fact carries the
extraction's `queries` field, a list of phrasings written for it, so a fact matches words its text does not hold — the
best fact of the NAS question is the size of an old memory file, and it matches «сколько», «занимает» and «база» only
through its list («размер старой базы, сколько килобайт, вес базы»). That alone is not a bias, though: 388 of the 391
live facts carry such a list, and the top fact of the twenty prompts carries 6.5 phrasings against 6.1 in the base, so
the finding needs a corpus where the lists differ in length before it can be called one.

## Tests

`go vet ./...` clean, `go test ./...` all packages ok. `TestQueryFloor` pins the floor of a search, of a message, of
an explicit `-min-score` and of the wide net; `TestSearchMessageLeavesOutAFactOfOneTerm` pins the two-term rule and its
escape hatch — a fact that shares one word with a message is left out of the answer, and `-min-score 0` returns it.

## Reproduction

```sh
cp facts.db /tmp/facts.db
MEMCUE_FACTS=/tmp/facts.db MEMCUE_STATE_DIR=/tmp/state \
  memcue search -json -n 5 -- "сколько свободно на диске на nas? Что больше всего занимает и сколько весят бекапы с loffe?"
MEMCUE_FACTS=/tmp/facts.db MEMCUE_STATE_DIR=/tmp/state \
  memcue search -json -n 5 -min-score 0 -- "<the same message>"
```
