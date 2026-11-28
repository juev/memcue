# Memory regression suite

Run this in addition to the issue-specific experiment and normal CI:

```sh
make test-memory-regression HERMES_PYTHON=/path/to/hermes-venv/bin/python
```

Use Go and Node as in [CI](../../.github/workflows/ci.yml), and a Python environment with the released Hermes distribution installed (CI pins `hermes-agent==0.17.0`). When using a source installation, set `MEMCUE_HERMES_DISTRIBUTION` to its root. The target builds the candidate CLI and reuses checked-in tests with disposable state. It makes no model calls. Check the output for unexpected skips; in particular, a skipped Hermes dispatcher test does not verify delivery. CI runs these same test files as part of broader jobs.

| What must not regress | Existing executable tests |
| --- | --- |
| Conditional lookup, historical fallback only where supported, and shared policy text versus client-specific delivery | [`test_cross_harness.py`](../../plugins/test_cross_harness.py), native `SessionStart` |
| Actual Hermes add/search/correct/retire calls, grounded-capture instructions, no-write restrictions, result scope and expiry | [Hermes provider tests](../../plugins/hermes/memcue/tests/test_provider.py) |
| Retirement reason, exact same-scope refusal and reopening | [Native retirement fixtures](../../cmd/memcue/retirement_reason_test.go), [reopen fixture](../../cmd/memcue/reopen_test.go) |
| Complete snapshot and cwd changes, unavailable versus empty, literal user frames, string/part shapes and transport serialization | [Hermes request tests](../../plugins/hermes/test_request_context.py), [#829 experiment](hermes-context-ownership-829.md) |

## Model evidence is a separate check

A green suite proves the tool and delivered-text contracts, not model compliance. For changes that specifically aim to improve model decisions, compare matched isolated scenarios and inspect what actually persisted. Reuse the checked-in [working-model comparisons and fixtures](working-model-820-821-822.md) for action selection. The [DeepSeek capture study](pitfall-capture-827.md) and [Codex follow-up](pitfall-capture-codex-834.md) describe actual write/recall cases: direct reproducible evidence, identical user-relayed output, a no-write request, and an existing scoped duplicate. Those reports are evidence and case specifications, not executables in the default suite. Do not substitute them for a new model run or send live memory to a model as a regression fixture.
