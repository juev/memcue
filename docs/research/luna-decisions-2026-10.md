# GPT-6 Luna Decisions against Jev as the reranker, October 2026

Status: **measured 2026-10-23; Jev stays.** Issue #309 asked whether OpenAI's Decisions model, served by routerai.ru as
`openai/gpt-6-luna-decisions` on the same `/v1/decisions` endpoint as Jev, ranks recall candidates better. It does not:
it is slower, about three times more expensive per request, and serves fewer prompts a useful first fact at every
threshold. Its one advantage, less noise at 0.3–0.5, comes from probabilities that are nearly binary, so the threshold
cannot tune it.

## Method

- Candidates and labels: the gold set of #274 ([main-judge-2026-10.md](main-judge-2026-10.md)), 171 prompts with at
  least two candidates, the first 20 facts the words find, 3 377 pairs.
- Request: the one `internal/rerank` sends, one `noul` per fact, no policy, one request per prompt
  ([judge_ab.py](scripts/judge_ab.py) `jev` with `MEMCUE_JUDGE_MODEL` set to each model). Only the model name differs.
- Runs: Luna twice, Jev 1.14 once on the same day for a fair latency comparison; `jev-1` is the run of #274.
- Labels: the 598 Opus labels of #274, plus 66 pairs only these runs served, labelled blind by Opus through `claude -p`
  ([judge_ab_report.py](scripts/judge_ab_report.py) `label`, 7 calls, $0.96 of nominal API cost).

## Results

### Latency and cost

| Model | Median | p95 | Over 2 s | Input tokens, 171 requests | Price, ₽ per 1M input | Per request |
|---|---:|---:|---:|---:|---:|---:|
| Jev 1.14 | **0.46 s** | 0.71 s | 0 | 608 384 | 4.43 | **0.016 ₽** |
| GPT-6 Luna Decisions | 0.81–0.82 s | 1.15–1.24 s | 1 | 807 009 | 10 | 0.047 ₽ |

Output is free on both. The same text costs Luna a third more tokens than Jev: the tokenizers differ.

### Quality

124 of the 171 prompts have a USEFUL fact among the labelled candidates. Columns as in
[threshold-2026-10.md](threshold-2026-10.md): `top-1` and `any` count prompts, label columns count served facts.

| Run | Min | Top-1 | Any | USEFUL | RELATED | NOISE | None |
|---|---:|---:|---:|---:|---:|---:|---:|
| jev-1 | 0.30 | 94 | 104 | 158 | 96 | 67 | 5 |
| jev-1 | 0.50 | 92 | 101 | 154 | 77 | 44 | 23 |
| jev-1 | 0.70 | 84 | 91 | 139 | 46 | 12 | 58 |
| jev-3 | 0.30 | 92 | 105 | 160 | 95 | 68 | 5 |
| jev-3 | 0.50 | 92 | 103 | 157 | 76 | 39 | 24 |
| jev-3 | 0.70 | 82 | 89 | 136 | 47 | 10 | 60 |
| luna-1, luna-2 | 0.30 | 85 | 101 | 148 | 89 | 30 | 27 |
| luna-1, luna-2 | 0.50 | 83 | 96 | 139 | 81 | 28 | 33 |
| luna-1, luna-2 | 0.70 | 80 | 92 | 134 | 73 | 26 | 40 |

The two Luna runs returned identical tables.

### Score shape

Counted over the scores in `jev-3.jsonl` and `luna-1.jsonl`. Share of the 3 377 scores strictly between 0.1 and 0.9: Jev 67 %, Luna 14 %. Between 0.3 and 0.7: Jev 916 scores,
Luna 187. Luna answers almost yes or no, which is why its rows barely move with the threshold.

## What it says

- **At the default 0.5 Jev serves 9 more prompts a useful first fact** (92 against 83) and carries more noise
  (39–44 facts against 28).
- **At comparable noise Luna is still behind.** Jev at 0.7 serves 82–84 useful first facts with 10–12 noise facts; Luna
  at 0.7 serves 80 with 26.
- **The threshold does not tune Luna.** From 0.3 to 0.7 its noise falls by 4 facts; Jev's falls by 57. A deployment
  could not trade useful prompts for noise with it.
- Luna is deterministic between runs; Jev moves by 0–3 prompts. That is the only property in Luna's favour this
  measurement can see.

## Limits

- One candidate set and one labeller (Opus); the earlier set measured 87 % agreement between two labellers.
- Luna got Jev's request unchanged. A policy in `state`, or a `score` question instead of `noul`, was not tried and
  might change its calibration.
- Prices are routerai.ru list prices on 2026-10-23, not billed amounts.

## Reproducing

```bash
# cand.jsonl and labels.json are the files of main-judge-2026-10.md
MEMCUE_JUDGE_MODEL=openai/gpt-6-luna-decisions python3 -I docs/research/scripts/judge_ab.py cand.jsonl jev luna-1.jsonl
MEMCUE_JUDGE_MODEL=openai/gpt-6-luna-decisions python3 -I docs/research/scripts/judge_ab.py cand.jsonl jev luna-2.jsonl
python3 -I docs/research/scripts/judge_ab.py cand.jsonl jev jev-3.jsonl
python3 -I docs/research/scripts/judge_ab_report.py label  cand.jsonl labels.json jev-1.jsonl jev-3.jsonl luna-1.jsonl luna-2.jsonl
python3 -I docs/research/scripts/judge_ab_report.py report cand.jsonl labels.json jev-1.jsonl jev-3.jsonl luna-1.jsonl luna-2.jsonl
```
