# Chinese queries: segment, translate, or index a third language, October 2026

Status: **measured 2026-10-25 for [#410](https://github.com/juev/memcue/issues/410), nothing shipped yet.**
A Chinese question finds its fact today only through the Latin names inside it: 50 of 60 in the top five. Translating
the question into Russian and English before search finds 59 of 60 with either language and 60 of 60 with both. A
Chinese translation of every fact, indexed as bigrams, finds 60 of 60 on a 300-fact subset, but costs a translation
per fact and 17 % of index size. A hint "search in the facts' languages" would fire on none of today's misses: every
miss returned five facts, just not the right one.

## Setup

- A live base of 2,855 facts, copied after the translation pass of #395, so every fact carries its Russian or English
  translation.
- The 60 known-item targets of #392, one question each in Russian, English and Chinese (the set of
  [search-floor-2026-10.md](search-floor-2026-10.md)). Scored with the search of 0.14.0 over the whole base, top five,
  floor 0.6: hit@5 is "the target is in the answer", rank 1 is "the target comes first".
- Scripts: [scripts/chinese](scripts/chinese/). The lexer variants (`cjk.go.txt`, `cjk_gse.go.txt`) and the stand
  (`zhstand.go.txt`) were built in a copy of the code with `cjk.go` added to `internal/text`; they are kept as text,
  since main has no such lexer.
- **Pure Chinese questions.** All 60 Chinese questions hold a Latin name, path or version, so none of them comes back
  empty. Opus rewrote each one with no Latin letter and no digit (a tool name becomes a Chinese description) without
  seeing the fact. This is the case the issue describes: a question that today returns nothing.
- **Subset base** for options 1 and 3, because translating all 2,855 facts into Chinese was out of budget: the 60
  targets plus the 240 facts that ranked best (top four, no floor) for any of the 180 questions on the full base. All
  options were run on this subset too, so its numbers compare with each other, not with the full base.
- Lexer variants, built into a copy of the code (`MEMCUE_CJK`), the index rebuilt for each:
  - **bigram**: a run of Han or kana becomes overlapping bigrams, a lone character a unigram. No dictionary.
  - **gse**: the pure-Go dictionary segmenter `github.com/go-ego/gse` v1.1.0 in search mode, Simplified dictionary
    embedded, a short list of function words dropped.
- **Option 2**: Opus translated each Chinese question into Russian and English, one batch, without the facts. Search
  ran on each translation; "either" counts the target in the top five of the Russian or the English search.
- **Option 3**: Opus translated the 300 subset facts into Simplified Chinese, 40 per call, and the translation was
  appended to the fact's `queries` after `= `, the way #395 stores the Russian or English one.
- Model calls: `claude -p --model opus --tools "" --output-format json --setting-sources ""`, no tools, no hooks.
  Model spend for the whole study: $2.36.
- Latency: mean of 100 runs of the search per question, two rounds, one laptop. Process start is not included except
  where stated.

## Results

Chinese questions with Latin names (60), target in the top five / first:

| option | full base | subset base |
|---|---|---|
| now | 50 / 36 | 55 / 34 |
| 1. bigram lexer, no Chinese in the index | 50 / 36 | 55 / 34 |
| 2. question translated to Russian | 59 / 55 | 59 / 54 |
| 2. question translated to English | 59 / 51 | 60 / 51 |
| 2. both searches, either hits | 60 | 60 |
| 3. Chinese translation in the index, lexer as now | – | 56 / 35 |
| **1 + 3. Chinese translation, bigram lexer** | – | **60 / 54** |
| 1 + 3. Chinese translation, gse | – | 60 / 54 |

Pure Chinese questions (60), same scoring:

| option | full base | subset base |
|---|---|---|
| now | 0 (60 empty) | 0 (60 empty) |
| 1. bigram lexer alone | 0 (60 empty) | 0 (60 empty) |
| 2. translated to Russian | 48 / 41 | 54 / 43 |
| 2. translated to English | 52 / 38 | 56 / 44 |
| 2. both searches, either hits | 54 | 57 |
| 3. Chinese translation, lexer as now | – | 0 (60 empty) |
| 1 + 3. Chinese translation, bigram | – | 40 / 30 |
| 1 + 3. Chinese translation, gse | – | 39 / 28 |

