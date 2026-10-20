# Dream triage research, October 2026

Measurements of 2026-10-20 on one private base of about 2500 facts, mostly Russian, 270 characters on average. The
document gives aggregate numbers only: the base holds host names, paths and private repositories. Scripts are in
[scripts/](scripts/); each section names the one that produced its numbers. Prices: Jev about 4.7 RUB per million
input tokens on RouterAI, Opus through `claude -p`.

## Question

A full `memcue dream -all -dry-run` over 2480 facts sent 393 groups to Opus. It cost $7.58 and took 13 minutes. Opus
proposed 66 actions (40 retire, 26 correct) in 43 groups, so most groups were paid for and changed nothing. A daily
dream costs much less: $0.13 for 118 new facts, $0.30 for 47.

Two questions followed:

1. Can a cheap model drop the groups that need no action before Opus sees them?
2. Are lexical neighbours enough, or does a dream need vectors to find duplicates, as other memory systems use?

## How other systems do it

Read from their source code.

| | Mem0 v2 (`0724291`) | Graphiti (`3cc723c`) | memcue |
|---|---|---|---|
| Candidates | embedding of the new messages, top 10, no threshold | BM25 and cosine merged by RRF, top 10, cosine >= 0.6 | FTS5 terms weighted by IDF, coverage >= 0.35, up to 3 neighbours |
| Deterministic dedup | MD5 of the text | normalised text, same entities | none |
| Who decides | the LLM | the LLM, one call per new fact, no dates shown | Opus, ten groups per call |
| Outdated facts | none since v2.0: memories only accumulate | the LLM says "contradicted"; code compares dates and invalidates the earlier fact | the model compares the dates itself |

Neither system treats a high cosine as a duplicate. Vectors find candidates; a model decides.

## How TypeSafe documents Jev

