# A hint for a thin answer: what the model does when the floor hides facts, October 2026

Status: **measured 2026-10-25 on a live installation**, shipped as a change to the Hermes plugin. The 0.14.0 note
([pull-hermes-014](pull-hermes-014-2026-10.md)) left an open question: on user prompts the score floor raises precision
and costs useful facts, and the answer says nothing about what it left out. This note prices the smallest fix — one
sentence on the thin answer.

## Why the question came up

- The floor trims the answer in 4 of 8 short queries and 9 of 20 user prompts. A caller who gets one fact cannot tell a
  thin base from a floor that cut the rest, and the tool schema's advice ("try other words") is the wrong lever for
  that case: other words fix a miss, not a floor.
- On this base the case is exact and common: of 28 measured queries, **9 end with a single fact while four more facts
  hold the same words below the floor**.
- The alternative — a per-call count of everything the search held back — was dropped before it was built: a field
  present on half the calls stops being read, and widening `-n` never helps above the floor.

## Setup

- One copy of the live base: **391 live facts**, 176 characters on average, schema 5.
- **28 queries**: the 20 user prompts of the 0.10.0 note, unchanged, and 8 short queries written the way the agent
  writes them. A search hands over at most five facts; 9 of the 28 are thin.
- **Two arms**, same prompts, same base:
  - **A** — the tool result as 0.14.0 hands it over: `{"facts": [...]}`.
  - **B** — the same result plus the hint the plugin now adds when a single fact is all the floor allows:
    "*N* more facts hold the words but sit below the score floor; run the same search with min_score 0 to read them
    (limit still caps the answer)."
- One model, the one this installation runs (deepseek-flash), temperature 0, at most three turns per question, a tool
  loop against the base copy; harness [thin_hint_ab.py](scripts/thin_hint_ab.py).
- Metrics: whether a later call widened the net (`min_score` below 0.6, `in all`, or a limit above 5), the number of
  calls, tokens, and whether the run reached a final answer inside the three turns.

## What the hint costs

| | |
|---|---|
| queries that got the hint | **9 of 28** (32 %) |
| facts reported below the floor | 4 on every one of them |
| extra search | p50 **8.9 ms** |
| context added | ~150 characters, on the thin answers only |

## What the hint changes

| | A: no hint | B: with hint |
|---|---|---|
| runs that reached an answer inside three turns | **3 of 9** | **7 of 9** |
| runs that widened the net | 7 of 9 | 9 of 9 |
| runs that set `min_score 0` themselves | 1 | 5 |
| calls per run, mean | 5.11 | 4.22 |
| tokens per run, mean | 5 876 | 7 734 |
| **tokens per answered question** | **17 629** | **9 944** |

The model is not blind without the hint: it retries with other words, raises the limit to eight or ten, and in three
runs set `min_score` by itself (0.4, 0.35, 0). What it does with the hint is different in kind: it goes to the floor
lever directly and finishes. Six of the nine runs without the hint spent all three turns searching and ended with no
answer; with the hint two did, both of them questions this base hardly covers ("free space on the backup NAS", "the
analysis of the new release").

## What this does not say

- One model, one base, nine questions, three turns, one pass. "Answered" means a final message inside the budget, not
  a correct answer; the answers were not graded.
- The hint does not fire when nothing holds the words at all — a search that finds no fact is a miss, and other words
  are the right lever there. That case was not measured, because that arm is unchanged by the hint.
- The DSH plugin keeps the old description and no hint; the same change belongs there but was not measured.
- Live verification needs a restart of the gateway: the provider lives in the process, so the new code applies on the
  next start, not on the next call.

## Reproduction

```sh
# a copy of the base, the 28 queries, and the key of the extraction endpoint
cp facts.db /tmp/facts.db
DEEPSEEK_API_KEY=… python3 docs/research/scripts/thin_hint_ab.py \
    --base /tmp/facts.db --prompts prompts.json --model deepseek-flash --rounds 3
```

The harness runs both arms over every prompt and reports the table above; it never writes to the live base or the live
state directory.
