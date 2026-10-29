# Results of the #574 source measurement

Data only; the analysis is in [the report](../../../source-default-574-2026-10.md). Collected 2026-10-29 on the docker
host with the #531 stand ([`../../confirm531/stand/`](../../confirm531/stand/)) and the #566 scenarios
([`../../pitfall566/fixtures/`](../../pitfall566/fixtures/)).

- `records/<arm>__<scenario>__r<n>.json` — 30 compact records from `../../confirm531/extract.py`: 5 scenarios × 2
  arms × 3 repeats. Both arms ran the stand's `cc-base` variant, the shipped Claude Code plugin, each from its own
  checkout and image; `cc-cand` is the name given to the branch's records after extraction (in `meta.arm` and the file
  name), so the graders' tools tell them apart. `meta` holds the exit, time, cost, tokens and the image; `record` is
  what the graders saw.
- `grades/A`, `grades/B` — two independent grading passes over every record (`../../confirm531/grade.py`, Opus 5.5,
  no tools, the arm hidden). They differed on one field of one record (`named_shelf` of `cc-base__P02__r2`), which a
  third pass, `grades/C`, settled. `../../confirm531/summary.py records grades ../../pitfall566/fixtures/labels.json`
  combines them.
- `../sources.py records` counts the `memcue add` calls, whether they name `-source`, and the source each stored fact
  got.

## How the runs were made

- `cc-base`: `main` at `5933988` (product code that of `c9873c0`, the branch's base; the commit between them changes
  one research document), memcue `stand531-5933988`, image `9dd610e1c5f6`. Grid in `~/stand574-base`:
  `run.py grid --arms cc-base --repeats 3 --jobs 4 --budget 10`, finished 13:32 MSK.
- `cc-cand`: branch `source-default-574` at `6550851`, memcue `stand531-6550851`, image `8f67b0dd1904`. The same grid
  in `~/stand574-cand`, finished 13:34 MSK.
- Both on Claude Code 2.1.312 and Opus 5.5. The fixtures are the copy in `~/stand566`, which matches the repository's
  apart from macOS `._*` metadata files, as in #566.
- `cc-base P05 r3` took 664 s: its stream shows two API retries and a five-minute gap before the last turn.
- Cost at API prices: runs $3.03 (`cc-base` $1.60, `cc-cand` $1.43), grading $1.44.
- Checks before publishing (`../../confirm531/stand/scan.py`, with this run's secrets, over both run directories, the
  records and the grades): no secret value, home path, user name or host name.
- The raw run directories stay on the docker host under `~/stand574-base/runs` and `~/stand574-cand/runs`.
