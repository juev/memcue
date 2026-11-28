# Working-model memory after Dream (#820, #821, #822)

## Prior evidence and decision

- [#661](dream-ablations-661-2026-11.md) found no measurable search-quality difference during a one-day replay. It was not a multi-week study of capture or the current scheduler.
- [#818](https://github.com/juev/memcue/issues/818) cross-checked 126 logged retirements against history in a bounded audit. The reported errors include standing guidance retired as singleton noise after one valid verdict. The counts neither estimate the overall false-retirement rate nor measure write-time filtering; the synthetic classification pilot there used already stored facts.
- Dream's singleton noise job moves to selective capture; duplicates to search-before-write; outdated claims to evidence-backed correction in context. Automatic splits, rescoping and cold-history sweeps are deliberately dropped. Existing history, expiry and explicit note migration do not depend on those jobs. No live memory was used for the pilot below.

## Paired pilot, 2026-11-28

[Runner and cases](working-model-820-821-822.py) · [complete synthetic function-call results](working-model-820-821-822.jsonl).

The subject was `gpt-6.2-sol`, through the Hermes OpenAI Codex Responses route, low reasoning effort. Each request was a fresh, independent scenario with the same tool set within its comparison. The action oracle is in the runner before inference. The 13 authored scenarios cover five capture cases, six evidence/correction cases and two retired-feedback cases; each has two or three arms. There were 28 completed requests with a permissive tool schema and 28 with a closer-to-native schema (scoped and enumerated add arguments plus observed versions). The initial transport attempt returned HTTP 400 for all 28 requests with a string `input`; those requests produced no decisions and are excluded. Using a user-message array was the only request-shape change before the completed permissive run.

Run from an environment that can import the installed Hermes provider and OpenAI SDK:

```sh
python3 docs/research/working-model-820-821-822.py > permissive.jsonl
python3 docs/research/working-model-820-821-822.py --native-schema > native-schema.jsonl
```

`OK` includes the expected action, explicit user provenance for the user rule, and (in the constrained run) exact fixture scope, expected version and `tool` provenance for verified non-user saves. Input and output tokens are provider totals by row; latency is the median per request in milliseconds. No price was returned. These were model tool calls, not simulated model responses, but the runner did not dispatch them against a live or per-case fact store.

| Schema | Comparison | Arm | OK | Input tokens | Output tokens | Median ms |
|---|---|---|---:|---:|---:|---:|
| Permissive | Capture | baseline | 5/5 | 2282 | 294 | 2471 |
| Permissive | Capture | standing | 5/5 | 2632 | 287 | 2315 |
| Permissive | Evidence | baseline | 6/6 | 2756 | 263 | 2074 |
| Permissive | Evidence | standing | 6/6 | 3290 | 271 | 1987 |
| Permissive | Retired feedback | none | 2/2 | 933 | 98 | 1950 |
| Permissive | Retired feedback | search | 2/2 | 1157 | 121 | 2156 |
| Permissive | Retired feedback | save | 2/2 | 1163 | 118 | 2248 |
| Constrained | Capture | baseline | 3/5 | 2747 | 242 | 2386 |
| Constrained | Capture | standing | 3/5 | 3097 | 327 | 2902 |
| Constrained | Evidence | baseline | 6/6 | 3314 | 258 | 1762 |
| Constrained | Evidence | standing | 6/6 | 3848 | 265 | 1992 |
| Constrained | Retired feedback | none | 1/2 | 1119 | 174 | 2738 |
| Constrained | Retired feedback | search | 1/2 | 1343 | 89 | 1872 |
| Constrained | Retired feedback | save | 1/2 | 1349 | 157 | 2548 |

**What happened.** Both capture arms skipped the one-off and the duplicate and selected all three durable cases. The two constrained misses in each capture arm were wrong provenance (`agent` for the verified pitfall and the different-project observation), not omitted facts. The model abstained for the wrong host, conflicting facts without evidence, and the version conflict. Both evidence arms preserved the Windows condition on correction and supplied specific reasons for the two justified retirements. All feedback arms skipped the stale repeat and attempted to save the verified recurrence with a v9 qualifier; the constrained misses were provenance (`agent` in the first two arms and `user_confirmed` with save-time feedback where the script expected `tool`). The synthetic feedback was delivered as scenario context, not a measured two-turn interception of a native tool call.

**What this supports.** The working model made the capture and mutation decisions without a background pass. The additional instruction did **not** reduce noisy writes relative to the baseline on this sample; it makes the intended safety contract explicit, not an empirically superior prompt. This pilot cannot establish long-term recall equivalence, estimate rare harmful retirements, or tell whether reminders improve behavior. No timer, blanket verification, or background reviewer is justified by these results. Keep standing selective guidance.

**Why save time.** Neither feedback arm beat the baseline in the small comparison. Use the lowest-exposure deterministic default: report an exact, same-scope retired match before an add or text correction can commit. Regular search never returns invalidated text, and the implementation does not classify paraphrases or guess at scope. A retired match is not permission to restore. An exact recurrence uses `reopen` only with new evidence; a qualifier that actually changes the claim can be captured as new. Forgotten matches return a generic refusal only. This is a boundary on identical claims, not a semantic ban on re-learning old ideas. Offline replicas cannot detect retirements they have not received.

## Native verification boundary

The tests independently exercise native writes and readback: required reason, text correction and batch refusal without partial writes, ordinary search exclusion, reopen, sync status/reason association under a concurrent correction, and wire round trip. Rebuilding a text base's SQLite projection retained a retirement and its reason; trying to re-save the forgotten claim returned a generic warning without the text, reason or ID. A disposable PostgreSQL 16.2 instance passed the native retirement/readback/reopen test, including exact-recapture refusal. The opt-in Linux test with an isolated Headscale 0.29.7 server and two replicas initially found a fixture still calling `retire` without a reason. After updating the fixture, it passed: both replicas read the same reason during a competing correction, the second replica refused the original scoped claim, and reopening delivered the corrected text without an active retirement reason. The server and replicas still retain only the latest row, as does PostgreSQL; local SQLite and text history keep old versions.

A local upgrade smoke started from a `v0.40.11` binary and a SQLite fixture with active and retired history plus an old Dream schedule. The new binary preserved both version arrays field-for-field, kept ordinary recall active-only, and made zero requests to a loopback endpoint across session-start and the old `session-card -dream-tools` invocation. The obsolete Dream command failed and created no new Dream state. Upgrade servers before writers so older binaries do not discard the new reason field. See the README for the compatibility contract.