From [docs.typesafe.ai/llms-full.txt](https://docs.typesafe.ai/llms-full.txt):

- Duplicate check: the new record goes in `state`. Each candidate gets its own `noul`, with the candidate in an
  `instructions` field such as `potential_duplicate`.
- One condition per `noul`.
- A `false` criterion should rule out "merely on a similar topic".
- A `none of the above` option in `choice` "may not work well": the probabilities of a `choice` sum to 1, so some
  option always ranks first. Use a companion `noul` instead.
- The model reads dates as text: extract them and compare in code.
- English is the main training language. `jev-latest` is 1.13; 1.14 is `jev-preview`.

Our first three attempts below broke most of these rules.

## Experiments

### 1. One `choice` per dream group ([group_choice.py](scripts/group_choice.py))

Each of the 395 groups of `memcue dream -all -list` went to Jev as one `choice` with options `duplicate`, `outdated`
and `none`. A group passes when P(duplicate) + P(outdated) reaches the threshold. Labels: whether the Opus dry run
acted on any fact of the group (43 groups).

| Threshold | 1.13, no dates | 1.14, no dates | 1.14, with written date and origin |
|---|---|---|---|
| 0.2 | 191 groups, 42/43 | 208, 42/43 | 194, 43/43 |
| 0.3 | 148, 37/43 | 175, 41/43 | 158, 42/43 |
| 0.4 | 117, 36/43 | 138, 39/43 | 134, 42/43 |
| 0.5 | 97, 33/43 | 110, 39/43 | 117, 39/43 |

Baseline by group size: sending groups of 3+ facts passes 221 groups and keeps 34/43. A run costs 363k–457k input
tokens, about 2 RUB.

This shape was merged as opt-in triage (#6, PR #11) and stays off by default.

### 2. Pairwise `noul` with candidates in `state` ([pairwise_noul.py](scripts/pairwise_noul.py))

Candidates: recall with coverage >= 0.2, up to 10 per fact. That gives 7862 pairs, covering all 60 pairs named in
the reasons of the Opus dry run. One request per fact carried all its candidates in `state`, with two `noul` per
candidate: "same thing or covers" and "outdated by the later one". The run took 1908 requests, 3.38M tokens, about
16 RUB.

| Threshold | Pairs | Opus pairs kept | Groups after joining (max 6 facts) |
|---|---|---|---|
| 0.3 | 1381 | 57/60 | 409 |
| 0.5 | 595 | 48/60 | 296 |
| 0.6 | 358 | 45/60 | 216 |

This sends more groups to Opus than experiment 1, not fewer. The "outdated" question alone kept 9/60 at 0.3. Of six
high-scoring pairs outside the Opus labels, one was a real duplicate, two overlapped and three were neighbours on one
topic. Negative result.

### 3. One `choice` over up to 30 neighbours ([choice_neighbours.py](scripts/choice_neighbours.py))

This follows Mem0: which neighbour does the new fact repeat or outdate, or none. Candidates came from recall with
coverage >= 0.1, 20.8 per fact on average. The run took 2490 requests, 13.5M tokens, about 63 RUB. The median
P(none) was 0.05, and only 6% of facts had P(none) >= 0.5. At 0.5 the step would pass 1741 of 2501 facts, keeping
47/60 Opus pairs. Of 12 sampled pairs scored >= 0.8 outside the labels, 2 were duplicates, 3 overlapped and 7 were
neighbours. Negative result: a `choice` ranks the options, so it says which neighbour is closest, not whether any is
a duplicate.

### 4. Vectors ([vectors.py](scripts/vectors.py), [vectors_check.py](scripts/vectors_check.py))

`baai/bge-m3` on RouterAI embedded all facts (238k tokens). We took the top 10 neighbours of each fact by cosine.

| Cosine | Top-10 pairs | Not among lexical candidates (0.2, 10) | Not among lexical candidates (0.1, 30) |
|---|---|---|---|
| >= 0.80 | 160 | 3 | 1 |
| >= 0.75 | 831 | 95 | 18 |
| >= 0.70 | 2949 | 1031 | 344 |

The 60 Opus pairs have cosine 0.65–0.92, median 0.79, tenth percentile 0.69. Their range overlaps topical neighbours,
so no cosine threshold separates them. Only one of the 107 vector-only pairs >= 0.72 mixed Russian and English, and a
sample of 10 had no duplicate.

**Mistake in this comparison.** The labels came from Opus over lexical groups. Opus never saw a pair only vectors
would find, so this test could not show what vectors add. Experiment 6 repeats it without that bias.

### 5. Public pair sets ([public_sets.py](scripts/public_sets.py))

This checks whether the tools can tell a duplicate from a neighbour at all, on 200 pairs per set with known answers.
Jev is used the documented way: one record in `state`, the other in `instructions`, one condition, and a `false`
criterion for "same topic, different fact". The run took 508k tokens, about 2.4 RUB.

| Set | Jev 1.13 AUC | Jev 1.14 AUC | bge-m3 cosine AUC | Mean Jev score by class |
|---|---|---|---|---|
| PAWS-X en: paraphrase vs same words, other meaning | 0.903 | 0.901 | 0.684 | other 0.42, paraphrase 0.91 |
| ru_paraphraser: precise / near / not | 0.910 | 0.910 | 0.882 | not 0.12, near 0.54, precise 0.84 |
| XNLI ru: contradiction vs the rest | 0.967 | 0.965 | 0.214 | entailment 0.12, neutral 0.12, contradiction 0.61 |

Cosine sees similarity, not meaning. On PAWS-X both classes score about 0.97, and on XNLI a contradiction is closer
than an entailment. Jev asked the documented way separates both cases. 1.13 and 1.14 do not differ. The sets are
public and may be in Jev's training data, and their sentences are shorter than our facts.

### 6. Blind labels on our base ([blind_sample.py](scripts/blind_sample.py), [blind_label.py](scripts/blind_label.py), [blind_jev.py](scripts/blind_jev.py), [blind_eval.py](scripts/blind_eval.py))

We drew 300 pairs from five sources: a fact's first lexical neighbour, a random lexical neighbour, a fact's first
vector neighbour, a random top-10 vector neighbour (50 each), and 100 random pairs within one repository. Each pair
was shown in random order, without its source. Opus labelled every pair as duplicate, outdated or different, with
dates ($2.63). Jev 1.14 scored the same pairs the documented way, with no dates (259k tokens).

| Source | Duplicate | Outdated | Different |
|---|---|---|---|
| lexical, first neighbour | 6 | 0 | 44 |
| lexical, random neighbour | 1 | 1 | 48 |
| vector, first neighbour | 3 | 1 | 46 |
| vector, random top-10 | 0 | 0 | 50 |
| random, same repository | 0 | 0 | 100 |

All 12 positive pairs are also lexical candidates, including the four drawn from vectors.

| Jev score | AUC |
|---|---|
| any action, max of the two `noul` | 0.916 |
| duplicate, `noul` "same fact" | 0.965 |
| outdated, `noul` "incompatible" (2 cases) | 0.876 |
| bge-m3 cosine, for comparison | 0.867 |

| Threshold | Positives kept | Pairs sent on | Precision |
|---|---|---|---|
| 0.3 | 11/12 | 75/300 | 0.15 |
| 0.5 | 7/12 | 37/300 | 0.19 |
| 0.7 | 5/12 | 12/300 | 0.42 |

With 12 positives these numbers are rough. A threshold tuned on one half (0.15) kept 4/4 on the other half and sent
on 62 of 150 pairs.

## Mistakes in the method

- Labels drawn from lexical groups biased the vector comparison (experiment 4).
- Thresholds were tuned on the data they were measured on, in experiments 1–3.
- The Opus labels of experiments 1–4 come from one dry run. On the same base a dry run proposed 13 actions and the
  real run 11.
- The prompts of experiments 1–3 went against the Jev documentation: candidates in `state`, two conditions in one
  question, `none` inside a `choice`, dates for the model to compare.

## Conclusions

- **No vectors.** On this base every duplicate found was also a lexical candidate. Facts written by one author in one
  base share their words. Mem0 and Graphiti compare free conversation, where paraphrase is common.
- **Jev works when asked the documented way.** AUC is 0.97 for duplicates on our blind pairs and 0.90 on PAWS-X. It
  ranks well but raises many false alarms, so Opus is still needed after it.
- **Little to save.** At threshold 0.3 a daily dream would cost $0.1–0.2 less, about $3–6 a month. That costs about
  one finding in twelve, a second provider receiving the facts, and more code. Triage stays opt-in and off by
  default. Pairwise `noul` triage was not built.

## Reproducing

The scripts read their inputs from arguments and write their outputs to files named on the command line. A key is
read from a file whose path is given. The vector scripts need numpy: `uv run --no-project --with numpy python -I`.
The other scripts run with `python3 -I`. Neighbour candidates come from a Go program built only with a tag:

```bash
go run -tags research ./docs/research/scripts/candidates FACTS.db 0.2 10 > candidates.jsonl
memcue dream -all -list > groups.txt
memcue dream -all -dry-run   # its dream.log record holds the Opus actions the labels come from
```

Inputs and outputs contain the facts of the base. Keep them out of the repository.
