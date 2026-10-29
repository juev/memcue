# The source of an agent's fact, October 2026 (#574)

Status: **measured 2026-10-29 on synthetic data, against the change of #574.** In the #566 measurement
([pitfall-capture-566](pitfall-capture-566-2026-10.md), "The source of an agent's fact") 10 of the 15 Claude Code
`memcue add` calls had no `-source`, and `add` stored each of those pitfalls as the user's words. #574 changes two
things at once, the owner's choice of 2026-10-29:

- `memcue add` without `-source` stores the fact as `agent`, not `user`.
- The Claude Code SessionStart note says which source to name (`-source user` for the user's words, `-source tool` for
  what a command or a file showed) and gives the pitfall line its command, `memcue add -kind pitfall -source tool …`.
  The skill drops "(the default)" from the user's source and passes `-source user` in its rule example.

In short:

- **Every fact the branch stored is `tool`**: 12 of 12 `add` calls named `-source tool`. On `main`, 5 of the 12
  stored pitfalls got `source: user`. The `agent` default did not come into play: no call on the branch left the flag
  out.
- **On `main` the agent looked the flag up**: in 3 runs it read `memcue add -h` before storing, and those stored
  `tool`. No branch run did.
- **Nothing else moved.** The expected pitfall was stored in 12 of 15 runs on both sides, all in the repository's
  scope, with no noise, no forbidden change and nothing on the shelf. All 30 answers are correct.

This is one grid over the 5 #566 scenarios with 3 repeats each, Claude Code only. Hermes and DSH pass `-source` on
every call and were not run.

## Method

The stand, the graders and the scripts are those of #531 ([`scripts/confirm531/`](scripts/confirm531/)), the
scenarios those of #566 ([`scripts/pitfall566/fixtures/`](scripts/pitfall566/fixtures/)). The results are in
[`scripts/source574/results/`](scripts/source574/results/).

- **Arms**: Claude Code 2.1.312 on Opus 5.5, the shipped plugin (the stand's `base` variant) in both.
  - `cc-base`: product code of `main` (`c9873c0`), which already carries the #566 line.
  - `cc-cand`: the branch of #574 (`6550851`, `43ef08f` after its rebase): the new default of `add` and the new note and skill text.
  Each arm has its own image, built from its own checkout, and its own stand root.
- **Grading**: two Opus passes per record (`grade.py`). They differed on one field of one record, which a third pass
  settled. `summary.py` gives the table below. The source counts come from the records themselves
  ([`sources.py`](scripts/source574/sources.py)): the `memcue add` commands in the runs' Bash calls and the source of
  each fact the run added to its base.

## Results

30 runs: 5 scenarios × 2 arms × 3 repeats.

| | cc-base | cc-cand |
|---|---|---|
| pitfall stored | 12/15 | 12/15 |
| of them in the repository's scope | 12/12 | 12/12 |
| stored as `source: tool` | 7 | 12 |
| stored as `source: user` | 5 | 0 |
| `memcue add` calls naming `-source` | 7 of 12 | 12 of 12 |
| `memcue add -h` before storing | 3 runs | 0 |
| changes: expected / extra useful / noise / forbidden | 12/0/0/0 | 12/0/0/0 |
| searched memory | 12/15 | 11/15 |
| answers: correct / partial / wrong / unresolved | 15/0/0/0 | 15/0/0/0 |
| mean seconds per run | 66 (24 without `P05 r3`) | 16 |
| cost, $ at API prices | 1.60 | 1.43 |

Per scenario, pitfalls stored of 3 and how many as `user`:

| scenario | cc-base | cc-cand |
|---|---|---|
| P01 relative paths | 0 | 0 |
| P02 CRLF data file | 3, 1 `user` | 3, 0 |
| P03 git-ignored `out/` (S06) | 3, 1 `user` | 3, 0 |
| P04 env var without a default | 3, 3 `user` | 3, 0 |
| P05 BOM of an outside export | 3, 0 | 3, 0 |

- **Where the flag went missing on `main`.** Every P04 run on `main` stored its pitfall without `-source`, and one
  run each of P02 and P03. The P02 and P03 runs that looked up `memcue add -h` named `-source tool`.
- **Most of the time difference is not the change.** `cc-base P05 r3` took 664 s: its stream shows two API retries
  and a five-minute gap before the last turn. Without it, `cc-base` averages 24 s a run against 16.
- **P01 stores nothing on either side**, as in #566.

## Limits

- 5 invented scenarios, 3 repeats each; repeats of a scenario correlate. Every expected fact is a pitfall, so the
  measurement says nothing about a rule or a decision stored by the agent, where `-source user` is the right source
  and a missing flag now gives `agent`, which keeps the fact off the session card.
- Both parts of the change ran together, so the grid does not say how much the text did and how much the default
  would have done alone. On this grid the default did nothing, since no branch call left the flag out.
- Grading is by models; no person checked a label. The source counts are read from the records, not graded.
- Claude Code on Opus 5.5 only.

## Conclusion

The acceptance of #574 holds on the scenarios where the gap showed: every finding the agent stored through
`memcue add` carries `tool`, against 5 of 12 stored as the user's words before, and nothing else in the capture or the
answers changed. The `agent` default stays as the guard for a call that omits the flag.
