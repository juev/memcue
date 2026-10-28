# Quillon 1.8: configuration

## Limiter

`Limiter(rate="N/period", key=...)` limits each key to N requests per period (`second`, `minute`, `hour`).

## Backends

- `memory` (the default): counters live in the memory of the process. Each process counts on its own, so an
  application with N worker processes lets a key make up to N times the configured rate.
- `tally`: counters live on a tallyd server shared by every process (`backend="tally", url="tally://host:7070"`);
  the configured rate then holds across processes.

## Window

Windows are fixed and aligned to the clock: a `minute` window resets at the start of every minute.
