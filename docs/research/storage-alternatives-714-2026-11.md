# Alternatives to SQLite for the sync server: Turso and git-backed databases, November 2026 (#714)

Status: **read and probed 2026-11-10; SQLite stays, product code unchanged.** The owner asked two questions while
[#714](https://github.com/juev/memcue/issues/714) was being built: could another local database replace SQLite, and
could a database that syncs through git be used instead of a sync server of our own. Neither candidate fits. Turso
Sync syncs only with Turso Cloud and does not resolve conflicts. Dolt needs cgo and ICU, its probe binary is 147 MB,
and a second process that opens the same database cannot write. DoltLite, a SQLite fork with Dolt storage, keeps
FTS5 and lets several processes write, but it needs cgo, leaves same-column conflicts to the application and syncs
through its own remotes, not git. The other git-backed libraries found are hobby projects or have no merge.

## What the storage has to do

The requirements come from the sync server contract of #714 (`docs/specs/sync-server.md`, on the #714 branch when this
note was written) and from how memcue runs today:

- One self-contained Go binary, cross-compiled without cgo (`modernc.org/sqlite`). The installed 0.35.0 release on
  the Mac is 29 MB.
- Several short-lived processes open the same base at once: the prompt hook, `memcue add` from the agent, the helper
  applying server rows, and dream on the server.
- Writes work offline and are found by search at once (R1, R3).
- Concurrent changes merge per field with HLC registers. A losing `content` is kept as a new fact (R5–R7).
- Lexical search with BM25 over precomputed stems (FTS5 today).
- The long poll sees commits of another process through `PRAGMA data_version`.

The live Mac base on 2026-11-10 held 3 774 facts in a 2.5 MB index; `memcue search` took about 10 ms end to end.

## Turso Sync

Turso Sync (`tursogo`, `NewTursoSyncDb`) looks closest to #714: a local database with push and pull. The pages read
say:

- The remote is Turso Cloud: "Push local writes to Turso Cloud" (Go SDK reference). Self-hosting is not mentioned.
- The public beta post of 2025-03-31: "beta quality, and not yet recommended for production use", "no durability
  guarantees, which means data loss is possible", "Conflict detection (but resolution is not yet implemented)".
  The planned strategies are `FAIL_ON_CONFLICT`, `DISCARD_LOCAL`, `REBASE_LOCAL` and a manual resolver (offline
  writes post, 2024-10-25). None of them is a per-field merge that keeps the losing text.
- `tursogo` needs no cgo. The older embedded replicas (`go-libsql`) need cgo and send writes to the cloud primary, so
  they cannot write offline.
- FTS5 or BM25 support in the Turso engine is not stated on the pages read.

A status newer than 2025-03-31 was not found. Even with sync out of beta, memcue would still keep its own merge,
outbox and search, and would add a dependency on a cloud service.

## Dolt

Dolt is a SQL database with git-style versioning. Its documentation says that a Git repository can serve as a Dolt
remote, with the data under `refs/dolt/data`. Merge is cell-level: "if multiple clients updated different cells …
the merge succeeds"; the same cell changed on both sides is a conflict that goes to `dolt_conflicts` and must be
resolved by the application. "By default, Dolt will not allow you to commit transactions that have merge conflicts."
There is no automatic policy for data conflicts. Dolt has `FULLTEXT` indexes with `MATCH … AGAINST` since v1.8.5
(July 2023).

The documentation does not cover cgo, binary size or several processes, so a probe measured them on darwin/arm64 with
Go 1.27.3 and `github.com/dolthub/driver/v2` v2.4.1 (Dolt `go` module of 2026-10-02). The program opens a database
directory, creates a table with a `FULLTEXT` index, inserts 50 rows and runs one `MATCH … AGAINST`.

| Check | Result |
|---|---|
| `CGO_ENABLED=0 go build` | fails: `github.com/dolthub/gozstd` is cgo |
| `go build` with cgo | fails: `github.com/dolthub/go-icu-regex` needs `unicode/regex.h` |
| `go build` against Homebrew `icu4c@79` | builds; 147 305 138 bytes, links `libicui18n`, `libicuuc`, `libicudata` dynamically |
| one process | opens in 76 ms, 50 inserts and the search in 294 ms in total |
| three processes on one directory at once | one writes; the other two open read-only without an error, and every insert fails with `Error 1105: cannot update manifest: database is read only` |
| `FULLTEXT` search | works |

So Dolt brings what memcue avoids by using `modernc.org/sqlite`: a binary five times larger, cgo and a system library on every
machine, and one writer per base. Several processes would need `dolt sql-server`, a daemon in front of a local file.
Its merge does not remove the merge of #714 either: a same-cell conflict is the application's to resolve, and that is
the HLC register merge again, written against `dolt_conflicts`.

## DoltLite

