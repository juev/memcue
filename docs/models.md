# Three models: main, judge, embeddings

Status: **accepted 2026-10-21 and implemented in 0.4.0.** The old opt-in endpoint names are removed;
this document is the contract the code and the README follow.
Implementation: [#62](https://github.com/juev/memcue/issues/62).

## Decision

Keep exactly three model roles, each configured by environment variables:

1. **Main chat model** — extraction and candidate review. `MEMCUE_API_*`; when `MEMCUE_API_URL`
   is empty, extraction falls back to `claude -p`.
2. **Judge** — Jev, one model for all three typed decisions: dream triage, recall rerank and
   duplicate check. `MEMCUE_JUDGE_*`.
3. **Embeddings** — a local or hosted OpenAI-compatible embedder (bge-m3 in the pilot),
   used only to propose candidates for the judge. `MEMCUE_EMBED_*`.

There is no fourth model: no separate review endpoint, no second judge, no vector-only reader.

## Why: the pilot

2,571 active facts from a copy of the Claude Code base, 180 real prompts from 636 Claude Code
transcripts, 3,223 blind usefulness labels, 177/180 prompts reranked by Jev 1.14, and a 60-fact
truth-priority sample. Aggregate data only; private facts stay outside the repository.

| Reader | Useful top-1 | Useful top-2 |
|---|---:|---:|
| lexical only | 75 / 180 | 94 / 180 |
| Jev over lexical candidates | 112 / 177 | 119 / 177 |
| **Jev over lexical + vector candidates** | **129 / 180** | **137 / 180** |
| vector additions alone | 35 / 180 | 56 / 180 |

Paired against lexical, Jev rescues 39 prompts at top-1 and loses 1. Against Jev-over-lexical,
adding the vector candidates rescues **18** prompts and loses 2; all 18 rescued top-1 facts came
from the vector-only set. Vector alone is weak, but it is a useful candidate generator once the
judge sees its facts.

Cost and time: Jev over lexical candidates 396k input tokens, $0.0166, 0.79 s median; over
lexical + vector 669k input tokens, $0.0281, 0.87 s median. The local bge-m3 backfill of 2,539
facts took 64 s; the embedding endpoint is free and local in the pilot.

Truth sample: 60 discordant facts, 52 `TRUE_NOW`, 8 `STALE`, 0 `FALSE`, 0 `UNVERIFIABLE`.
The 17 unique vector rescues are 15 true + 2 stale; the 21 Jev rescues are 17 true + 4 stale;
controls are 19/20 true. The sample is deliberately discordant, **not random**: it cannot
estimate the base false rate, and 0 `FALSE` is not evidence that the base is clean. The random
160-fact audit and the full 200-row served audit were not completed.

## Variables

### Required

| Variable | Meaning |
|---|---|
| `MEMCUE_API_URL`, `MEMCUE_API_KEY`, `MEMCUE_MODEL` | main chat endpoint, key and model; URL empty means `claude -p` |
| `MEMCUE_JUDGE_URL`, `MEMCUE_JUDGE_KEY`, `MEMCUE_JUDGE_MODEL` | one Jev endpoint for triage, rerank and duplicate check |
| `MEMCUE_EMBED_URL`, `MEMCUE_EMBED_KEY`, `MEMCUE_EMBED_MODEL` | OpenAI-compatible embeddings endpoint; key may be empty for a local server |

### Optional tuning

| Variable | Default | Meaning |
|---|---:|---|
| `MEMCUE_JUDGE_RERANK_MIN` | 0.5 | Jev probability below which a candidate is not given |
| `MEMCUE_JUDGE_RERANK_CANDIDATES` | 10 | candidates taken from each source (lexical and vector) |
| `MEMCUE_JUDGE_RERANK_TIMEOUT` | 2 s | one rerank request |
| `MEMCUE_JUDGE_TRIAGE_TIMEOUT` | 2 s | one triage request |
| `MEMCUE_JUDGE_TRIAGE_MIN` | 0.2 | probability of an action that sends a dream group to the main model |
| `MEMCUE_JUDGE_DUP_TIMEOUT` | 10 s | one duplicate request |
| `MEMCUE_JUDGE_DUP_MIN` | 0.5 | probability of "same claim" that retires a candidate |
| `MEMCUE_JUDGE_DUP_MAX_FACTS` | 400 | approved facts that still fit one duplicate request |
| `MEMCUE_JUDGE_DUP_LONG_CONTEXT` | off | send the 262k danger-zone header for the duplicate check |
| `MEMCUE_EMBED_TIMEOUT` | 2 s | one embedding request |
| `MEMCUE_EMBED_SPACE` | empty | label of the vector space when a server changes model under one name |

### Removed

| Old | Replaced by | Why |
|---|---|---|
| `MEMCUE_VET_URL`, `MEMCUE_VET_MODEL`, `MEMCUE_VET_KEY` | main `MEMCUE_API_*` | review runs on the main model; a fourth model is out of scope |
| `MEMCUE_RERANK_URL`, `_MODEL`, `_KEY`, `_KEY_ENV` | `MEMCUE_JUDGE_*` | one Jev endpoint, not a separate rerank endpoint |
| `MEMCUE_TRIAGE_URL`, `_MODEL`, `_KEY` | `MEMCUE_JUDGE_*` | one Jev endpoint, not a separate triage endpoint |
| `MEMCUE_JUDGE_MIN`, `_MAX_FACTS`, `_LONG_CONTEXT` | `MEMCUE_JUDGE_DUP_*` | the old names become duplicate-specific |
| `MEMCUE_RERANK_MIN`, `_CANDIDATES`, `_TIMEOUT` | `MEMCUE_JUDGE_RERANK_*` | group under the judge namespace |
| `MEMCUE_TRIAGE_MIN` | `MEMCUE_JUDGE_TRIAGE_MIN` | group under the judge namespace |
| `MEMCUE_EMBED_KEY_ENV` | `MEMCUE_EMBED_KEY` | direct key, same shape as the other two models |
| `MEMCUE_EMBED_MODE`, `_MARGIN`, `_MIN_SCORE`, `_MAX_FACTS` | removed | vector is a candidate generator, not a standalone reader; Jev decides |

Operational flags stay: `MEMCUE_VET`, `MEMCUE_VET_POLICY`, `MEMCUE_VET_TTL_DAYS`, `MEMCUE_DREAM`,
`MEMCUE_MEMORY_DIR`, `MEMCUE_MIN_COVERAGE`, `MEMCUE_EXTRACT`, `MEMCUE_FACTS`, `MEMCUE_RECALL_LOG`,
`MEMCUE_HOST`, `CLAUDE_CONFIG_DIR`, `XDG_STATE_HOME`.

## Recall flow

```
prompt
  -> lexical candidates: FTS5, existing MinTerms/MinMatched/MinCoverage/ticket gate
  -> + vector top-N (only when judge and embeddings are both configured)
  -> ticket exactness applied to the combined candidate set
  -> Jev: one noul per candidate, keep >= MEMCUE_JUDGE_RERANK_MIN, sort, cap MaxFacts
  -> hits
```

Fallbacks are deliberate:

- Jev error, timeout or missing settings: the lexical answer, unchanged.
- Embeddings error or missing settings: Jev over lexical candidates.
- Both missing: lexical only. The vector reader is never a standalone reader; `only` mode and
  its margin/floor knobs are removed.

The lexical gate stays lexical: `MEMCUE_MIN_COVERAGE` continues to filter word candidates. The
ticket exactness rule is a hard rule and must also be applied to vector candidates, otherwise a
prompt naming `PROJ-1235` can surface `PROJ-1234` through semantic similarity. This depends on
the rerank-gate issue.

## Operational details

- One judge endpoint owns one key and one pause/health state. Triage, rerank and
  duplicate share `internal/judge` and the `judge.pause` file in the binary's state directory
  (`$XDG_STATE_HOME/memcue` for the default base, a per-base subdirectory otherwise, or
  `MEMCUE_STATE_DIR`); embeddings keep their own pause because they are a different endpoint.
- `recall.log` records the candidate source (lexical/vector) and the judge score separately;
  `Hit.Meaning` is the vector similarity before the judge and the judge score after it.
- `memcue eval` exercises the combined flow and reports the source mix, so the measured
  129/180 mode is reproducible after the refactor.
- The old variable names should fail with a message naming the replacement, or be rejected at
  startup; silently ignoring them would leave a 0.3.0 setup half-configured.

## Other decisions and non-goals

- **Review runs on the main model.** `MEMCUE_VET=1` remains off by default. A contradiction with
  an approved fact is `unsure` (not `drop`); by default it becomes provisional for
  `MEMCUE_VET_TTL_DAYS`, recalled after approved facts. `MEMCUE_VET_POLICY=quarantine` keeps the
  human queue.
- **Duplicate check is one Jev role.** A base above 400 approved facts (1,700 with long context)
  is split into chunks of `MEMCUE_JUDGE_DUP_MAX_FACTS`; the strongest "same claim" wins. The
  2.6k base therefore costs several requests per candidate; precision on it is not measured yet.
- **Triage is the same Jev role**; it is a cost filter for dream groups, not a fourth model.
- **No Jev-as-retriever.** Whole-base Jev retrieval measured 12/120 useful top-1, worse than
  bge-m3 (21) and lexical (44). Jev orders candidates; it does not generate them.
- **No `only` mode, no `MEMCUE_EMBED_MARGIN`, no absolute vector floor.** The vector model
  proposes top-N; the judge decides.
- **Schema is unchanged.** `fact_vectors` stays; `memcue embed` stays. No migration.
- **0.4.0 is a breaking configuration release.** Old variable names are removed, not aliased;
  the README and release notes carry the migration table. If one release of warnings is wanted,
  it is a separate decision.

## Consequences

- Three models, two provider keys plus a local embeddings server; the pilot's bge-m3 is local.
- Per-prompt cost/latency grows with the combined Jev request: 0.87 s median on the pilot,
  $0.0281 per 180 prompts. The pass stays opt-in and can be off on a laptop.
- Recall quality on the pilot: 129/180 useful top-1 vs 75 lexical; 18 of those wins exist only
  because the vector candidates reached Jev.
- The result is measured on one user's 2.6k base and one judge; it is directional, not universal.
- The base still contains false and stale facts; the 60-sample did not find `FALSE`, but it was
  not random. Base cleanup and the random audit remain separate work.

## Open work

- Measure the chunked duplicate precision and cost on the full 2.6k base.
- Random truth audit of the base; the 60-fact sample was not random.
- Decide whether provisional facts should auto-promote after use or stay TTL-only.
- ~~A/B the main model as the reranker.~~ Measured 2026-10-21: the main model is 4 useful top-1
  prompts behind Jev (108 vs 112 on 176 prompts) and about five times slower, despite labelling the
  sample; keep the judge. See [main-reranker-2026-10.md](research/main-reranker-2026-10.md).
- Decide whether vector candidates are added on every prompt or only on lexical misses; the
  pilot used every prompt, which is the measured 129/180 mode.
- Measure the chunked duplicate precision and cost on the full 2.6k base, not only the 41-candidate sample.
- Complete the random 160-fact truth audit if a base false-rate estimate is needed.
