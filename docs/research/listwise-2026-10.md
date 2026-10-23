# The listwise reader on the product path, October 2026

Status: **measured 2026-10-23; shipped as an option, Jev stays the default.** Issue #281 proposed reranking recall with
one listwise question to the main chat model, on the strength of #279 (`main-judge-2026-10.md`): 171 gold-set prompts, as
many useful prompts as Jev with a third to two thirds less noise. On the current base that lead has disappeared — the
reader serves as many useful prompts, two more noise facts, and costs three times the latency — so `MEMCUE_RERANK=listwise`
is opt-in and Jev keeps the default.

## Method

- Base: a copy of the Hermes base the gateway uses, 294 active facts, schema 5.
- Prompts: 96 real user messages from the gateway session log with two or more candidates.
- Arms, both through the product path (`memcue recall -cwd /opt/data -explain`, one process per prompt):
  - `MEMCUE_RERANK=judge` with the Jev endpoint;
  - `MEMCUE_RERANK=listwise` with `MEMCUE_API_URL=https://api.deepseek.com`, `MEMCUE_MODEL=deepseek-flash`, which is the
    main model the gateway hands the extraction.
- Labels: Opus through `claude -p`, the SYSTEM/SCHEMA of `judge_ab_report.py`. 192 pairs already carried labels from the
  threshold sweep ([threshold-2026-10.md](threshold-2026-10.md)); the 46 pairs only these arms served were labelled in this
  run (6 calls, $0.28, 238 pairs in total, none unlabelled).

## Results

96 prompts. `top-1` counts prompts whose first served fact is USEFUL, `any` prompts with a USEFUL fact among the at most
two served, and the label columns count facts.

| arm | top-1 | any | USEFUL | RELATED | NOISE | served nothing | p50 | p90 |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| Jev 0.5 (default) | 63 | 65 | 104 | 30 | 8 | 16 | 320 ms | 374 ms |
| listwise, deepseek-flash | 63 | 66 | 106 | 35 | 10 | 16 | 995 ms | 1164 ms |

The arms disagree on 58 of the 96 prompts, so the reader is not a no-op: it picks different facts and orders them itself.
What it does not do on this base is buy quality — one more prompt with a useful fact, two more useful facts, two more
noise facts, the same first fact on the same number of prompts — while the median turn grows by 675 ms. In the live
gateway, which pays a TLS handshake per process, that ratio is worse, not better.

## Why #279 promised more

#279 measured on the gold set of 171 prompts over an older, smaller base, where Jev at 0.5 left 40–44 noise facts. Today
the same threshold leaves 8, because `vet` and `dream` removed most of what the noise used to be (the same reason the
threshold sweep of #278 found no headroom between 0.5 and 0.55). A reader that wins by cutting noise has nothing to cut
once the noise is gone; what is left is the latency of a second model call.

## Decision

- `MEMCUE_RERANK` names the reader: `judge` (default), `listwise`, `off`. Without the variable Jev runs when its endpoint
  is configured, and the listwise reader runs only when there is no Jev key and `MEMCUE_API_URL` is set — better than the
  words alone is the one thing it is guaranteed to be.
- The reader itself is in `internal/rerank/listwise.go`: the measured request (`judge_ab.py main-list`: policy as the
  system message, numbered facts, `{"facts": [n, …]}`, temperature 0, reasoning off), the model's order kept as scores
  1, 0.9, … and 0 for the rest, `MEMCUE_JUDGE_RERANK_MIN` deliberately not applied to it, its own `listwise.pause` so a
  dead main model cannot silence the Jev roles, and the lexical answer on every failure. An empty pick is a verdict: it
  serves nothing, and no fallback runs.
- Where it is worth turning on: a base where `vet` has not run yet, a host without a Jev key, or a prompt set whose noise
  the words still carry. The cost is visible in `recall.log` as `ms` per turn.

## Reproducing

```bash
# the same base copy and prompt list as threshold-2026-10.md
MEMCUE_FACTS=/tmp/base.db MEMCUE_RERANK=judge \
  MEMCUE_JUDGE_URL=… MEMCUE_JUDGE_MODEL=typesafe/jev-1.14 MEMCUE_JUDGE_KEY=… \
  memcue recall -cwd /opt/data -explain "$PROMPT"
MEMCUE_FACTS=/tmp/base.db MEMCUE_RERANK=listwise \
  MEMCUE_API_URL=https://api.deepseek.com MEMCUE_MODEL=deepseek-flash MEMCUE_API_KEY=… \
  memcue recall -cwd /opt/data -explain "$PROMPT"
```

The label stage is the `label` mode of `judge_ab_report.py` with `THRESHOLDS=0.5,0.9`, over a candidate file that holds
the pairs both arms serve.

## Limits

- One base, one main model (`deepseek-flash`), one day. #279 measured another base and a different model mix; a stronger
  main model may separate the readers again.
- 96 prompts put the resolution near one or two prompts.
- The labels are Opus's opinion, and "on topic is not USEFUL" is the part a second labeller disagrees with most.
