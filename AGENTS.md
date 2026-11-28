# memcue contributor instructions

## Work one issue at a time

- Work through the agreed issue queue in order. Finish the current issue and record its outcome before starting the next one. Do not run issues in parallel.
- Use experiments to decide what to change. State the hypothesis, baseline, and acceptance criteria before running a comparison. A deterministic reproduction and regression test are enough for a deterministic bug; do not add model calls where they are not needed. Do not ship a candidate just because it was tried.
- For changes to memory behavior, run the [memory regression suite](docs/research/memory-regression.md) on the candidate in addition to the issue-specific experiment and normal CI. Do not count skipped tests as passes. Keep model comparisons separate from the model-free suite.
- Record every experiment in [`docs/research/`](docs/research/README.md), including failed attempts, negative or inconclusive results, and rejected candidates. Link the report from the research index. Record the tested revision, setup and method, observed results and limitations, and the resulting decision. Use synthetic examples or sanitized aggregates; do not commit private data or credentials.
