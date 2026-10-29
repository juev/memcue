# The research shelf: a confirming run on an isolated stand, October 2026 (#531)

Status: **measured 2026-10-28 on synthetic data; no product code changed.** A follow-up to
[research-shelf-531](research-shelf-531-2026-10.md): that run gave a direction, but it was not sandboxed, Hermes was
emulated, and one author wrote both the scenarios and the candidate text. This run fixes all three.

In short:

- **Claude Code with the candidate text stores findings and finds them again.** It stored 21 of 24 expected
  findings, all on the shelf; base stored 12 of 24, only 3 of them on the shelf. The candidate reached the shelf in
  24 of 24 runs that needed it, base in 15. Answers: 57 of 57 correct, against 52 correct, 2 partial and 3 wrong for
  base. Per scenario the candidate is never worse than base and better in four (S05, S09, S16, S17).
- **Hermes on DeepSeek gains less.** The candidate stored 13 of 24 findings (base 3, none on the shelf) and reached
  the shelf 21 times of 24 (base 15). Answers barely moved: 50/4/3 against 48/6/3. Per scenario the picture is mixed:
  four better (S08, S13, S14, S18), three worse (S06, S10, S16).
- **The candidate's cost.** For Claude Code the cost is 4% higher ($5.77 against $5.53 for 57 runs, `summary.py`),
  and a run is shorter on average (30 s against 37 s). Few extra facts: 6 useful ones beyond the expected in Claude
  Code and 7 in Hermes, no noise. The Claude Code candidate made no forbidden change; the Hermes candidate made 4 in
  three runs.
- **No arm stored a pitfall from the work.** In S06 (a script fails in a fresh clone) none of the 12 runs wrote
  anything; the candidate's line about pitfalls changed nothing.
- Found on the way: `memcue add` refuses a shelf finding about another system's config default, the Hermes CLI loads
  its memory provider only with the `memory` toolset, and two stand artifacts, fixed and described below.

This is one grid over 19 invented scenarios with 3 repeats each. Repeats of a scenario are not independent, so a
difference of a few runs is a direction, not a rate for real work.

## What changed since the first run

