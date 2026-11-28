# One conditional memory-search rule (#830)

## Hypothesis and acceptance

The policy inputs at `bc27627` sometimes tell the model both to skip lookup when it cannot help and to search at the start of every task. Can the integrations express one rule without changing any retrieval/write behavior? The contract is: search before answering or acting if prior context can change the next step; skip self-contained work; look up possibly applicable rules on resumption or switching systems; use prior experience when it can help diagnose an unexpected result. Use a supported history fallback only after an unhelpful lookup if the history still matters. The Kimi `pitfalls` command, write-time duplicate checks and explicit research-shelf searches are separate behaviors and stay intact.

## Deterministic comparison

| Attempt | Result |
| --- | --- |
| New policy check against `bc27627` | Eight of nine checked inputs failed the conditional contract. Only the Hermes input passed. |
| Same check after updating the wording | All nine passed. |
| First full cross-harness run | One old test still required DSH not to mention unexpected errors. The new shared contract supersedes that expectation. |
| First targeted Go run | Old text expectations failed; the long temporary path also exceeded the Unix-domain socket path limit for helper tests. |
| Updated tests and shorter path to the same scratch directory | All 18 cross-harness tests and the targeted Go session-start/card tests passed. |
| CLI adapter tests with the shortened `/proc/.../fd` alias | Codex's standalone installation test compared the alias to a resolved path and failed; the alias is only useful for the Unix-socket tests. |
| CLI adapter tests with canonical scratch paths | Codex: 27 passed, 7 opt-in skips; Qwen: 26 passed; Kimi: 24 passed. |
| First reusable [memory regression suite](memory-regression.md) run | 116 passed with no skips; the targeted Go selection did not yet include the historical note comparison. |
| First full CI on that suite | The historical #647 note-preservation test failed. Its expected note still demanded the old recall lines; the archive was not changed. |
| Expanded reusable suite | 121 passed: 2 Hermes dispatcher, 9 native Go (including the history, shelf and measured-note contracts), 92 Hermes provider and 18 cross-harness; no skips. A first count check expected eight Go tests; JSON readback identified all nine by name. |

Reproduce on the candidate with `python3 plugins/test_cross_harness.py` and `go test ./cmd/memcue -run 'SessionStart|SessionCard'`. The first runner reads the delivered source inputs and executes the Codex startup adapter. The second exercises the native startup/card output. The DSH history test exercises the registered prompt. Existing tests preserve identical shared text for DSH, Pi and OpenCode and keep CLI instructions-only fallbacks separate. The history mechanism is unchanged; only the recommendation to use it is narrowed.

## Decision and limits

Use the established Hermes rule in the other policy surfaces; leave Hermes capture and ranking, the automatic pitfall snapshot and all storage operations unchanged. The historical [Claude Code comparison](search-policy-647-2026-11.md) motivates the change but does not measure this candidate. No new inference was used, so this study verifies instruction consistency and delivery contracts, not model compliance or lower token cost.
