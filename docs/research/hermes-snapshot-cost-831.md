# Hermes per-request pitfall snapshot cost (#831)

## Hypothesis and acceptance

On baseline `b8c5849`, an eligible request incurs a local native read even when its repository snapshot is empty. The empty response is not interchangeable with an unavailable read: the model needs a current result that supersedes any previous snapshot. An acceptable optimization must keep that distinction, the complete current fact list, refreshes after mutations at the same cwd, and the existing transport shape. Measure those boundaries before adding state or a budget.

## Setup

[Executable benchmark](../../plugins/hermes/measure_request_context.py), local Linux x86-64, Python 3.14.9, Go 1.27.3, installed Hermes source distribution (package metadata reports `0.0.0`), memcue `v0.41.0-14-gb8c5849`. No inference, production config or facts. All cases used the same isolated SQLite base per run. Pre-warm each case; interleave 50 requests per case in a fixed seeded order. Repeat on fresh fixture homes. Timing covers the actual Hermes dispatcher, request bridge and native command; payload byte deltas use compact UTF-8 JSON and exclude preflight, serialization time and network. p95 uses linear interpolation. The third row-set used the final benchmark source; the first two runs used the same cases, timing and oracle before added type assertions.

```sh
make build
MEMCUE_BIN="$PWD/bin/memcue" MEMCUE_HERMES_DISTRIBUTION="$HERMES_SOURCE" \
  "$HERMES_PYTHON" plugins/hermes/measure_request_context.py --samples 50
```

## Results

| Request cwd / active facts | Extra JSON bytes per request | Run 1 p50 / p95 ms | Run 2 p50 / p95 ms | Run 3 p50 / p95 ms |
| --- | ---: | ---: | ---: | ---: |
| Outside Git / 0 | 111 | 13.139 / 17.303 | 12.989 / 17.405 | 12.841 / 19.839 |
| Empty repository / 0 | 235 | 19.141 / 32.962 | 18.462 / 25.482 | 17.490 / 20.859 |
| Populated repository / 195 | 48,786 | 21.130 / 31.544 | 21.590 / 34.926 | 20.544 / 42.212 |
| Stress repository / 1,950 | 485,782 | 36.722 / 46.840 | 36.648 / 46.546 | 37.508 / 57.459 |

Each of the 600 timed requests invoked `session-card -pitfalls-json` once, including repeated unchanged snapshots. The probe checked exact whole-fact equality on every request. The real Codex Responses local preflight and JSON output preserved the 195- and 1,950-fact snapshots, without changing `instructions`. This exercises local transport handling through 485,782 added bytes, **not** an upstream context window, API request/body limit, billed tokens, provider cache behavior, live latency or the largest possible snapshot.

The same transformed request was exercised after switching cwd to an empty repository or outside Git, after adding a fact without changing cwd, and after a real native SQLite open failure and recovery. The bridge replaced the old snapshot, exposed the new fact promptly, and distinguished unavailability from confirmed emptiness. In-process redispatch did not append duplicates.

## Rejected shortcuts and decision

- A cwd-only cache fails the freshness control: the new fact is required in the next request at exactly the same path. There is no cache change here.
- Suppressing every empty card removes the explicit invalidation after a populated snapshot. The control removed the appended frame, recovering the unchanged user payload but losing the `supersedes earlier` notice. This measures the missing signal, not how often a model would act on stale context.
- An in-process freshness oracle could avoid some subprocess cost, but would need to account for native scope changes, local writes, expiry and replica sync. The baseline does not justify implementing or shipping that state machine, truncating any facts, or adding another system instruction. Keep delivery as-is. Reconsider only with observed provider-side pressure and a design that preserves the controls above.

Ran `make test-memory-regression`: **121 passed**, no skips. No production change is proposed.
