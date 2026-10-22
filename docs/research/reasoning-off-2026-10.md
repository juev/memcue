# Reasoning off per call, October 2026

Measurements of 2026-10-22 for #259 (stage 4 of #232). The document gives aggregate numbers only: the bases and the
sessions hold host names, paths and private repositories.

## Question

DeepSeek reasons by default, and `thinking: {"type": "disabled"}` (`Endpoint.Quick`) turns that off. Before this
measurement only the expansion of facts ran without reasoning. Which other calls of the main model keep their quality
without it?

## Method

Each call was run with reasoning on and off, twice each, on `deepseek-flash` through `api.deepseek.com`; the
reference is the same call through `claude -p` (`claude-opus-5-5`). The binaries with reasoning off were experiment
builds (not committed) that set `Quick` on one call.

| Call | Sample | Compared |
|---|---|---|
| vet review | the 41 candidates of #240 on the base as it stood at 2026-10-20T23:52Z, 235 approved facts | verdict per candidate against the `claude -p` verdicts (11 drop, 29 keep, 1 split) |
| split and dream | `memcue dream -all -dry-run` over the same base | the facts split, and the actions (`retire`, `correct`) against the `claude -p` plan: F1 of the sets |
| extraction | `memcue extract -dry-run` over 30 recent Claude Code sessions of the author's Mac, on one copy of its base | each run's facts against the `claude -p` facts of the session, judged by `claude -p` ([extract_judge.py](scripts/extract_judge.py)) |

## Results

### vet review: reasoning off

| Reasoning | Run | Agree / 41 | Drops caught / 11 | Keeps dropped / 29 | Output tokens | Time |
|---|---|---|---|---|---|---|
| on | 1 | 35 | 11 | 4 | 11,435 | 53 s |
| on | 2 | 37 | 11 | 4 | 10,235 | 46 s |
| off | 1 | 37 | 10 | 2 | 2,617 | 13 s |
| off | 2 | 37 | 10 | 1 | 2,587 | 12 s |

The disagreements are spread over both modes; none of them repeats in every run of one mode. Without reasoning the
review drops one duplicate fewer and fewer facts worth keeping, with a quarter of the output.

### split and dream: reasoning stays on

| Run | Split F1 | Actions F1 | Groups | Output tokens | Time |
|---|---|---|---|---|---|
| `claude -p` reference | — (89 facts split) | — (38 actions) | 18 | | 228 s |
| reasoning on, 1 | 0.80 | 0.65 | 18 | 255,326 | 643 s |
| reasoning on, 2 | 0.78 | 0.80 | 16 | 271,722 | 671 s |
| both off, 1 | 0.67 | 0.05 | 3 | 25,320 | 27 s |
| both off, 2 | 0.68 | 0.26 | 9 | 28,934 | 34 s |
| dream off, split on, 1 | 0.77 | 0.38 | 17 | 131,396 | 103 s |
| dream off, split on, 2 | 0.82 | 0.31 | 12 | 107,234 | 105 s |

Split without reasoning splits more facts (127–142 against 89) and agrees less (0.67–0.68 against 0.78–0.80). Dream
without reasoning, with split unchanged, keeps half of the agreement on actions (0.31–0.38 against 0.65–0.80). Both
losses are beyond the spread between the runs with reasoning.

### extraction: reasoning stays on

135 reference facts over 30 sessions (most sessions are tool output and give 0–6 facts).

| Reasoning | Run | Reference covered | Facts | Useful, not in reference | Junk | Output tokens |
|---|---|---|---|---|---|---|
| on | 1 | 88 | 134 | 35 | 1 | 264,716 |
| on | 2 | 87 | 158 | 45 | 4 | 274,372 |
| off | 1 | 77 | 154 | 49 | 17 | 50,530 |
| off | 2 | 84 | 153 | 52 | 12 | 52,607 |

Without reasoning extraction covers 4–11 fewer reference facts and writes 3–17 times more junk.

## Decision

- vet review runs without reasoning.
- Split, dream and extraction keep it.
- Expansion already ran without it (#232).

## Mistakes in the method

- The reference is one `claude -p` run per input; its own run-to-run spread is not measured, only DeepSeek's.
- The extraction judge is `claude -p` as well, the same model as the reference, so it may favour wordings close to
  its own. It judged both modes the same way.
- Two runs per mode. The review sample is 41 candidates; a difference of one is within the spread.
- Costs: the `claude -p` references took $19.36 (extraction $14.88, dream $3.90, review labels $0.58) and the judge
  $10.42; DeepSeek costs are not reported by the endpoint (#258).
