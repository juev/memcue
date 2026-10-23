# The rerank threshold between 0.5 and 0.7, October 2026

Status: **measured 2026-10-23; the default stays 0.5.** Issue #278 asked whether `MEMCUE_JUDGE_RERANK_MIN` should move off
0.5, the value picked on 120 prompts labelled by the main model's family. The answer on a current base is that 0.5 and
0.55 are indistinguishable, and everything above 0.55 costs prompts while buying one or two noise facts: the noise this
knob can remove is no longer there.

## Method

- Base: a copy of the Hermes base the gateway uses, 294 active facts, schema 5.
- Prompts: 100 real user messages from the gateway session log (`cwd=/opt/data`, cron and subagent turns excluded), 96 of
  them with at least two candidates — recall skips the judge below that.
- Candidates: the first twenty facts the words find in BM25 order, exactly what `RecallJudge` sends
  (`go run -tags research ./docs/research/scripts/judgecand`).
- Scores: Jev 1.14 (`typesafe/jev-1.14`) twice on the same candidate lists (`judge_ab.py`, 96/96 prompts per run, median
  0.26 s and 0.34 s at p95) so that run-to-run spread shows up instead of hiding.
- Labels: Opus through `claude -p` on the 192 pairs any threshold of the sweep serves, blind to which threshold served them
  (`judge_ab_report.py label`, 12 calls, 192/192 labelled, $1.55 of nominal API cost).
- Sweep: 0.30, 0.50, 0.55, 0.60, 0.65, 0.70. `THRESHOLDS` now overrides the old fixed triple, and the table prints the
  threshold with two decimals so 0.55 and 0.65 are not rounded into their neighbours.

## Results

96 prompts. `top-1` counts prompts whose first served fact is USEFUL, `any` prompts with a USEFUL fact among the at most
two served, `none` prompts that got nothing at all, and the three label columns count facts, not prompts.

| run | min | top-1 | any | USEFUL | RELATED | NOISE | none |
|---|---:|---:|---:|---:|---:|---:|---:|
| jev-1 | 0.30 | 66 | 67 | 106 | 48 | 14 | 8 |
| jev-1 | 0.50 | 62 | 63 | 101 | 29 | 8 | 18 |
| jev-1 | 0.55 | 62 | 63 | 101 | 24 | 7 | 21 |
| jev-1 | 0.60 | 61 | 62 | 100 | 23 | 6 | 23 |
| jev-1 | 0.65 | 61 | 62 | 99 | 22 | 4 | 23 |
| jev-1 | 0.70 | 60 | 61 | 96 | 20 | 4 | 24 |
| jev-2 | 0.30 | 68 | 70 | 108 | 45 | 17 | 6 |
| jev-2 | 0.50 | 63 | 65 | 103 | 30 | 9 | 16 |
| jev-2 | 0.55 | 63 | 65 | 102 | 23 | 7 | 21 |
| jev-2 | 0.60 | 62 | 64 | 100 | 22 | 7 | 23 |
| jev-2 | 0.65 | 62 | 64 | 100 | 22 | 5 | 23 |
| jev-2 | 0.70 | 61 | 63 | 95 | 19 | 3 | 25 |

For reference, 70 of the 96 prompts have a USEFUL fact somewhere among the labelled candidates: the ceiling is reached
long before the threshold is.

## What it says

- **0.50 → 0.55 changes nothing useful.** Both runs keep the same `any` count (63 and 65) and lose one noise fact; the
  three prompts that stop getting anything held a RELATED fact, not a useful one.
- **0.55 → 0.60 starts to cost.** One prompt per run loses its only useful fact, and two more go empty. Noise falls by
  one fact. That ratio stays bad all the way to 0.70: four more empty prompts and three fewer useful facts for two
  fewer noise facts.
- **The headroom the issue expected is gone.** #274 measured 40–44 noise facts at 0.5 on a 171-prompt gold set over an
  older, much smaller base; here 0.5 leaves 8–9 noise facts out of 137–141 delivered. The vet-and-dream hygiene on the
  base removed most of what the threshold used to cut, so raising it now cuts into useful facts more than into noise.

## Decision

The default stays at **0.5**, and the reason is in `docs/rerank.md`. `0.55` is defensible in principle (same useful
prompts, one less noise fact per run) but it is inside the run-to-run spread: the two Jev runs differ by one to two
prompts at every threshold, so a default moved to 0.55 would be a change the measurement cannot see. A deployment that
wants the tighter cut can set `MEMCUE_JUDGE_RERANK_MIN=0.55` without losing a useful prompt on this evidence.

## Reproducing

```bash
cp ~/.memcue/facts.db /tmp/base.db
python3 - <<'EOF'   # 100 live prompts, see the note for the filter
EOF
go run -tags research ./docs/research/scripts/judgecand /tmp/base.db eval.json > candidates.jsonl
MEMCUE_JUDGE_URL=... python3 -I docs/research/scripts/judge_ab.py candidates.jsonl jev jev-1.jsonl
MEMCUE_JUDGE_URL=... python3 -I docs/research/scripts/judge_ab.py candidates.jsonl jev jev-2.jsonl
export THRESHOLDS=0.3,0.5,0.55,0.6,0.65,0.7
python3 -I docs/research/scripts/judge_ab_report.py label  candidates.jsonl labels.json jev-1.jsonl jev-2.jsonl
python3 -I docs/research/scripts/judge_ab_report.py report candidates.jsonl labels.json jev-1.jsonl jev-2.jsonl
```

`label` asks Opus through `claude -p` and skips pairs `labels.json` already holds, so a rerun after adding thresholds
labels only the new pairs.

## Limits

- One base, one prompt set, one labeller, one day. The prompts are the agent's own session, where the base describes the
  tool being asked about, so the share of self-referential prompts is higher than in ordinary use.
- Two Jev runs put the resolution of the sweep at one or two prompts.
- The labels are Opus's opinion; #274 measured how far that sits from the main model's family, and the "same topic is not
  USEFUL" rule is the part a second labeller disagrees with most.