| | first run (#535) | this run |
|---|---|---|
| Isolation | `claude -p` with the user's home, a deny list | one container per run: no capabilities, its own home, only the run directory and the fixtures (ro) mounted |
| Hermes | emulated under Claude | real Hermes v0.22.5 from the live image, DeepSeek `deepseek-flash` |
| Scenarios | written by the candidate's author | written by a separate agent that never saw the candidate; the candidate's author did not read them before grading ended |
| Grading | models, adjudicated by the author | two independent Opus passes with no tools and the arm hidden; a third pass on disagreements |
| Web | MCP stand-in, unreserved domains | MCP stand-in, `*.example` only |

## Method

Everything needed to repeat the run is in [`scripts/confirm531/`](scripts/confirm531/).

- **Candidate text** ([`candidate/`](scripts/confirm531/candidate/)), written and committed before the scenarios
  existed (sha256 in the branch history): the first candidate with the report's five refinements and the owner's
  decisions of 2026-10-28. Claude Code names the shelf; store what the conclusion rests on and its limits; do not
  store a document of the repository (#547); compare a finding's version or date with what the workspace uses before
  relying on it; an answer says no more than the finding and marks an inference. No expiry and no new kind.
- **Scenarios** ([`fixtures/`](scripts/confirm531/fixtures/), layout in [`SCHEMA.md`](scripts/confirm531/SCHEMA.md)):
  19, 7 capture and 12 recall, two of them chains (S13 reads the base that S02 left in the same arm and repeat, S18
  the base of S08). The shelf is needed in 8: S05, S07, S09, S11, S13, S14, S17, S18.
- **Stand** ([`stand/`](scripts/confirm531/stand/)): Docker on a home server. A Claude Code 2.1.312 image with the
  memcue plugin of the branch, and a Hermes image from the live digest. The candidate variant adds a fourth
  SessionStart hook and a skill section (Claude Code), or a block after the memcue note in the system prompt (Hermes).
  Claude Code runs Opus 5.5.
- **Grading**: [`extract.py`](scripts/confirm531/extract.py) makes a compact record of a run (prompt, base changes,
  memcue calls, pages read, answer); [`grade.py`](scripts/confirm531/grade.py) gives it to a fresh `claude -p`
  (Opus 5.5) with [`grade_prompt.md`](scripts/confirm531/grade_prompt.md) and the scenario's labels. Passes A and B
  agreed fully on 222 of 228 records, on the answer verdict on 224; pass C settled the six disagreements.
  [`summary.py`](scripts/confirm531/summary.py) recomputes every number below from the kept records.
- **Data and provenance**: [`results/README.md`](scripts/confirm531/results/README.md).

## Results

57 runs per arm (19 scenarios × 3).

| | cc-base | cc-cand | hm-base | hm-cand |
|---|---|---|---|---|
| expected findings stored | 12/24 | 21/24 | 3/24 | 13/24 |
| shelf findings stored on the shelf | 3/21 | 21/21 | 0/21 | 13/21 |
| shelf findings stored elsewhere | 9/21 | 0/21 | 3/21 | 0/21 |
| pitfall from the work (S06) stored | 0/3 | 0/3 | 0/3 | 0/3 |
| changes: expected / extra useful / noise / forbidden | 8/0/0/1 | 16/6/0/0 | 1/3/0/0 | 9/7/0/4 |
| runs with nothing to store that stored | 0/42 | 4/42 | 1/42 | 6/42 |
| searched memory | 46/57 | 49/57 | 39/57 | 40/57 |
| reached the shelf when needed | 15/24 | 24/24 | 15/24 | 21/24 |
| needed fact found | 24/27 | 27/27 | 19/27 | 24/27 |
| answers: correct / partial / wrong | 52/2/3 | 57/0/0 | 48/6/3 | 50/4/3 |
| of them chains | 6/0/0 | 6/0/0 | 2/4/0 | 5/1/0 |
| mean seconds per run | 37 | 30 | 39 | 38 |
| cost, $ at API prices | 5.53 | 5.77 | 0.13 | 0.13 |

### Capture

- Claude Code base does store findings, but in the repository's scope, not on the shelf: 9 of its 12. A search in the
  same repository finds them, so base's chains are right too (6 of 6). From another repository an ordinary search
  would not return them.
- Hermes base stores almost nothing (3 of 24), and its chains are the weakest: 4 partial answers of 6.
- "Stored with nothing to store" in the candidate arms is mostly an edit of a seeded fact: in S15 the finding was
  already on the shelf, and the agent added details to it instead of a second copy (the graders counted that as
  useful); in S14, S17 and S18 a note that the source was checked again.
- Forbidden changes: cc-base S02 r2 put a finding in the repository scope together with README text; hm-cand S04 r2
  added a new shelf fact without retiring the stale one; S09 r3 stored its own arithmetic with an invented limit; S15
  r3 made a second copy of the finding and stored the repository's settings.
- No arm stored anything from the documents inside the repository (S10), as #547 asks.

### Search and answers

- The main difference is where an ordinary search returns something while the needed fact is on the shelf. In S09
  cc-base reached the shelf in one run of three; in the other two it invented the general S3 limit of 10,000 parts
  instead of 5,000 and answered wrong. The Claude Code candidate read the shelf in all three. In Hermes both arms
  reached the shelf in two runs of three and answered wrong once.
- S17 (no cue, a paraphrase): one wrong answer each for cc-base, hm-base and hm-cand; three correct for cc-cand.
- S16 (nothing recorded): Claude Code base added a generic "about 1 MB" guess in one run, a partial answer; the
  Claude Code candidate said three times that nothing is known.
- In S19 (an ordinary host fact is needed) every arm answered correctly; the shelf is not needed there, and Hermes did
  not read it.

## Product findings

1. **`memcue add` refuses a shelf finding about another system's config default.** The installation-state check fires
   on its `config default` signal with an empty or `environment` kind, also for `scope shelf:research`. Seeding hit it
   (seed `flags`, S05); the agents did not in the runs. The one refusal in the runs is hm-base S14 r2, where the agent
   stored a pinned package version, which is exactly what the check should stop. Hypothesis, not checked: an agent
   that stores such a finding about another system on the shelf gets refused, although it has nothing to do with the
   state of its own machines.
2. **The Hermes CLI loads its memory provider only with the `memory` toolset.** With `platform_toolsets.cli` lacking
   `memory`, the system prompt has no memcue note and the memcue tools are missing, although the plugin is enabled and
   the provider is set. The live Hermes uses the `hermes-cli` preset, which includes `memory`, so it does not show
   there.
3. **Pitfalls from the work are not stored.** S06 is one scenario, but none of 12 runs wrote down the cause of a script
   failure found by running it; the candidate's line "a pitfall goes where it goes today" did not change that.

## Stand artifacts and what was done

- **The Hermes write guard.** The image sets `HERMES_WRITE_SAFE_ROOT=/opt/data`, so Hermes could not write files in
  the workspace. It hit 14 Hermes runs in S05, S09, S12 and S16; in S09 agents even stored the denial as a pitfall. All
  four scenarios were run again in both Hermes arms with the run directory as the safe root; the first records are in
  `results/superseded/`.
- **Seeding through `add`.** Because of finding 1, seeding moved to `memcue import` and the images were rebuilt at
  17:30, mid-grid; S05 r1 of every arm was run again. The agent, the texts and the plugins did not change; each record
  keeps its image id.
- **Built-in Claude Code plugins.** Every Claude Code run loaded `cc-plugin-agents-md` and
  `cc-plugin-plugin-authoring`, bundled in the 2.1.312 binary. They are the same in both arms; their effect was not
  measured.

## Isolation and privacy

- Each run is its own container with the host's uid, no capabilities, `no-new-privileges`, memory, CPU and process
  limits and its own network. Only the run directory and the fixtures (read-only) are mounted; the host's home and the
  live bases are not visible (checked with a probe container). The Hermes image has an anonymous `/opt/data` volume
  from `VOLUME`, new for every container and removed with it.
- The secrets (a `claude setup-token` token, a DeepSeek key) came in through `--env-file` and were visible to the agent
  in its environment. [`stand/scan.py`](scripts/confirm531/stand/scan.py) looked for their exact values, the home path,
  the user name and the host name in the raw runs, the records and the grades: no match.
- Everything an agent saw went to the model providers: Anthropic (Claude Code and grading) and DeepSeek (Hermes). The
  data is invented, and the records hold no host paths.

## Cost and time

The grid ran 17:25–18:01 MSK, the S05 rerun and the chains until 18:03, the Hermes rerun 18:36–18:50. At API prices:
the grid $11.58 (Claude Code $11.30 on the subscription token, Hermes $0.29 on DeepSeek), the Hermes rerun $0.06,
grading about $12.60 (pass A $8.87, B $2.32, C $0.08, grading of the superseded runs $1.33).

## Limits

- One grid, 19 scenarios with 3 repeats; repeats of a scenario correlate. The 5 extra correct answers in Claude Code
  come from four scenarios of nineteen.
- The scenarios were invented by one agent; real research sessions are longer and a real shelf is larger.
- Grading is by models; no person checked a label.
- Hermes ran on `deepseek-flash`, not on the live Hermes's main model.
- One Claude Code model (Opus 5.5); other models were not measured.

## Conclusion

For Claude Code the candidate text did what it was meant to: findings land on the shelf, the agent reaches the shelf
when it is needed, and no answer got worse. For Hermes on DeepSeek capture improves and answers do not; four forbidden
changes show the model follows the text less carefully. The next step is the owner's decision whether to move the
candidate into the product texts (`cmd/memcue/session.go`, the skill, the Hermes block) as a separate task; findings
1–3 are candidates for separate issues.
