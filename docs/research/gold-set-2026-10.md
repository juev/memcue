# Gold set: which reader of the base earns its keep, October 2026

Measurements of 2026-10-21 on the base the Hermes gateway uses (233 active facts, mostly Russian, 203 characters on
average) and 120 real prompts from that gateway. Aggregate numbers only: the base holds host names, paths and private
repositories.

## Why the old set could not answer the question

`docs/evaluation.md` counts labels on the pairs a *lexical* run produced: 135 pairs, a judge saying USEFUL, RELATED
or NOISE. That answers "how good is the lexical answer". It cannot answer "is another reader better", because every
pair it holds was chosen by the system under comparison: a semantic reader can only keep or lose those wins, and its
own finds are invisible. Measured three ways on that set, the three readers looked nearly the same (11, 11 and 21 of
21 prompts), which is an artefact of the instrument, not a property of the readers.

## Protocol

For each prompt the candidate pool is the union of what three readers propose:

- lexical: FTS5 over stems, BM25 order, top 10, no coverage gate;
- vector: `bge-m3` (1024), cosine of the prompt against `content + keywords`, top 10;
- judge: Jev 1.14 over the whole base, one `noul` per fact, top 10 with probability >= 0.3.

120 pools, 2538 pairs, 21.1 per prompt. The order inside a pool is shuffled deterministically, so the judge that
labels never sees which reader proposed a fact. Labels are the definitions of `docs/evaluation.md`: USEFUL (helps
answer the prompt), RELATED (on topic, not needed), NOISE. The judge was DeepSeek: 242 688 input and 298 661 output
tokens for the whole set, about $0.2.

A pool also gives the ceiling: for 96 of 120 prompts a USEFUL fact was somewhere among the ~21 candidates, and for 24
prompts nothing in the pool helped.

## Results

Prompts that got a USEFUL fact, and the mixture of the facts each reader put first:

| reader, top-1 | useful prompts | USEFUL / RELATED / NOISE |
|---|---|---|
| lexical (FTS5) | 44 / 120 | 44 / 38 / 38 |
| vector (bge-m3) | 21 / 120 | 21 / 37 / 62 |
| judge over the whole base (Jev) | 12 / 120 | 12 / 34 / 56 |

At top-2: lexical 56, vector 28, judge 22. So neither semantic reader replaces the words, and the pair that is the
point of such systems — "found by meaning, missed by words" — accounted for 3 prompts out of 120 where the words'
top-2 was pure NOISE and the vector found a USEFUL fact.

The pool ceiling against the best reader (96 against 56) says where the loss is: not in candidate generation but in
the order. That reframed the work from "add a retriever" to "add a ranker".

## Ranking the words' facts with the judge

The same question ("would this fact help answer the prompt") asked about ten lexical candidates rather than 233 facts
is a different job: one request, ten answers, no index to rebuild.

| words, top-1 | with the judge reordering, top-1 |
|---|---|
| 43 / 118 prompts | **70 / 118** |
| USEFUL 43 / RELATED 38 / NOISE 37 | USEFUL 70 / RELATED 25 / NOISE 23 |

At top-2: 56 to 72 prompts. The first fact changed for 77 of 118 prompts, 29 of them better and 2 worse. The whole
run cost **$0.009** and 203k input tokens at **0.7 s** per prompt (median); the whole-base variant costs $0.164 and
1.21 s for the same set and ranks worse — 12 useful prompts against 70.

## What was measured wrong along the way

- **The first vector attempt embedded `content` alone**, while the lexical index is built from `content + keywords`
  and 222 of 233 facts carry keywords. Rebuilt with the same text on both sides, the vector numbers moved from 87 to
  87 prompts with a fact and stayed at 13 useful — the hypothesis was tested and rejected, not assumed.
- **A raw cosine threshold does not separate** "needs memory" from "does not": at 0.55 it kept 20 of the 35 misses
  and 33 of 37 prompts that needed nothing, because `bge-m3` scores any technical text at 0.5-0.6 against any
  technical fact.
- **`sqrt(2 ln n)` overestimates the chance maximum** on a small homogeneous base: cosine similarities are clustered,
  not isotropic, so a margin above 1.5 silences the pass entirely while 0.75 floods it.

## Mistakes in the method

- One judge, no inter-judge agreement check on this set. The earlier set measured 87% exact agreement and kappa 0.78
  between two judges; the borderline NOISE/RELATED verdicts are the ones that move.
- The reranker is judged by how well it predicts *another model's* usefulness verdict, because both were asked the
  same question. That measures cross-model agreement, not ground truth.
- The pool is the union of what three readers found: prompts where all three miss a fact in the base look like
  "nothing helps" even when the base holds the answer.
- The judge's generosity differs from the earlier eval (13 useful prompts there, 44 here for the same reader), so the
  two sets must never be compared with each other — only readers within one set.
- Two prompts had fewer than two lexical candidates and were skipped by the reranker; one pool pair lost its label to
  a failed request and was re-run.
- Pool vectors were `bge-m3` from a local Ollama; a hosted model may rank differently.

## Reproducing

The key and the prompt set come from outside the repository; the base is not committed. Everything else is in
[scripts/gold_set.py](scripts/gold_set.py):

```bash
python3 -I docs/research/scripts/gold_set.py pool   --facts FACTS.db --prompts eval.json --jev jev.jsonl --out pool.json
python3 -I docs/research/scripts/gold_set.py label  --pool pool.json --facts FACTS.db --out labels.jsonl
python3 -I docs/research/scripts/gold_set.py rerank --pool pool.json --facts FACTS.db --out rerank.jsonl
python3 -I docs/research/scripts/gold_set.py report --pool pool.json --labels labels.jsonl --rerank rerank.jsonl
```

`jev.jsonl` is the whole-base judgement of every prompt, written by [jev_whole_store.py](scripts/jev_whole_store.py)
with `--recall`; the pool stage needs a fact base with vectors for the `bge-m3` model (`memcue embed`).

Costs of the runs above: pool 2538 pairs and 3.9M input tokens for the whole-base judge ($0.164), the labels $0.2,
the rerank $0.009. Total under $0.4.