Russian and English questions (60 each), top five, before and after:

| base | now | bigram | Chinese + now lexer | Chinese + bigram | Chinese + gse |
|---|---|---|---|---|---|
| full | 58 / 60 | 58 / 60 | – | – | – |
| subset | 59 / 60 | 59 / 60 | 60 / 60 | 60 / 60 | 60 / 60 |

Rank 1 for Russian and English did not move in any variant (57 and 51 on both bases).

Cost, size and latency:

| option | cost | index | search latency | dependency |
|---|---|---|---|---|
| 1. bigram | none | no change: the base holds no Han text | Chinese questions 2.6 → 3.5 ms on the full base; Russian, English unchanged | none |
| 1. gse | none | – | – | +33 MB binary (11.3 → 44.5 MB stand binary), ~0.2 s dictionary load per process |
| 2. by the session model | one instruction in the note and tool descriptions | none | none in memcue | the agent follows it |
| 2. by an endpoint inside `search` | Opus: $0.009–0.011 and 2.6–3.4 s per question alone, $0.003 per question in a batch of 60 | none | +2.6–3.4 s per Chinese query | an endpoint on every such query |
| 3 (+1). Chinese in the index | Opus: $0.0053 per fact in batches of 40 | subset 2.57 → 3.01 MB (+17 %, bigram), 2.96 MB (+15 %, gse), ≈1.5 KB per fact | Chinese questions 0.49 → 1.45 ms (bigram), 1.10 ms (gse) on the subset; Russian, English +0.05 ms | a translation pass and a third language per fact |
| 4. hint when nothing fits | none | none | none | the agent retries |

- **Option 1 alone changes nothing**, as expected: the base has no Han text, so the full-base stems are byte for
  byte the same and every Chinese bigram matches nothing. It only adds the cost of looking the bigrams up.
- **Option 2 is the best on both question sets** and touches neither the index nor Russian and English search. Its
  six misses on pure questions were not read one by one; a likely cause is a name the rewrite turned into a
  description ("a ledger language server"), which no translation brings back.
- **Option 1 + 3 closes the gap for questions with names** (60 of 60), but on pure questions it finds 40 of 60, below
  translation. Bigrams and the dictionary score the same; bigrams need no dependency.
- **Option 4 would not have fired on today's misses.** Of the 10 Chinese questions that miss their target on the full
  base, all 10 returned five facts, matched by a Latin name shared with other facts. The hint helps only the pure
  questions, where all 60 come back empty.

## Decision

Recommended (not decided): **option 2 through the session model, with option 4 as the safety net.** The note and
the tool descriptions say to write the query in the language of the facts. When a query holds CJK text, the answer
says so as well, whether the search found something or not, since a CJK query that does find facts is matched only
on its Latin names. Options 1 and 3 are not worth their cost while no agent has written a Chinese query: a third
translation per fact and +17 % index for a result below translation. If a Chinese-speaking user does appear, bigrams
are the segmentation to take, not a dictionary.

## Not measured

- **Whether an agent follows the instruction.** Option 2 here is Opus translating on request. Whether the session
  model searches in the facts' languages by itself after a note says so was not run.
- **Option 3 on the whole base.** The subset holds the nearest Russian and English competitors of each target, not
  its nearest Chinese ones, and IDF on 300 facts differs from 2,855. Option 3 numbers are likely optimistic. The
  index size for the full base (≈4 MB more) is extrapolated from the subset, not measured.
- **Translation by the production endpoint.** All translations were done by Opus; DeepSeek or another model may
  translate facts and questions differently and costs differently.
- Japanese and Korean. Kana is in the bigram range; nothing in Japanese or Korean was asked.
- Pure Chinese questions are a model's rewrite of model-written questions. One labeller, one pass.
