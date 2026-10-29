# A pitfall found by running something: S06 and one more line, October 2026 (#566)

Status: **measured 2026-10-29 on synthetic data; no product code changed.** In the #531 confirming run
([research-shelf-confirm-531](research-shelf-confirm-531-2026-10.md), product finding 3) no run of scenario S06
stored the cause of a script failure it had found by running the script. Step 1 of #566 asks whether S06 is typical.
Step 2 measures one added line of the capture text on the same scenarios.

In short:

- **S06 is typical.** On the shipped texts, of 18 runs that hit a failure, 6 stored its cause. Five of the six are one
  scenario (P05, a supplier export with a UTF-8 BOM). In the S06 copy (P03) none of 6 runs stored anything, the same
  as in #531.
- **The line works for Claude Code.** With it, Claude Code stored the expected pitfall in 12 of 15 runs, against 3 of
  15 without it. It stored the cause in all 9 runs that hit a failure. It also stored the cause in the 3 P04 runs,
  where it read the code first and never hit the failure.
- **Hermes on DeepSeek gains less**: 6 of 15 against 3 of 15. It stored the cause in 5 of the 9 runs that hit a
  failure, and in none of the 3 S06 runs.
- **Nothing got worse.** No noise, no forbidden change, no fact on the shelf, in any arm. Answers: Claude Code 15
  correct with the line, against 14 correct and 1 unresolved without it; Hermes 13 correct and 2 partial, against 11,
  3 partial and 1 unresolved. The line cost Claude Code 7% more ($1.49 against $1.39 for 15 runs) and 3 s a run.
- Found on the way: **10 of the 15 Claude Code `memcue add` calls had no `-source`**, so the fact was stored as the
  user's words (`add` defaults to `-source user`). The skill asks for `-source tool`; the session note names it only
  for the shelf.

This is one grid over 5 invented scenarios with 3 repeats each. Repeats of a scenario are not independent, so the
per-scenario split is a direction, not a rate.

## Method

The stand, the graders and the scripts are those of #531 ([`scripts/confirm531/`](scripts/confirm531/)), with a new
root on the docker host. The new fixtures, the candidate line and the results are in
[`scripts/pitfall566/`](scripts/pitfall566/).

- **Arms**: Claude Code 2.1.312 on Opus 5.5 (`cc-`) and Hermes v0.22.5 on DeepSeek `deepseek-flash` (`hm-`). All four
  run the product code of `e479d45`. Since #567 the shipped plugin carries the #531 candidate text.
  - `base`: the shipped texts.
  - `cand`: the shipped texts and one line, [`candidate/`](scripts/pitfall566/candidate/), built the way #531 built
    its candidate. Claude Code gets a fourth SessionStart hook and a paragraph at the end of the skill. Hermes gets a
    block after its memory note.
- **The line**, the owner's wording of 2026-10-29: "When a command fails and you fix or work around it, store the cause
  and the fix as a pitfall before you finish if a later session in a fresh checkout would hit it again — even when the
  cause is in the repository's own files; what the repository shows is its documents, not a failure you found by
  running it." The last clause answers the #547 rule, "a document in the repository is what the repository shows: do
  not store it". Step 1 suggested that the agents read that rule as covering every file of the repository.
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

- **Grading**: two Opus passes per record (`grade.py`), which agreed on every field of all 60 records. `summary.py`
  gives the tables below. Whether a run hit the failure is not a graded field. It was read from the raw runs on the
  host: the error text in the output of the agent's own tools, not in its memory calls.

## Results

60 runs: 5 scenarios × 4 arms × 3 repeats.

| | cc-base | cc-cand | hm-base | hm-cand |
|---|---|---|---|---|
| runs that hit the failure | 9/15 | 9/15 | 9/15 | 9/15 |
| pitfall stored | 3/15 | 12/15 | 3/15 | 6/15 |
| of them in the repository's scope | 3/3 | 12/12 | 3/3 | 6/6 |
| changes: expected / extra useful / noise / forbidden | 3/0/0/0 | 12/0/0/0 | 3/0/0/0 | 6/0/0/0 |
| searched memory | 7/15 | 13/15 | 5/15 | 6/15 |
| answers: correct / partial / wrong / unresolved | 14/0/0/1 | 15/0/0/0 | 11/3/0/1 | 13/2/0/0 |
| mean seconds per run | 18 | 21 | 32 | 34 |
| cost, $ at API prices | 1.39 | 1.49 | 0.03 | 0.03 |

