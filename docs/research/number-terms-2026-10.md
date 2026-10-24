# Versions as exact terms: what a numeric anchor buys, October 2026

Status: **measured 2026-10-24; shipped behind `MEMCUE_NUMBER_TERMS=1`, off by default ([#328](https://github.com/juev/memcue/issues/328)).** The lexer drops bare
numbers, versions, ports and addresses on purpose: matching `Go 1.27` against notes about other versions is a
false-positive source. The cost had not been measured. A prompt that names only a version recalls nothing at all,
and a prompt that names a version next to a topic loses the one term that told the versions apart. Keeping the
number as an exact term — `1.27` becomes `nm1x27`, which never matches `1.20` — turns 0 of 11 anchor prompts into
10 of 11 and changes nothing measurable on ordinary questions.

## Context

`internal/text` strips numbers from both the index and the prompt (`reNumeric`: `v0.21.6`, `1.27`, `2026-10-19` are
"never a match"); only a ticket key (`PROJ-1234`, `#123`) survives as one exact term. Two consequences, both
reachable from a live session:

- a prompt that *is* a version (`0.17.4`, `v0.22.2`) has one term, so `MinTerms: 2` silences it before search runs;
- a prompt `topic + version` (`memcue 0.1.5`) loses the discriminator, and the pool is every fact about the topic.

Neither shows up as an error: the hook simply injects nothing, which reads exactly like "the base has no such fact".

## Method

One binary (the branch build), two arms, one base, same judge:

- `off` — `MEMCUE_NUMBER_TERMS` unset, index built word-only: release 0.6.0 behaviour, verified on the same prompts;
- `on` — `MEMCUE_NUMBER_TERMS=1`, index rebuilt from the same rows so only the tokens differ.

The base is the live one the agent gateway uses (schema 5, 396 active facts), copied with `MEMCUE_FACTS` and
`XDG_STATE_HOME` pointed at the copy; the judge is Jev through `MEMCUE_JUDGE_URL`, as in production. Every prompt
gets a fresh session id, because a fact is served once per session and a repeated id answers with the *next* facts.
The served text is the only thing scored — the response echoes the query, so scoring the raw JSON hands a free hit
to any prompt that contains its own answer.

Prompt sets:

- **anchor prompts**, built from the base itself: for each fact holding a version, `version` alone and `topic
  version`, where the topic is one of the tools that fact mentions (`memcue`, `hermes`, `go`, `forgejo`, …). 12
  built, 11 usable — in the twelfth the regex cut `27.0` out of `go1.27.0`, so the prompt named a version the fact
  does not contain;
- **26 ordinary questions** from an earlier comparison of the fact base against session search, all with a rare
  token expected in the served fact.

Scoring: a prompt counts as a hit when the expected version appears in the served facts, and separately when the
source fact itself is served. Tool: [number_terms.py](scripts/number_terms.py).

## Results

Anchor prompts, 11 usable, 26 runs in each arm:

| prompts | off | on | of which the source fact |
|---|---:|---:|---:|
| version only (`0.17.4`) | 0 / 11 | **10 / 11** | 7 |
| topic + version (`memcue 0.1.5`) | 0 / 11 | **9 / 11** | 6 |

The two misses are artefacts of the prompt builder, not of the change: one version is not in the base in the form
the prompt used, and one prompt's version is so common (`0.5.2`) that the judge serves a neighbouring fact carrying
it — the correct answer, not the source row.

Ordinary questions, 26:

| | off | on |
|---|---:|---:|
| prompts with the expected token | 26 / 26 | 26 / 26 |
| identical served fact sets | — | 25 / 26 |
| median latency, judge on | 344 ms | 351 ms |
| injected characters, total | 14 835 | 14 814 |

24 of 26 identical is not a measurement of "no change" on its own: the same arm run twice against itself agrees on
**23 of 26** prompts, because the judge's scores move by a few hundredths between identical runs. The single
differing prompt in the table is therefore inside the noise of the pipeline, and no prompt lost or gained a hit.

## Semantics: a bonus, never a filter

A numeric term participates in the search and in the ranking coverage, and is deliberately excluded from
`MinMatched`: otherwise `сборка стенда 1.27` would require a fact to hold all three terms, and the fact that
answers the question without repeating the version would drop out. The gate is relaxed to one term only when the
prompt carries nothing but numbers — the same exception a ticket key already has.

## Migration

The index is written at fact-write time, so flipping the flag on an existing base changes nothing until the index is
rebuilt, and nothing complains: counts still match. `ensureIndex` therefore rebuilds once when numbers are enabled,
the index holds no `nm` term at all, and the base does hold version-like content. A base indexed after the flag is
on costs one count query per open.

## What was measured wrong first

The first pass of the sister bench (fact base against session search) scored the raw JSON, which echoes the query,
and reported 92 % for the search arm where the true figure was 62 %. The same family of mistake bit this run once:
reusing the prompt session ids made the `off` arm return the *next* facts, so it looked like 5 of 26 instead of
26 of 26. Both are recorded here because both are invisible without looking at the identifiers.

## Limitations

- Anchor prompts are machine-built from one base: realism is bounded by how a version happens to appear in a fact.
- One base, Russian and English mixed; a base of code-heavy notes would put more numbers in the prompt path.
- Two arms on one binary, one pass each; judge noise is ±1–2 prompts, so differences smaller than that are not
  effects. The ordinary-question arm was repeated against itself to size that noise.
