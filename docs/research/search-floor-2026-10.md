# A floor for search: N as a maximum, October 2026

Status: **measured 2026-10-25 for [#407](https://github.com/juev/memcue/issues/407), shipped as the default.**
`memcue search` handed over exactly N facts (five by default) whenever the base had that many matches. When one fact
fitted the query, four unrelated ones came with it. The floor below drops a fact whose BM25 is under 0.6 of the
best fact's. On queries that agents wrote, it removed a third of the unrelated facts and a quarter of the facts on
the topic that did not help. It kept a useful fact for every query that had one, and the characters per search fell
by 17 %.

## Setup

- One live base of 2,855 facts, copied after the translation pass of #395, so every fact carries its translation.
- [searchfloor](scripts/searchfloor/main.go) runs `recall.Search` with no floor, top ten. For each fact it writes
  what a floor could be built on: the BM25 score, its share of the best fact's score (`ratio`), the IDF-weighted
  share of the query's terms the fact holds (`coverage`), and the number of matched terms. Each candidate floor is
  then applied offline over the same hits ([eval.py](scripts/searchfloor/eval.py)), with N = 5.
- Query sets:
  - **35 queries agents wrote** to `memcue search`, from recent Claude Code transcripts: the input search actually
    gets in pull mode. Opus labelled every one of their top ten facts: `U` helps with the query, `R` on the topic
    but no help, `N` unrelated.
  - **180 known-item questions**: the 60 target facts of #392, one question each in Russian, English and Chinese.
    Scored as "the target is in the result".
  - **15 version-only queries** of #391. Scored as "a fact naming the version is in the result".
  - **25 user prompts** were measured too, but a user's message is not what search gets. They are long, so every
    floor built on coverage empties them. They are left out of the decision.

## Results

Queries agents wrote (35), N = 5:

| floor | queries with a useful fact | useful | on topic | unrelated | facts per search | characters |
|---|---|---|---|---|---|---|
| none (0.13.0) | 32 | 92 | 64 | 19 | 5.00 | 1,391 |
| ratio ≥ 0.5 | 32 | 90 | 58 | 18 | 4.74 | 1,311 |
| **ratio ≥ 0.6** | **32** | **87** | **47** | **12** | **4.17** | **1,158** |
| ratio ≥ 0.7 | 32 | 81 | 37 | 5 | 3.51 | 988 |
| ratio ≥ 0.8 | 31 | 65 | 30 | 3 | 2.80 | 813 |
| coverage ≥ 0.5 | 30 | 76 | 50 | 7 | 3.80 | 1,085 |
| coverage ≥ 0.6 | 27 | 66 | 32 | 3 | 2.89 | 867 |
| two or more terms matched | 30 | 87 | 53 | 6 | 4.17 | 1,194 |

Known items (target in the result, Russian / English / Chinese questions, 60 each) and version-only queries:

| floor | known items | version-only |
|---|---|---|
| none | 59 / 60 / 50 | 15 / 15 |
| ratio ≥ 0.6 | 58 / 60 / 50 | 15 / 15 |
| ratio ≥ 0.7 | 58 / 60 / 50 | 15 / 15 |
| coverage ≥ 0.7 | 55 / 54 / 50 | 15 / 15 |
| two or more terms matched | 59 / 60 / 50 | **0 / 15** |

- **The ratio floor never takes the whole answer away.** Up to 0.7, every query that had a useful fact in its top
  five keeps one, and no query comes back empty. A coverage floor empties two queries already at 0.5, and a
  minimum of two matched terms drops every version-only query, because a version is one term.
- **At 0.6**, the five useful facts lost all sat at 0.41–0.59 of the best score. Three queries got one fact, three
  got two, 23 kept five.
- **At 0.7** the unrelated facts almost vanish (19 → 5), but 11 useful facts of 92 go with them. The rest of the
  answer is then thin for a query that wanted a list.

## Decision

`search` drops a fact whose BM25 is below 0.6 of the best one's (`recall.MinScore`), and `-n` is the most it returns.
`-min-score 0` turns the floor off. A text answer with nothing left says "No stored facts match.". The session note
of Claude Code, the Hermes `memcue_search` description and the DSH `limit` say that fewer facts than asked means the
rest did not fit.

Not measured: queries of the Hermes agent, a base other than this one, and a second labeller. One labeller, one pass.