Per scenario, runs that hit the failure and pitfalls stored, of 3:

| scenario | cc-base | cc-cand | hm-base | hm-cand |
|---|---|---|---|---|
| P01 relative paths | 0 / 0 | 0 / 0 | 0 / 0 | 0 / 0 |
| P02 CRLF data file | 3 / 1 | 3 / 3 | 3 / 0 | 3 / 2 |
| P03 git-ignored `out/` (S06) | 3 / 0 | 3 / 3 | 3 / 0 | 3 / 0 |
| P04 env var without a default | 0 / 0 | 0 / 3 | 0 / 0 | 0 / 1 |
| P05 BOM of an outside export | 3 / 2 | 3 / 3 | 3 / 3 | 3 / 3 |

### Step 1: the shipped texts

- **What gets stored is a cause from outside.** A recurring outside input (P05, the BOM of every supplier export) was
  stored in 5 of 6 runs. A data file of the repository (P02) in 1 of 6. A property of the repository's own layout
  (P03) in 0 of 6.
- **Where the cause is visible in the code, no run hit the failure.** In P01 and P04 all 12 runs read the code first
  and ran the task the right way at once; none stored anything.
- **The agents knew the cause.** 16 of the 18 runs that hit a failure named its cause and fix in the answer. The
  other two are P05 runs (`cc-base r1`, `hm-base r2`) whose final message is only the note that the fix was saved;
  the SKUs appear in the tool output, not in what the user was told. The graders marked them unresolved.

### Step 2: the added line

- **Claude Code stored every failure it hit.** That includes S06 (P03, 3 of 3), which no Claude Code run of #531 or
  step 1 stored. It also stored P04 in 3 of 3 runs without hitting the failure. The fact says that `duty.py` reads
  `DUTYBOOK`, that the README does not mention it, and how to run it. The graders counted these as expected: the
  label asks for that pitfall, whether or not the run hit it.
- **Hermes moved on P02 and P04 only.** P02 went from 0 to 2 of 3 and P04 from 0 to 1. P03 stayed at 0 of 3, although
  every P03 run hit the failure and named the cause in its answer.
- **Every fact is in the repository's scope, with kind `pitfall`.** Each names the symptom, the cause and the fix. No
  arm put one on the shelf, and no arm stored the command output or the task's progress, which the labels forbid.
- **Claude Code searched more with the line**: 13 of 15 runs against 7. In 11 of the 12 runs that stored a pitfall
  the search came just before the `add`, as the capture text asks ("search first").

### The source of an agent's fact

Of the 15 Claude Code `memcue add` calls in both arms, 10 had no `-source`. The fact then got `source: user`, the
default of `add` (1 of 3 in `cc-base`, 9 of 12 in `cc-cand`). The skill says "`-kind pitfall -source tool`". The
SessionStart note tells the agent to store "with the memcue skill (memcue add)" and gives `-source tool` only in the
shelf command. A fact marked as the user's words skips the installation-state check of `add` (#475). Hermes is not
affected: `memcue_add` defaults a pitfall to `agent`. This is outside #566 and goes to its own issue.

## Limits

- 5 invented scenarios, 3 repeats each; repeats of a scenario correlate. The split by cause type rests on one
  scenario per type.
- One wording of the line, on top of the shipped texts. Where the line lives (the note, the skill, both) was not
  varied.
- Grading is by models; no person checked a label. Whether a run hit the failure was read from the raw runs, not
  graded.
- Hermes ran on `deepseek-flash`, not on the live Hermes's main model; Claude Code on Opus 5.5 only.

## Conclusion

S06 is typical: on the shipped texts the agents leave the cause of a failure in the repository's own files unstored.
The added line fixes that for Claude Code: 12 of 15, every failure it hit, no noise, answers no worse, 7% more cost.
For Hermes on DeepSeek it helps in two scenarios of five and not at all on S06. The next step is the owner's
decision whether to move the line into the product texts (`cmd/memcue/session.go`, the skill, the Hermes and DSH
blocks). The missing `-source` is a separate issue.
