# Results of the #566 pitfall measurement

Data only; the analysis is in [the report](../../../pitfall-capture-566-2026-10.md). Collected 2026-10-29 on the
docker host with the #531 stand ([`../../confirm531/stand/`](../../confirm531/stand/)), its root set to a new
directory (`STAND531_ROOT=~/stand566`).

- `records/<arm>__<scenario>__r<n>.json` — 30 compact records from `../../confirm531/extract.py`: 5 scenarios × 2
  arms (`cc-base`, `hm-base`) × 3 repeats. `meta` holds the arm, exit, time, cost, tokens and the image; `record` is
  what the graders saw.
- `grades/A`, `grades/B` — two independent grading passes over every record (`../../confirm531/grade.py`, Opus 5.5,
  no tools, the arm hidden). They agreed on every field of all 30 records, so no third pass ran.
  `../../confirm531/summary.py records grades ../fixtures/labels.json` combines them.

## How the runs were made

- Images built from `main` at `e479d45` (`confirm531/stand/build.sh`): Claude Code 2.1.312 `13f163056f1d`, Hermes
  v0.22.5 `822771ecd9ce`, memcue `stand531-e479d45`. The `base` variant is the shipped plugin, which since #567
  carries the research-shelf text measured in #531.
- Grid: `run.py grid --arms cc-base,hm-base --repeats 3 --jobs 4 --budget 10`, 11:39–11:43 MSK, after one probe run
  (`cc-base P01 r1`, 11:37) that the grid kept.
- Cost at API prices: $1.43 for the runs (Claude Code $1.39 on the subscription token, Hermes $0.03 on DeepSeek
  `deepseek-flash`), $1.23 for grading.
- Checks before publishing (`../../confirm531/stand/scan.py runs records grades`): no secret value, home path, user
  name or host name.
- The raw run directories (13 MB) stay on the docker host under `~/stand566/runs`.
