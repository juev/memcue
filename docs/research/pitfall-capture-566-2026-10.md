# A pitfall found by running something: is #531's S06 typical? October 2026 (#566)

Status: **measured 2026-10-29 on synthetic data; no product code changed.** Step 1 of #566: in the #531 confirming
run ([research-shelf-confirm-531](research-shelf-confirm-531-2026-10.md), product finding 3) no run of scenario S06
stored the cause of a script failure it had found by running the script. This run asks whether S06 is typical.

In short:

- **It is typical.** Of 18 runs that hit a failure, 6 stored its cause. Five of the six are one scenario (P05, a
  supplier export with a UTF-8 BOM). In the S06 copy (P03) none of 6 runs stored anything, the same as in #531.
- **What gets stored is a cause outside the repository's code.** A recurring outside input (P05, the BOM of every
  supplier export) was stored in 5 of 6 runs. A data file of the repository (P02, CRLF line endings) in 1 of 6. A
  property of the repository's own layout (P03, a git-ignored output directory) in 0 of 6.
- **Where the cause is visible in the code, no run hit the failure.** In P01 (a script with paths relative to its
  directory) and P04 (an environment variable without a default) all 12 runs read the code first and ran the task the
  right way at once. None stored anything.
- **Everything stored is right.** All 6 facts are `pitfall` facts in the repository's scope, never on the shelf. Each
  names the symptom, the cause and the fix. The graders found no noise and no forbidden change.

This is one grid over 5 invented scenarios with 3 repeats each, on the shipped texts only. Repeats of a scenario are
not independent, so the per-scenario split is a direction, not a rate.

## Method

The stand, the graders and the scripts are those of #531 ([`scripts/confirm531/`](scripts/confirm531/)), with a new
root on the docker host. The new fixtures and results are in [`scripts/pitfall566/`](scripts/pitfall566/).

- **Arms**: `cc-base` (Claude Code 2.1.312, Opus 5.5) and `hm-base` (Hermes v0.22.5, DeepSeek `deepseek-flash`), each
  with the plugin shipped at `e479d45`. Since #567 that plugin carries the #531 candidate text, so these arms equal
  #531's candidate arms in their texts.
- **Scenarios** ([`fixtures/`](scripts/pitfall566/fixtures/), layout as in
  [`confirm531/SCHEMA.md`](scripts/confirm531/SCHEMA.md)): P03 is #531's S06 unchanged. P01, P02, P04 and P05 are new.
  They were written by an agent that did not read the memory instructions, the plugins or the reports. In each, the
  user asks for an ordinary task and forbids editing the script, so the cause stays and a fix is a step outside the
  code. The README describes normal use only. The agent checked each scenario in a `node:22-bookworm-slim`
  container: the first natural attempt fails, and the fix gives the expected answer.

  | id | the task | the cause | the fix |
  |---|---|---|---|
  | P01 | build a static page with `node site/build.js` | the script opens `partials/`, `data/` and `public/` relative to the working directory | run it from `site/` |
  | P02 | the shipping total from `scripts/cost.sh` | `data/zone-rates.tsv` has CRLF line endings, so the last column keeps a `\r` | a CR-stripped copy as the second argument |
  | P03 | the monthly total from `scripts/monthly.sh` (S06) | `out/` is git-ignored and the script does not create it | `mkdir -p out` |
  | P04 | the import duty from `duty.py` | the tariff path comes from `DUTYBOOK`, which has no default | `DUTYBOOK=schedules/hs-2026.csv` |
  | P05 | the reorder check on a supplier export | the export starts with a UTF-8 BOM | `--encoding utf-8-sig` |

- **Grading**: two Opus passes per record (`grade.py`), which agreed on every field of all 30 records.
  `summary.py` gives the table below. Whether a run hit the failure is not a graded field. It was read from the raw
  runs on the host: the error text in the tool output (the message of `sh` differs between the images, so P03 was
  checked by hand).

## Results

30 runs: 5 scenarios × 2 arms × 3 repeats.

| | cc-base | hm-base |
|---|---|---|
| runs that hit the failure | 9/15 | 9/15 |
| pitfall stored | 3/15 | 3/15 |
| of them in the repository's scope | 3/3 | 3/3 |
| changes: expected / extra useful / noise / forbidden | 3/0/0/0 | 3/0/0/0 |
| searched memory | 7/15 | 5/15 |
| answers: correct / partial / wrong / unresolved | 14/0/0/1 | 11/3/0/1 |
| mean seconds per run | 18 | 32 |
| cost, $ at API prices | 1.39 | 0.03 |

Per scenario, runs that hit the failure and pitfalls stored, of 3:

| scenario | cc-base hit / stored | hm-base hit / stored |
|---|---|---|
| P01 relative paths | 0 / 0 | 0 / 0 |
| P02 CRLF data file | 3 / 1 | 3 / 0 |
| P03 git-ignored `out/` (S06) | 3 / 0 | 3 / 0 |
| P04 env var without a default | 0 / 0 | 0 / 0 |
| P05 BOM of an outside export | 3 / 2 | 3 / 3 |

- **Answers**: 28 of 30 runs reported the expected value. The three partial Hermes answers are P01 (two runs) and
  P04 (one), where the graders wanted the cause named in the answer. The two unresolved answers are P05 runs
  (`cc-base r1`, `hm-base r2`) that stored the pitfall. Their final message is only the note that the fix was saved;
  the SKUs appear in the tool output, not in what the user was told.
- **16 of the 18 runs that hit a failure named its cause and fix in the answer.** The other two are the P05 runs
  above. The agents knew the cause; they did not see it as something a later session needs.
- **One stored fact has the wrong source.** `cc-base P05 r1` stored its finding with `-source user`, which the
  installation-state check of `add` does not see. The fact itself is right. One run; the
  source-labelling text is not part of this issue.

## What the split suggests

Hypothesis, not checked: the agents store a cause when it comes from outside and will come again. The P05 README says
the export "goes into `exports/` unchanged", and the prompt says "the export is exactly what the supplier portal
produced". A cause in the repository's own files reads as "what the repository shows", which the capture texts tell
the agent not to store (#547). Under that reading P03's cause is in `.gitignore` and the script, and P02's in a data
file the repository holds. A later session in a fresh checkout still hits both, so they are the pitfalls the capture
text asks for.

P01 and P04 measure something else. An agent that reads the code first never hits the failure, and has nothing to
record. Whether a cause that a careful reading avoids is worth storing is a question of its own. This run does not
answer it.

## Limits

- 5 invented scenarios, 3 repeats each; repeats of a scenario correlate. The split by cause type rests on one
  scenario per type.
- Only the shipped texts were measured; no instruction change was tried yet (step 2 of #566).
- Grading is by models; no person checked a label. Whether a run hit the failure was read from the raw runs, not
  graded.
- Hermes ran on `deepseek-flash`, not on the live Hermes's main model; Claude Code on Opus 5.5 only.

## Conclusion

S06 is typical. Of the runs that hit a failure, two thirds left its cause unstored. Without the one scenario with a
recurring outside input, 1 of 12 stored it. Step 2 of #566 applies: try an instruction change and measure it on these
five scenarios. The text should name a failure in a fresh checkout of this repository as a pitfall to store, even when
the cause is in the repository's own files.
