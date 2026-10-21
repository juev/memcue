# Ordering the words' facts with a judge

The words find the facts; they do not order them well. Measured on a gold set built for the purpose, FTS5 puts a
USEFUL fact first for 44 of 120 prompts, and a semantic retriever does worse, not better: bge-m3 21, Jev over the
whole base 12. Yet a useful fact was among the candidates for 95 prompts — so the loss was in the ranking, not in
finding.

This pass asks a typed decisions model (Jev) one yes/no question per candidate: *would this fact help answer the
prompt*. It changes the order and nothing else: the candidates are still the words', the answer is still capped by
`MaxFacts`, and a fact below `MEMCUE_RERANK_MIN` is not given at all.

```sh
export MEMCUE_RERANK_URL=https://openrouter.ai/api/alpha/decisions
export MEMCUE_RERANK_MODEL=typesafe/jev-1.14
export MEMCUE_RERANK_KEY_ENV=OPENROUTER_API_KEY     # or MEMCUE_RERANK_KEY with the key itself
```

| Variable | Meaning |
|---|---|
| `MEMCUE_RERANK_URL` | the decisions endpoint; unset keeps the pass off |
| `MEMCUE_RERANK_MODEL` | default `typesafe/jev-1.14` |
| `MEMCUE_RERANK_KEY_ENV` / `_KEY` | where the key is; the pass stays off if neither is set |
| `MEMCUE_RERANK_MIN` | probability below which a fact is not given (default 0.5) |
| `MEMCUE_RERANK_CANDIDATES` | facts the words hand over (default 10) |
| `MEMCUE_RERANK_TIMEOUT` | one request (default 2 s) |

## What it measured

Gold set: the 120 real prompts used for `docs/evaluation.md`, candidates from three retrievers (lexical top-10,
bge-m3 top-10, Jev-top-10-over-the-base), 2538 pairs, shuffled, labelled blind by a judge that never saw which
retriever proposed a fact.

| reader, top-1 | prompts with a USEFUL fact | USEFUL / RELATED / NOISE |
|---|---|---|
| words (FTS5, BM25 order) | 43 / 118 | 43 / 38 / 37 |
| words + this pass | **70 / 118** | 70 / 25 / 23 |
| bge-m3 over the base | 21 / 120 | 21 / 37 / 62 |
| Jev over the whole base | 12 / 120 | 12 / 34 / 56 |

At top-2 the pass takes 56 to 72 prompts. See [the gold-set study](research/gold-set-2026-10.md) for the protocol and what was measured wrong along the way. It changes the first fact for 77 of 118 prompts: 29 better, 2 worse.

Cost and time: 203k input tokens and **$0.009** for all 120 prompts, **0.7 s** at the median (2 s timeout). The
whole-base variant costs $0.164 and 1.2 s for the same set and ranks worse.

## Failure and cost behaviour

Any error, timeout or missing setting leaves the ordinary lexical answer in place, unchanged — the same rule as the
meaning pass. After a failure the endpoint is left alone for 60 s, doubling to 15 minutes, and the pause is kept in
`$XDG_STATE_HOME/memcue/rerank.pause` because the hook is a fresh process on every prompt.

The pass runs on every prompt while it is configured, so its 0.7 s is paid per turn. On a laptop without a key it is
off; on the server it is a deliberate trade of latency for precision, not a free improvement.

## On the older labelled set

The eval set of `docs/evaluation.md` labels only the pairs the lexical run produced, so it cannot show the pass's
gain — but it does show what the pass stops doing. Same 120 prompts, same base:

| run | prompts with a fact | facts | NOISE | into prompts needing no memory |
|---|---|---|---|---|
| words alone | 73 | 127 | 43 | 45 |
| words + this pass | 75 | 127 | **1** | **24** |

94 of the 127 facts the pass served were not in that label set at all, which is the expected consequence of a
different candidate list; the labelled part of its output is 13 USEFUL, 19 RELATED and one NOISE. That run is a
consistency check, not a measurement of the gain — the gain is the gold set above.

## Known limits

- The gold labels were produced by DeepSeek asking the same question the pass asks Jev; the pass is therefore measured
  on how well it predicts another model's usefulness verdict, not on ground truth.
- Single judge, no inter-judge agreement check on this set.
- One request carries ten facts; a much longer candidate list costs more tokens per prompt and has not been measured.
