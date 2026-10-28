# Results of the #531 confirming run

Data only; the analysis is a separate step. Collected 2026-10-28 on the docker host with the stand in `../stand/`.

- `records/<arm>__<scenario>__r<n>.json` — 228 compact records from `../extract.py`: 19 scenarios × 4 arms
  (`cc-base`, `cc-cand`, `hm-base`, `hm-cand`) × 3 repeats. `meta` holds the arm, exit, time, cost, tokens and the
  image; `record` is what the graders saw.
- `grades/A`, `grades/B` — two independent grading passes over every record (`../grade.py`, Opus 5.5, no tools, the
  arm hidden); `grades/C` — a third pass on the records where A and B differed (6). Each grade carries the sha256 of
  the record it judged. `../summary.py records grades ../fixtures/labels.json` combines them.
- `superseded/` — the first records and grades of S05, S09, S12 and S16 in both Hermes arms (24 runs). The Hermes
  image sets `HERMES_WRITE_SAFE_ROOT=/opt/data`, which denied every file write in the workspace; 14 of these runs hit
  it. The four scenarios were run again in both Hermes arms with the workspace as the safe root; those runs are in
  `records/` and `grades/`.

## How the runs were made

- Grid: started 17:25 MSK, `run.py grid --repeats 3 --jobs 4 --budget 25`; chains (S13 from S02, S18 from S08) after
  their captures; S05 r1 of all arms run again at 18:02 after a seeding fix. Hermes rerun of S05/S09/S12/S16:
  18:36–18:50.
- Images by run (first 12 hex of the image id): Claude Code `3265db735f22` (18 runs, before 17:30) and
  `039f807d4e66` (96); Hermes `a667d8efb5e4` (18, before 17:30), `357862bef6f2` (96, of which 24 are superseded) and
  `daa9a209c2f3` (24, the rerun). The images were rebuilt mid-grid at 17:30: seeding moved from `memcue add` to
  `memcue import`, because `add` refused a seeded shelf fact as installation state; the agent, its text and the
  plugins did not change. The 18:36 rebuild changed only `HERMES_WRITE_SAFE_ROOT`.
- Cost at API prices: $11.58 for the grid (Claude Code $11.30 on the subscription token, Hermes $0.29 on DeepSeek),
  $0.06 for the Hermes rerun; grading about $0.075 per grade.
- Checks before publishing (`../stand/scan.py`): no secret value, home path, user name or host name in the raw runs,
  the records or the grades.
- The raw run directories (stream, session export, base dumps, web log; 87 MB and 8.9 MB) stay on the docker host
  under `~/stand531/runs` and `~/stand531-hfix/runs`.
