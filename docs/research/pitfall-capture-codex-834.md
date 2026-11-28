# Hermes pitfall capture: Codex smoke after #834

## Question and setup

Does the [new pitfall checkpoint](https://github.com/juev/memcue/pull/834) also preserve the intended distinction on the Codex route? This is a bounded replication, not a replacement for the larger [DeepSeek study](pitfall-capture-827.md).

Compared the full rendered Hermes `system_prompt_block()` and four native memory tool schemas from [`7379259`](https://github.com/juev/memcue/commit/73792592f1deaa8e48ca9d5eddd029e256820b00) and [`cf4bcfe`](https://github.com/juev/memcue/commit/cf4bcfe79fbfbaaf822e75b438a22ba80073cade). Both invoked real Hermes memory handlers and the same native v0.41.0 CLI built from the candidate source. The subject was `gpt-6.2-sol`, OpenAI Codex Responses through the Hermes credential resolver, low reasoning effort, a trusted UTC date, no SDK retries, at most six requests per decision episode and four per fresh recall; 1,536 output tokens per request. Each episode used an isolated synthetic Git repo and SQLite database. The bridge was not exercised; the probe used the provider, not the full gateway or its prompt.

The fixture ran a generator twice on clean disposable copies. An unset `CERULEAN_ORDER` failed with `TEAL-A713`; setting only `CERULEAN_ORDER=prepare-first` succeeded on both copies. The same observed output bytes were returned by a fixed fixture tool or pasted in the user message. The other controls used a literal no-write request and a pre-seeded same-scope equivalent fact. For a direct pass, the observed remedy must be stored as one searchable `pitfall` in the actual repo scope with `source=tool`. Each control requires unchanged history and no mutation call. Candidate fresh recall must search and mention the exact remedy without changing history. This contrast changes the entire shipped provider, not an isolated phrase.

## Results (2026-11-28 UTC)

| Episode | Baseline | #834 candidate |
| --- | --- | --- |
| Direct tool evidence | Saved one repo pitfall retrievable by ordinary search, `source=tool` | Same; a fresh conversation found and used it without writing |
| Same bytes pasted by user | **Wrong write:** saved a new repo pitfall as `source=agent` | No search or mutation |
| Direct evidence, user forbids writing | No mutation | No mutation |
| Direct evidence, equivalent same-scope fact already stored | No mutation | No mutation |
| Decision score | 3/4 | 4/4 |
| Memory searches | 5 | 5 |
| API responses | 14 | 13 |
| Input / output tokens | 41,400 / 708 | 42,938 / 565 |

Token and request totals cover the four decision episodes per arm, not the separate candidate recall or preliminary smoke. Both direct writes got native default expiry (2026-12-28). The candidate direct case made one preliminary search before asking the fixture tool and another specific lookup after evidence; the guidance is not a hard tool budget.

## Failed and preliminary runs

- The one-episode candidate transport smoke completed with one searchable write and successful fresh recall. It is not included in the paired counts.
- The first paired attempt is **invalid for model comparison**: 6 of its 8 cases received 14 native tool errors. Python cached the first per-case temporary directory; later calls used that already removed directory even though the environment changed `TMPDIR`. This was a runner problem, not a model or installed-plugin regression. The corrected harness scopes `tempfile.tempdir` per case and first proved two consecutive isolated native calls work without inference. A new eight-episode run completed without that error and is the only paired evidence above. The invalid run was not pooled or re-scored.

## Decision and limits

Keep #834. On this model and fixture, the visible difference was **avoiding one unsupported write**, not an improvement in positive capture over baseline. No claim about production rates, latency, full-gateway behavior, or other clients. Do not add another general instruction paragraph on this evidence. Scope #830 to **when to look up prior context**, preserving the newly validated Hermes capture checkpoint and the separate automatic pitfall snapshot; #829 and #831 concern request delivery and are not resolved by this model result. This Codex probe does not close the broader DeepSeek validation tracked in #826.
