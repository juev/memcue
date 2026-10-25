# One language for the base: duplicates, storage and search, October 2026

Status: **measured 2026-10-25 for [#392](https://github.com/juev/memcue/issues/392); nothing shipped.** Facts are
stored in the language of the session or note they came from, so a user who works in two languages gets a base in
two languages. The fear was that two copies of one fact in different languages never meet, in the dream or in
search. Grouping does not suffer from this: a cross-language pair on one subject is grouped as often as a
same-language one. Search does lose something, in one combination: a Russian fact asked about in English. That is
not a rare case, because the agent writes about half of its own search queries in English. Storing everything in
English closes the gap. Storing everything in Russian does not. Keeping the original text and adding its
translation to the index closes it as well, without changing what the user reads.

## Setup

- One live base (schema 5): **2 856 active facts**, 2 464 (86%) mostly Russian and 392 (14%) mostly English,
  counted by letters outside backticks. 2 747 of the 2 817 facts that have search words (`queries`) already mix
  both languages.
- Binary: `main` at 246301f (0.11.1 with #390 and #391).
- Model work through `claude -p` with Opus 5.5: pair labels, questions, translations. One labeller, one pass.
- Scripts: [langpairs](scripts/langpairs/main.go) (pairs, Go, `-tags research`), [lang/](scripts/lang/) (the
  prompts and the variant scorer).

## 1. Does the dream meet a cross-language duplicate?

`langpairs` runs, for every active fact, the search the dream groups by (`groupNeighbours`: the fact's text and
keywords as the prompt, `MinContained` 0.8) and lists the linked pairs. Separately, it lists the pairs that share
at least two rare identifiers (paths, versions, backticked names, snake_case or dotted words; held by at most five
facts). It finds those without the lexer, so a pair with no common stems is still listed. 620 such pairs; the
labeller judged all 103 Russian–English pairs, all 37 English–English pairs and 120 of the 480 Russian–Russian ones:

- `D`: the same fact restated;
- `S`: the same subject, one updates, contradicts or contains the other;
- `N`: different facts that share names.

| pair | labelled | D | S | D + S grouped by the dream | D + S grouped, `queries` emptied |
|---|---|---|---|---|---|
| Russian–Russian | 120 | 1 | 36 | 24 of 37 (65%) | 23 of 37 |
| Russian–English | 103 | 5 | 39 | **37 of 44 (84%)** | 32 of 44 |
| English–English | 37 | 1 | 13 | 9 of 14 (64%) | 9 of 14 |

The cross-language pairs are grouped at least as often as the others, mostly through the identifiers they share:
emptying `queries` on a copy and rebuilding the index loses 5 of the 37. Plain duplicates are rare in every
column (7 of 260): earlier dreams have already removed them.

The limit of this part is its selection. A pair enters the table only if it shares two identifiers. A
Russian fact and its English restatement that share no name at all cannot be found this way. Part 2 measures that
case from the search side.

## 2. Search by the language of the question

60 target facts (30 Russian, 30 English, 80–400 characters). For each, the labeller wrote the question the user
would ask without the fact at hand, once in Russian and once in English, keeping tool names and leaving the answer
out (`question_prompt.md`). The score is whether the target is in the top five of `memcue search -n 5`, and at
which rank.

**On the whole base (variant A, as stored today):**

| fact | question | in top 5 | first |
|---|---|---|---|
| Russian | Russian | 29 / 30 | 27 |
| Russian | English | **22 / 30** | **12** |
| English | Russian | 30 / 30 | 28 |
| English | English | 30 / 30 | 26 |

A Russian question finds English facts because it keeps the English tool names. An English question about a
Russian fact has only those names to go on, and every other word of it misses.

**On a sub-base**: the 60 targets and 340 random other facts, rebuilt in each variant. Every fact was translated
both ways (`translate_prompt.md`, identifiers kept). Hit counts are top-5, with first places in brackets:

| variant | content | `queries` | Ru fact, Ru q | Ru fact, En q | En fact, Ru q | En fact, En q | all |
|---|---|---|---|---|---|---|---|
| A | as stored | as stored | 30 (29) | 25 (20) | 30 (30) | 30 (28) | 115/120 |
| A, no queries | as stored | empty | 30 (29) | 24 (20) | 30 (30) | 30 (28) | 114/120 |
| B | English | as stored | 30 (29) | **30 (30)** | 30 (30) | 30 (28) | **120/120** |
| B, no queries | English | empty | 30 (27) | 30 (30) | 30 (29) | 30 (28) | 120/120 |
| C | Russian | as stored | 30 (29) | 26 (20) | 30 (30) | 30 (28) | 116/120 |
| C, no queries | Russian | empty | 30 (29) | 26 (20) | 30 (30) | 30 (28) | 116/120 |
| E | as stored | stored + translation | 30 (29) | **30 (30)** | 30 (30) | 30 (28) | **120/120** |
| E, no queries | as stored | translation only | 30 (29) | 30 (30) | 30 (30) | 30 (28) | 120/120 |

In C, an English question about a fact that is now Russian (cell "En fact, En q") still finds it, through the
identifiers. Across all variants, only the "Ru fact, En q" column moves. English content (B) and a translation in
the index (E) fix it. The bilingual `queries` of today add one hit of five: they are keywords, not the prose a
question is made of. The top five costs about the same in every variant (1 230–1 340 characters); English content
is 7% longer in characters than the stored mix. Tokens were not counted.

## 3. Who asks in English

In pull mode the agent writes the query itself. Queries passed to `memcue search` in recent transcripts of one
installation: 20 English, 14 Russian, 7 mixed. In the search log of another installation: 16 English, 14 Russian,
7 mixed. Many English queries are names and versions, which are language-neutral, but prose queries in English
are common. The weak cell of part 2 is therefore a real case: the user writes in Russian, and the agent searches
in English.

## 4. A question in Chinese

The agent of the second installation runs on a Chinese model (DeepSeek), and none of its 37 queries was in
Chinese: the query follows the language of the conversation and of the facts it reads, not the origin of the model.
Still, the case was measured. The labeller wrote the 60 questions of part 2 once more in Simplified Chinese, with
technical names kept (`question_prompt.md`, the language line changed). Results on the whole base:

| fact | question | in top 5 | first |
|---|---|---|---|
| Russian | Chinese | 20 / 30 | 13 |
| English | Chinese | 29 / 30 | 23 |

The lexer splits on spaces and punctuation, so a Chinese phrase becomes one long word that matches nothing. The
search runs only on the Latin names, paths and versions inside the question, and those are enough for most
targets. A Chinese question with no such name returns nothing (`如何在服务器上更新反向代理配置`: no result), and a
translation into English or Russian in the index would not change that.

## Options

- **A (now).** Search loses a quarter of the Russian facts when the agent searches in English
  (22 of 30 on the whole base). The session note already says to try "both languages", and that helps only when
  the agent does retry.
- **B (English).** Closes the gap in the measurement. The user reads facts, the card and the dream's work in a
  second language, and a quoted decision loses its wording. Extraction needs a translation step or a changed
  instruction.
- **C (Russian).** Changes nothing measurable: the Russian facts were already Russian, and the English facts
  were already found.
- **D (Chinese or another dense language).** Not run. The lexer splits on spaces and has no segmenter for
  Chinese, so lexical search would break before tokens could be saved.
- **E (original text, translation in the index).** Closes the gap like B and keeps the stored text as it is.
  The translation goes where `queries` go today, so the schema stays as it is. The cost is a longer extraction
  output per fact, and a pass over the existing facts (the way `dream -all` added `queries`).

## Not measured

- Tokens per variant. The tokenizer was not available offline, and only characters were counted.
- Whether a translation written by the extraction model in the same call is as good as the separate call used
  here.
- Another base, or a user with a different mix of languages. 60 targets from one base, and questions written by
  a model that saw the fact, which flatters lexical search in every variant alike.

## 5. Variant E on the whole base, after #395

#395 shipped variant E: the session extraction writes the translation into `queries` under `= `, and
`TranslateFacts` translates stored facts. The pass over a base is left to the regular dream (#396), so this run used
the pass from the #395 branch before it was dropped (batches of 40, 4 calls at once, Opus through `claude -p`).
The base is a copy of the live one with 2 855 facts, and the questions are the 60 targets of part 2, in three
languages.

| fact | question | before: top 5 (first) | after: top 5 (first) |
|---|---|---|---|
| Russian | Russian | 29 (27) | 29 (27) |
| Russian | English | **22 (12)** | **30 (23)** |
| Russian | Chinese | 20 (13) | 21 (13) |
| English | Russian | 30 (29) | 30 (30) |
| English | English | 30 (26) | 30 (28) |
| English | Chinese | 29 (23) | 29 (23) |

- **The pass.** 2 855 facts in 72 calls, $19.92, 20 minutes, none failed. DeepSeek flash through `MEMCUE_API_URL`
  did 80 facts in 15 seconds.
- **The base.** 17 MB grew to 29 MB, version rows from 10 749 to 13 604, index stems from 962 554 to 1 496 047
  characters.
- **Other prompts.** Version-only prompts gave 15 of 15 before and after. The 25 real prompts of
  [pull-hermes-010](pull-hermes-010-2026-10.md)'s stand returned 29 774 and 30 282 characters: search output does not
  carry the queries.
- **Latency.** Search p50 went from 56 to 84 ms on the 0.11.1 code, because the per-open index check grows with the
  version rows. With the stamp of #396 (e20adcc) it is 9.7 ms before and 10.0 ms after on the same two copies.
- **Extraction cost.** Three sessions that had given facts before, one run each through `claude -p` and Opus, cost
  $2.39 on `main` and $2.48 with the translation (+4%). All 33 facts of the second run carried a translation. The
  fact counts differ between runs (26 and 33); with one run per session that is the model's spread and not an
  effect of the change.

The questions were written by a model that saw the fact, as in part 2. The Chinese column moves by one, which is
inside the noise: a translation into Russian or English gives a Chinese question nothing new.
