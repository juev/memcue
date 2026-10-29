# Results of the #566 pitfall measurement

Data only; the analysis is in [the report](../../../pitfall-capture-566-2026-10.md). Collected 2026-10-29 on the
docker host with the #531 stand ([`../../confirm531/stand/`](../../confirm531/stand/)), its root set to a new
directory (`STAND531_ROOT=~/stand566`).

- `records/<arm>__<scenario>__r<n>.json` — 60 compact records from `../../confirm531/extract.py`: 5 scenarios × 4
  arms × 3 repeats. `cc-base` and `hm-base` are step 1 (the shipped texts), `cc-cand` and `hm-cand` step 2 (the
  shipped texts and the line in [`../candidate/`](../candidate/)). `meta` holds the arm, exit, time, cost, tokens and
  the image; `record` is what the graders saw.
- `grades/A`, `grades/B` — two independent grading passes over every record (`../../confirm531/grade.py`, Opus 5.5,
  no tools, the arm hidden). They agreed on every field of all 60 records, so no third pass ran.
  `../../confirm531/summary.py records grades ../fixtures/labels.json` combines them.

## How the runs were made

- Product code of every arm: `main` at `e479d45`, memcue `stand531-e479d45`, Claude Code 2.1.312, Hermes v0.22.5. The
  `base` variant is the shipped plugin, which since #567 carries the research-shelf text measured in #531.
- Step 1 images (`confirm531/stand/build.sh REPO HOST`): Claude Code `13f163056f1d`, Hermes `822771ecd9ce`. Grid:
  `run.py grid --arms cc-base,hm-base --repeats 3 --jobs 4 --budget 10`, 11:39–11:43 MSK, after one probe run
  (`cc-base P01 r1`, 11:37) that the grid kept.
- Step 2 images (`build.sh REPO HOST ../candidate`, the same `e479d45` checkout): Claude Code `233285c337a5`, Hermes
  `9f0496d0294d`. Their base plugins and memcue binary are byte for byte those of step 1 (the two build contexts on
the host compared with `diff -r` and `cmp`). Grid: `--arms cc-cand,hm-cand`, otherwise
  the same, 12:01–12:05 MSK. The step 1 records were extracted again with the step 2 ones and came out identical, so
  their grades stand.
- Cost at API prices: runs $2.95 (Claude Code $2.88 on the subscription token, Hermes $0.07 on DeepSeek
  `deepseek-flash`), grading $2.55.
- Checks before publishing (`../../confirm531/stand/scan.py runs records grades`, after each step): no secret value,
  home path, user name or host name.
- The raw run directories (26 MB) stay on the docker host under `~/stand566/runs`.
