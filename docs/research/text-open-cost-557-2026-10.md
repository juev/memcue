# The cost of opening a text history, October 2026

Status: **measurements for [#557](https://github.com/juev/memcue/issues/557), 2026-10-28; the fix is in the same PR.**

In short:

- After the move to the text history ([#530](https://github.com/juev/memcue/issues/530)) search got 2–4 ms slower
  ([release-0-22-0-555](release-0-22-0-555-2026-10.md)). Nearly all of it is two stamp writes in `syncText` on every
  open, even when nothing changed. Each write is a separate commit with its own fsync.
- On the loffe disk (ext4 on md RAID) `syncText` cost 2.0 ms, 1.45 ms of it in the two writes. The flock, the
  directory scan, reading the stamps, `count(*)` and an empty transaction together take about 0.2 ms.
- A stamp is now written only when its value changes. Search on copies of the bases on the loffe disk: 20.2 → 16.7 ms
  (a file base: 15.9 ms) and 13.0 → 10.4 ms (9.8 ms). `syncText` takes 0.23 ms.
- What remains above a file base, 0.4–0.8 ms, is the flock, the directory scan and `count(*)`: the price of checking
  what git brought.
- A separate observation, not fixed: on the Mac, search over a cache built from the text is 0.3–0.7 ms slower than over
  a cache copied from the file. FTS5 `optimize` and `VACUUM` do not clearly close the gap.

## Method

Copies of the bases; the live bases are not written:

- Mac: the text `~/.claude/memory/facts` (4712 files) and the cache, taken with `sqlite3 .backup`;
- loffe: the same data from `~/.claude` on loffe;
- Hermes: the text of the Hermes base (983 files).

Each base has a file variant (`file.db`, a copy of the cache) and a text variant (the `facts` directory and a cache in
its own `MEMCUE_STATE_DIR`). The measurements, to separate the costs:

1. A temporary test in `internal/store` (not in the repository): `Open(cache)` against `OpenText`, then separately the
   flock, `textDirs`, reading the stamps, `begin + count + commit`, `saveStamp`, `saveTextRows` and `syncText`. 30
   repeats each, median and p90.
2. Temporary timers in the CLI's `withStore` (not in the repository): open, search and close inside one
   `memcue search`. 100 runs, file and text interleaved.
3. The whole `memcue search -n 5 "hermes loffe docker"` process: 0.22.0 against the fix, on a file and on text, 100–120
   interleaved runs; median.

The first run on loffe was in `/tmp` (tmpfs, where fsync is free) and showed a 0.6–1.0 ms difference. A repeat in
`$HOME` (ext4) showed 2.8–4.0 ms, as in production: the price of fsync shows only on a real disk.

## Before the fix

Phases of one `memcue search` on the loffe disk, median, ms:

| base | open file | open text | search on file | search on text |
|---|---|---|---|---|
| loffe (4712 facts) | 4.62 | 8.62 | 7.49 | 7.70 |
| Hermes (983 facts) | 1.54 | 4.37 | 4.21 | 4.40 |

The search itself costs the same; the whole difference is in the open. The steps of `syncText` on the same disk,
median, ms:

| step | loffe | Hermes |
|---|---|---|
| flock | 0.007 | 0.007 |
| `textDirs` (32 directories) | 0.062 | 0.074 |
| reading two stamps | 0.040 | 0.051 |
| `begin + count(*) + commit` with no write | 0.097 | 0.039 |
| `saveStamp(text-dirs)` | 0.720 | 0.717 |
| `saveTextRows` | 0.748 | 0.718 |
| `syncText` as a whole | 1.982 | 1.675 |

## After the fix

`syncText` on the loffe disk: 1.98 → 0.23 ms (loffe), 1.68 → 0.30 ms (Hermes).

The whole `memcue search` process, median, ms:

| where | base | 0.22.0, file | 0.22.0, text | fix, text | fix, file |
|---|---|---|---|---|---|
| loffe, disk | loffe | 15.94 | 20.20 | 16.74 | 15.93 |
| loffe, disk | Hermes | 9.81 | 13.00 | 10.38 | 9.78 |
| Mac | Mac | 12.63 | 14.51 | 14.09 | 12.71 |
| Mac | Hermes | 9.89 | 10.64 | 10.26 | 9.94 |

fsync is cheap on the Mac, and the stamp writes cost about 0.45 ms there, so the fix gained less on the Mac than on
loffe.

## A cache built from the text

On the Mac the text variant's cache was built from the text by the first run, not copied from the file as on loffe.
It is 3 MB larger (34.7 against 31.6 MB). Search with the fixed build, median, ms:

| cache | search |
|---|---|
| file base | 12.82–13.23 |
| text, cache copied from the file | 13.22–13.65 |
| text, cache built from the text | 13.87–14.36 |
| the same after `INSERT INTO fact_fts(fact_fts) VALUES('optimize')` | 14.10 |
| the same after `VACUUM` | 14.01 |

Two runs gave 0.3–0.7 ms between the built and the copied cache; `optimize` and `VACUUM` fall within the same noise.
The cause is not established. Hypothesis, not checked: a different order of rows and pages after a build in one
transaction. The effect is below the error of these measurements on the Mac: an Android emulator ran in another
session at the time.

## Limits

- One machine of each kind, one query in the whole-process measurements; the queries of
  [release-0-22-0-555](release-0-22-0-555-2026-10.md) were not repeated here.
- The Mac was loaded by other work, so its spread is larger than loffe's.
- The live bases were measured after the 0.22.1 release, below.

## The live bases after 0.22.1

The same script as in [release-0-22-0-555](release-0-22-0-555-2026-10.md), on the live bases after installing 0.22.1
([#559](https://github.com/juev/memcue/issues/559)), from the same directories as the 0.22.0 measurement. Median, ms:

| command | Mac 0.21.2 → 0.22.0 → 0.22.1 | loffe | Hermes |
|---|---|---|---|
| `search` (90 runs) | 12.2 → 13.9 → 13.3 | 13.4 → 17.1 → 13.5 | 7.0 → 9.7 → 7.5 |
| `session-start -part 1` | 59.6 → 55.1 → 55.5 | 315.0 → 292.6 → 288.7 | 14.5 → 17.2 → 16.0 |
| `add` | 124.7 → 118.6 → 116.0 | 240.8 → 237.6 → 200.6 | 26.9 → 32.7 → 31.3 |
| `correct` | 125.3 → 129.1 → 129.2 | 234.4 → 262.4 → 226.8 | 26.6 → 35.5 → 36.3 |

- Search on loffe is back to the file base's level; on Hermes 0.5 ms above it remains, on the Mac 1.1 ms. The loffe
  cache was built from the text at `git pull`; the Mac and Hermes caches were copied from the file by `migrate -text`.
  So the built-cache effect does not show on the live loffe base, and it does not explain what remains on the Mac.
  The Mac's remainder was not examined: the machine was loaded, and its spread is larger.
- Search answers of 0.22.0 and 0.22.1 to the 30 queries: 30 of 30 on the Mac and loffe, 29 of 30 on Hermes. The
  difference is in the query `text history JSONL facts`: the answer now includes the fact
  `01M5AE3GAGSXC00B244AAVVKKB`, which was corrected between the measurements to mention the text history. That is a
  change of data, not of search.
- Writes on the small Hermes base still cost 4–10 ms more than on the file base. The fix did not touch them: a write
  appends to the fact's file and saves the row stamp. Writes are rare next to searches; they were not examined.
- `facts` on loffe (638 ms) is a single run, the first command after loffe pulled new facts from the Mac; no
  conclusion is drawn from one run.
