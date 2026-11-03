# A response preference is a rule: the line measured, November 2026 (#613)

Status: **measured 2026-11-03; the line ships in the Claude Code SessionStart note.** #601 found that natural feedback
about how the agent should answer changed the current reply but was often not stored for a fresh session, and that
some stored preferences got a repository's scope. #613 asked for a small capture line, measured against the shipped
note on the same frozen scenarios, with a project-limited and a temporary control.

The line, last bullet of the note ([candidate](scripts/preference613/candidate/cc-note.md)):

> When you agree to change how you answer the user, the agreement is a standing rule of the user's: store it at once
> with `-kind rule -source user`, do not only acknowledge it. It holds everywhere (`-scope global`) unless the user
> limits it to this project (`-scope repo`); a format asked for one task only, or a passing mood, is not a rule to
> store.

In short:

- **Stored: 20 of 20 preferences with the line, 14 of 20 without.** All 20 global with the line; 12 of the 14 without.
- **Followed in a fresh session in another repository: 20 of 20 with the line, 12 of 20 without.**
- **The losses without the line are two scenarios**: "the bottom line first" (stored 3 of 5, followed 2) and "think in
  Celsius" (stored 1 of 5, followed 0). The two others were stored every time either way.
- **The controls stay clean**: a preference the user limited to one repository was stored with that repository's
  scope in 5 of 5 runs of each arm and applied in another repository in none; a passing mood and a format for one
  task were stored as lasting in 0 of 10 runs of each arm. No noise facts in either arm.
- **Cost is the same**: $3.43 against $3.44 API-equivalent for the 60 runs of each arm, model time 753 s against 770 s.

## Method

- **Arms**: the memcue binary of `main` (shipped) and the same with the line as the last bullet of the session note
  (candidate), both with the Claude Code plugin of `main` and the plugin's version. Nothing else differs.
- **Scenarios**: the four durable scenarios and two controls of #601, frozen there
  ([fixtures](scripts/preference601/fixtures/)), and one new control, P1: in a repository the user says that its
  release-note bullets start with the ticket id, and that other projects do not do this; the probe asks for release
  notes in another repository ([scenarios613.json](scripts/preference613/fixtures/scenarios613.json)).
- **Runs**: Claude Code 2.1.312 on Sonnet, as in #601; a capture is two turns of one session in the scenario's
  workspace, a git repository with its own remote; a probe is a fresh session in another workspace over the base the
  capture left. Five repetitions a scenario and arm: 70 captures and 50 probes
  ([run613.py](scripts/preference613/run613.py)).
- **Isolation**: on a server, by environment rather than a container: each run has its own home, Claude config, base
  and state, no settings sources, no MCP servers, only the arm's plugin, and signs in with a token.
- **Grades**: the grading prompts of #601, two Opus passes, the arm hidden ([grade613.py](scripts/preference613/grade613.py));
  a count needs both passes. They agreed on all 60 judgements. Scope and source are read from the stored facts, not
  graded. $6.99.

## Results

| | shipped | with the line |
|---|---|---|
| preferences stored (D1–D4, 20 runs) | 14 | **20** |
| of them global | 12 | **20** |
| of them stated as the user's | 14 | 20 |
| followed in a fresh session elsewhere (20 probes) | 12 | **20** |
| P1 stored with the repository's scope (5) | 5 | 5 |
| P1 applied in another repository (5) | 0 | 0 |
| controls stored as lasting (C1, C2, 10) | 0 | 0 |
| second answer acknowledged the request | 25 of 25 | 25 of 25 |
| facts written / noise | 19 / 0 | 25 / 0 |
| API-equivalent cost, 60 runs | $3.44 | $3.43 |

By scenario, stored and followed of 5:

| | shipped | with the line |
|---|---|---|
| D1 lists, not tables | 5 / 5 | 5 / 5 |
| D2 no emoji | 5 / 5 | 5 / 5 |
| D3 bottom line first | 3 / 2 | 5 / 5 |
| D4 Celsius | 1 / 0 | 5 / 5 |

## Delivery

The line is in the Claude Code SessionStart note, the whole note in one block and the first part of the split start;
a test pins it to the candidate file and checks both. The Hermes block and the DSH note do not carry it: this run
measured Claude Code only, and does not show an effect in another integration.

## Limits

- Five invented scenarios with preferences and three controls, five repetitions; one model (Sonnet), one Claude Code
  version.
- One grader model in two passes; the "followed" judgements are a model's.
- The probe is one fresh session soon after the capture; a preference stored as a rule also reaches every later card,
  which this run did not measure.
