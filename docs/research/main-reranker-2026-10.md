# A/B: the main chat model as the reranker

Status: **measured 2026-10-21; keep Jev.** Issue #66 asked whether the main chat model can replace
Jev as the second model. It cannot on this sample: the same-family main model scored 108 useful
top-1 prompts against Jev's 112, and lost 10 prompts for every 6 it rescued.

**2026-10-22:** measured again with reasoning off and the current candidates, labelled by Opus and DeepSeek. Scores
per fact still lose; a listwise question (pick at most two facts) matches Jev. See
[main-judge-2026-10.md](main-judge-2026-10.md) (#274, #279).

## Method

- Pilot pool on a copy of the 2,571-fact Claude Code base: 180 real prompts, 3,223 fact labels
  (`USEFUL` / `RELATED` / `NOISE`).
- The same 10 lexical candidates per prompt that Jev reranked (lexical rank from `pool.json`).
- Main model: **`deepseek-flash`**, the model that also produced the usefulness labels. This
  biases the A/B in favour of the main model; if the same-family model still loses, dropping Jev
  is not supported.
- One chat request per prompt, temperature 0, JSON scores 0..1, eight workers. 177 prompts had at
  least two lexical candidates; 176 returned valid JSON; one prompt failed JSON parsing.
- Jev scores from `results/rerank.jsonl`; labels from `results/labels.jsonl`; lexical order from
  `results/pool.json`.
- Script: [`main_rerank.py`](scripts/main_rerank.py). It takes paths outside the repository; the
  prompt text and the fact base are not committed.

## Results

176 prompts with all three rankings:

| reader | useful top-1 | useful top-2 |
|---|---:|---:|
| lexical (BM25 order) | 74 | 93 |
| Jev over lexical | **112** | **118** |
| main chat model over lexical | 108 | 117 |

Top-1 agreement and head-to-head:

| comparison | result |
|---|---|
| main vs Jev | 6 rescues, 10 losses; net **−4** useful top-1 |
| main vs lexical | 34 rescues, 0 losses |
| Jev vs lexical | 39 rescues, 1 loss |
| same top-1 fact | 94 of 176 prompts |

Cost and latency:

| reader | input tokens | output tokens | median | p95 |
|---|---:|---:|---:|---:|
| Jev (lexical, 177 prompts) | 396k* | — | 0.79 s | 1.02 s |
| main chat (176 prompts) | 229,589 | 138,939 | 4.28 s | 8.16 s |

\* the Jev figure is the pilot's combined lexical total; the rerank JSONL does not carry usage.

The endpoint did not report a cost; the token count above and the provider's contract are what the
cost follows from. Per prompt the main model used about 1,300 input + 790 output tokens and was
about five times slower than Jev.

## Decision

Keep the three-model layout from [models.md](../models.md). A same-family main model is 4 useful
top-1 prompts behind Jev and much slower, despite having produced the labels. The one-model
alternative is not supported by this sample; a larger, independent-label A/B would be needed to
reopen it.

## Limits

- One model (`deepseek-flash`) and one prompt pool; the labels are that model's opinion.
- 176 of 180 prompts; one JSON failure and three prompts with fewer than two candidates.
- No truth audit of the main-model selections; the 60-fact truth sample was not reused here.