DoltLite is a fork of SQLite in C that replaces the B-tree with Dolt's prolly-tree storage and adds commits, branches,
merge and push/pull as SQL functions. The vendor calls it beta: the [beta post](https://www.dolthub.com/blog/2026-08-31-doltlite-beta/)
of 2026-08-31 names version 0.50.0, says the file format is "frozen for the beta", and that "small `autocommit` writes
… are 3.1X slower than SQLite". The [README](https://github.com/dolthub/doltlite) states "One durable writer at a time.
A concurrent writer gets `SQLITE_BUSY`", while readers stay live. Its remotes are `file://`, an HTTP server
`doltlite-remotesrv` that "binds to localhost until TLS and authentication are configured", and DoltHub; git remotes
are not mentioned. The Go [bindings](https://github.com/dolthub/doltlite/blob/master/doc/doltlite/bindings.md) list
Linux and macOS, not Windows. The [datalib post](https://www.dolthub.com/blog/2026-10-29-doltlite-datalib/) of
2026-10-29 describes an application by Imbue that stores mirrored personal data in DoltLite, one commit per sync.

A probe on darwin/arm64 with Go 1.27.3 and `github.com/dolthub/doltlite-driver` v0.51.10:

| Check | Result |
|---|---|
| `CGO_ENABLED=0 go build` | builds, but the driver is not registered: `sql: unknown driver "doltlite"` |
| `go build` with cgo | builds in 31 s from the vendored amalgamation; 13 176 834 bytes, no library outside the system |
| FTS5 with `unicode61` and `bm25()` | works |
| six processes writing one file at once, 50 rows each | no failed insert; all 400 rows of the run are in the table and the FTS index |
| `dolt_merge` of branches that changed different columns of one row | merges both changes, no conflict |
| `dolt_merge` of branches that changed the same column of one row | the whole merge is rolled back, including the other row's clean change; the Go call returns no row and no error |

DoltLite removes the two blockers of Dolt for several processes and for search, but not the others:

- memcue releases are built with `CGO_ENABLED=0` for linux and darwin on amd64 and arm64 (`.goreleaser.yaml`). With
  DoltLite every target needs a C toolchain.
- A same-column conflict is still the application's to resolve. That is the HLC register merge of #714 again, inside
  an explicit transaction, since under autocommit the conflict silently discards the merge.
- Delivery would still need a server of our own and a change notification: the remotes are pull-based and the HTTP
  remote server is local-only without TLS and authentication.

One property is new: DoltLite keeps every version of a row, which the #714 server and replicas deliberately do not
(one row per fact, no history). If the server ever needs a history again, DoltLite is the candidate to measure first.

## Other git-backed libraries

These were seen in search results only; their code and documentation were not read:

- `nickyhof/CommitDB`: a SQL engine where every transaction is a git commit, with branches and push/pull. Its own page
  calls it a hobby project not meant for production.
- `gogitdb/gitdb`: an embedded document store that syncs with a git remote every 5 s by default. How it resolves
  conflicts is not stated in the snippets.
- `umputun/stash` (`app/git` package): versions a key-value store in a local repository with optional push; the
  snippets show no pull or merge.
- Irmin and `git-kv` are OCaml libraries.

## memcue's own git storage

memcue already stores its base in git: a JSONL file per fact, a merge driver and textconv
([#530](https://github.com/juev/memcue/issues/530), [git growth](git-growth-530-2026-10.md),
[compaction](text-history-compaction-575-2026-10.md)). #714 moves away from it because the merge driver, textconv and
`write_id` form a mechanism of their own that breaks and is repaired by hand, and because git delivers a change only
when somebody commits and pulls. A git-backed database would keep that delivery model and would still need the same
conflict rules.

## What was not checked

- Turso's sync documentation (`docs.turso.tech/sync/usage`) and any Turso status newer than 2025-03-31.
- Dolt with several processes through `dolt sql-server`, the size of a push to a git remote, and Dolt on Linux.
- DoltLite on Linux, its sync between two machines, the pure-Go sync library `dolthub/doltlite-go`, and its
  concurrency contract (`doc/doltlite/concurrency.md`).
- The code of CommitDB, gitdb and stash.

## Sources

- Turso: [Go SDK reference](https://docs.turso.tech/sdk/go/reference),
  [Offline Sync public beta](https://turso.tech/blog/turso-offline-sync-public-beta),
  [Offline writes](https://turso.tech/blog/introducing-offline-writes-for-turso),
  [Embedded replicas](https://docs.turso.tech/features/embedded-replicas/introduction.md); read 2026-11-10.
- Dolt: [remotes](https://www.dolthub.com/docs/concepts/dolt/git/remotes),
  [merges](https://www.dolthub.com/docs/sql-reference/version-control/merges),
  [concurrency, 2026-02-17](https://www.dolthub.com/blog/2026-02-17-dolt-concurrency/),
  [full-text indexes, 2023-07-26](https://dolthub.com/blog/2023-07-26-announcing-fulltext-indexes),
  [driver](https://github.com/dolthub/driver); read 2026-11-10. The probe was run the same day.
- DoltLite: [datalib, 2026-10-29](https://www.dolthub.com/blog/2026-10-29-doltlite-datalib/),
  [beta, 2026-08-31](https://www.dolthub.com/blog/2026-08-31-doltlite-beta/),
  [README](https://github.com/dolthub/doltlite),
  [bindings](https://github.com/dolthub/doltlite/blob/master/doc/doltlite/bindings.md); read 2026-11-10. The probe was
  run the same day.
- Search results: [CommitDB](https://pkg.go.dev/github.com/nickyhof/CommitDB/v2),
  [gitdb](https://github.com/gogitdb/gitdb), [stash/app/git](https://pkg.go.dev/github.com/umputun/stash@v0.19.0/app/git).
