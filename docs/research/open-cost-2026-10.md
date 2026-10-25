# What opening the base costs, October 2026

Status: **measured 2026-10-25 for [#396](https://github.com/juev/memcue/issues/396); the stamp shipped with it.**
Every `memcue search` and `session-start` opens the base, and `store.Open` checked the index against the facts in
full each time: 45 ms of the 54 ms a search took end to end. A stamp of the row counts the last check passed with
brings the open to under a millisecond and the search to 9.4 ms.

## Method

A copy of the live Claude Code base on macOS: 10 769 fact rows, 2 858 live facts, schema 5, release 0.11.1.

- **Per stage.** A throwaway test inside `internal/store` timed each step of `Open` and one `Search`, 40 opens of
  the same copy.
- **End to end.** `hyperfine -N -w 3 -r 40` on `memcue search '<prompt>'` with `MEMCUE_FACTS` on a copy, two
  prompts (`memcue dream hermes`, `как настроен merge driver`), the build of `main` against the build of the branch.
- **After a write.** The same with `--prepare 'memcue add …'`, so each run is the first open after a new fact.

## Where the time went

| step of `Open` | p50 |
|---|---|
| `migrate` (schema already current) | 0.18 ms |
| `dropVectors`, `ensureColumns`, `ensureIndexes` | 0.06 ms |
| `ensureIndex` | **45.0 ms** |
| `Search` itself | 0.43 ms |

Inside `ensureIndex` three queries take it all: the count of live facts (13.6 ms), the live facts without an index
row (14.9 ms) and the index rows without a live fact (16.5 ms). Each finds the latest version per id with a
correlated `max(version)` over all 10 769 rows. The counts of the tables themselves are cheap: `facts` 1.6 ms,
`fact_fts` 0.66 ms, `fact_index` 0.03 ms. On this base the check found nothing to repair; it ran on every open anyway.

## The stamp

Rows of `facts` are never deleted, and `Merge` renumbers only by adding, so any write — by this memcue, an older
one, or a merge — changes the count of fact rows. After the full check passes, or after `Rebuild`, the base keeps
`facts=N index=M fts=K` in a `stamps` table. An open that counts the same three numbers trusts the index; any other
count runs the full check once and stamps again. A lost index row changes the index count, so it is still repaired
on the next open.

| | `main` | stamp |
|---|---|---|
| `ensureIndex`, p50 | 45.0 ms | 0.70 ms |
| `memcue search`, end to end | 54.0 ± 0.6 ms | **9.4 ± 0.3 ms** |
| first search after a write | — | 78.7 ± 1.7 ms |

The first open after a write runs the full check and writes the stamp, a commit of its own, hence the 78.7 ms.
Stamping inside the write itself would remove it, but then the write has to read before it writes in the same
transaction, which lets two processes deadlock on the lock upgrade; one slower search per write is the cheaper
trade.

The table needs no schema version: an older memcue does not read it, and the facts it writes change the count.
