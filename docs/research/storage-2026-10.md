# SQLite or DuckDB for the fact base, October 2026

Status: **measured 2026-10-23; SQLite stays.** The owner asked whether DuckDB would serve the memory better than the
SQLite file it uses — in particular whether it would work with git better, and whether its search is better. On this
workload the answer is no on both counts, and the reasons are not speed: they are the incremental index, the number of
processes that open the file, and what a Go release has to carry. Numbers below are from
[storage_bench.py](scripts/storage_bench.py), reproducible on any machine with the live base.

## The question, as asked

- Is DuckDB better for the base than SQLite?
- Does it work with git better (the memory is committed to a git repository and pushed between machines)?
- Is its search better?
- What else should change: the judge, the write side, the sync at session end?

The last bullet is a design question, not a measurement, and this note does not answer it. It answers the storage.

## What the engines are asked to do

memcue opens one file from several short-lived processes: a `UserPromptSubmit` hook on every prompt, `memcue add` from
the agent, `dream` in the background, the review page, and the Hermes provider in a container. Reads happen on every
prompt, writes once or twice a session, and a background pass may be running at the same time as a prompt. The query
is `MATCH` over the stems of the prompt, joined to the latest version of each fact, the first 80 in BM25 order; then
the coverage gate. The file is committed by the owner's own setup at the end of a session.

## Method

- **Corpus.** Facts are generated from the vocabulary of a live base (45 facts, both languages), 22–40 words each,
  drawn with the base's own word frequencies, so term frequency and document frequency look like a real base: 300,
  3 000 and 20 000 facts, 243 characters on average.
- **Prompts.** 120 real prompts from `recall.log` (median 17 words).
- **Schema.** The memcue schema as it is: `facts` with versions, `fact_index` and an FTS5 table over pre-stemmed text,
  `journal_mode=DELETE` and `synchronous=FULL` — the setting the base actually runs with, because git must never see
  a half-written file.
- **Timed.** Bulk load, index build, one insert plus commit (what `memcue add` pays), and the recall query.
- **DuckDB** 2.0.0, `fts` extension, `stemmer='russian'`, `incremental=1` (the fastest honest setting — see below).

## Results

Median of 20 inserts and of 120 queries; `p95` over the queries.

| facts | engine | bulk load | index build | insert + commit p50 | query p50 | query p95 | file |
|---:|---|---:|---:|---:|---:|---:|---:|
| 300 | SQLite, DELETE | 0.00 s | — | 0.43 ms | 0.13 ms | 0.45 ms | 0.4 MB |
| 300 | SQLite, WAL | 0.00 s | — | 0.13 ms | 0.12 ms | 0.41 ms | 0.4 MB |
| 300 | DuckDB | 0.02 s | 0.02 s | 4.77 ms | 6.53 ms | 7.43 ms | 2.1 MB |
| 3 000 | SQLite, DELETE | 0.02 s | — | 0.41 ms | 0.32 ms | 2.04 ms | 3.4 MB |
| 3 000 | DuckDB | 0.21 s | 0.10 s | 5.94 ms | 7.58 ms | 8.42 ms | 2.6 MB |
| 20 000 | SQLite, DELETE | 0.17 s | — | 0.52 ms | 1.58 ms | 12.63 ms | 22.5 MB |
| 20 000 | SQLite, WAL | 0.21 s | — | 0.10 ms | 1.60 ms | 12.18 ms | 22.5 MB |
| 20 000 | DuckDB, incremental | 1.47 s | 0.49 s | 6.19 ms | 9.13 ms | 10.97 ms | 8.1 MB |

The live base is 45 facts and 176 KB; 20 000 is far more than this traffic will produce in years, and both engines stay
far below what a prompt already pays for recall — 1.6 ms at the median on the lexical path of 634 live prompts, 1.1 s
when the judge is called. **Speed is not a reason to choose either.**

## The four things that do decide it

### 1. An index that follows writes

DuckDB's FTS is a materialised structure — `fts_main_<table>.terms`, `.docs`, `.dict` and friends — created by a
`PRAGMA`, not maintained by the storage engine. Without `incremental=1` a fact written after the last build is
invisible to `match_bm25`:

```
create index; insert row; query -> the new row does not appear; a rebuild makes it appear
```

With `incremental=1` (2.0, and the id column must then be `NOT NULL`) inserts and deletes are maintained. An update of
the indexed column is not: a fact whose content changed still matches its old words at its old score, which is exactly
what `memcue correct` does to a fact. memcue could dodge this by indexing every version row and deleting the old one,
but that is a design constraint the storage imposes on the schema, not a feature.

FTS5 keeps its index inside the transaction: an insert, an update and a delete are all visible to the next reader, and
a rollback leaves no index behind. For a base that is written on the same path it is read from, that is the whole
argument.

### 2. One process, or a server

DuckDB allows one process to open the file for writing. A second process cannot open it at all — not even read-only,
measured while the first held it open:

```
writer opened rw
('ro while writer:', 'IO Error: Could not set lock on file ".../conc2.duckdb"')
('ro 2nd:',        'IO Error: Could not set lock on file ".../conc2.duckdb"')
two ro (writer closed): ok, ok
```

The [concurrency docs](https://duckdb.org/docs/2.0/connect/concurrency) say the same: read-write mode is one process,
read-only mode is many readers and no writer, and multi-process writes need the Quack client-server protocol or
DuckLake over PostgreSQL.

That inverts memcue's shape. Today a `dream` run in the background and a hook on the next prompt simply meet in SQLite,
which queues the writer with `busy_timeout`. On DuckDB the hook would fail for as long as `dream` runs — and `dream` is
the pass that keeps the base from growing. The alternatives are a daemon (a new process to install, supervise and
version on four platforms, and a socket per prompt) or one writer queue inside a server. Both are a rewrite of the
deployment story to buy nothing the workload needs.

### 3. What a release has to carry

memcue ships one static binary per platform through goreleaser with `CGO_ENABLED=0`, for linux and darwin on amd64 and
arm64, cross-compiled from one machine. A hello-world with `github.com/duckdb/duckdb-go/v2`:

- `CGO_ENABLED=0 go build` → `build constraints exclude all Go files in go.duckdb.org/libduckdb/darwin-arm64`;
- `GOOS=linux go build` → the same, with `linux-arm64`: cross-compiling needs a C toolchain per target;
- the binary is **76 MB** against **20.9 MB** for the whole memcue binary.

The plugin downloads its binary over the network at `SessionStart` and checks it against a checksum
(`plugins/claude-code/scripts/memcue.sh`); a 76 MB download per platform, built separately on each OS, is a different
product.

### 4. Disk

DuckDB stores the same 20 000 facts in 8.1 MB against 22.5 MB, after a checkpoint — three times smaller, columnar
compression and no per-row overhead. It is the one real advantage, and at these sizes it does not matter: the base is
176 KB today and would be about 1 MB at 20 000 facts.

## Git

Neither file is git-friendly. Both are binary, and a commit of either stores the whole file: DuckDB has no
`textconv`/merge equivalent for its own format, SQLite has `sqldiff` and memcue already has a three-way merge driver
that unions `(id, version)` rows.

What makes a store git-friendly is not the engine but the encoding, and the owner's idea points at the right target
from the wrong side. If the tracked artefact were text — one line per fact version, which `memcue dump` already
prints for `diff` — then:

- a merge is a line union, and git does it without a custom driver;
- a diff is readable, so a change to memory can be reviewed like code;
- the database becomes a derived cache: 20 000 facts load in 0.17 s, so it can be dropped and rebuilt from the text at
  any time, and the engine choice becomes reversible — including a later move to DuckDB, if a reason appears.

That is a change of source of truth, not of engine, and it is worth its own note. It does not make DuckDB better at
git; it makes the engine irrelevant to git.

## What the owner's other questions need

- **The judge.** Not measured here. The existing notes give the pieces: the judge costs a median 1 133 ms on 23 % of
  634 live prompts (`recall.log`, this machine), flips the served pair on 7 of 12 prompts between identical runs
  ([judge-cut-2026-10.md](judge-cut-2026-10.md)), and on the gateway base serves 8 NOISE facts of 138 against 21 of 55
  for the words alone on an older holdout — different prompts, different bases, so the two numbers cannot be compared.
  Nobody has run judge-off against judge-on on one prompt set. That is the missing measurement, and it is cheap: the
  lexical arm needs no model calls at all.
- **The write side.** The end-to-end run measured the read side only ([e2e-files-2026-10.md](e2e-files-2026-10.md));
  whether an agent keeps facts as fully as extraction does has not been measured.
- **The sync at session end.** The wait is the network and, possibly, the extraction call, not the engine: a commit of
  a 176 KB file is milliseconds, and a 20 000-fact base loads in 0.17 s from scratch.

## Reproduce

```sh
pip install duckdb                 # optional: without it the script measures SQLite alone
python3 docs/research/scripts/storage_bench.py \
    --base ~/.claude/memory/facts.db --prompts ~/.local/state/memcue/recall.log --sizes 300,3000,20000
```

The Go probe:

```sh
mkdir duckprobe && cd duckprobe && go mod init duckprobe
cat > main.go <<'EOF'
package main
import ("database/sql"; "fmt"; _ "github.com/duckdb/duckdb-go/v2")
func main() { db, _ := sql.Open("duckdb", ":memory:"); var v string; _ = db.QueryRow("select version()").Scan(&v); fmt.Println(v) }
EOF
go get github.com/duckdb/duckdb-go/v2
CGO_ENABLED=0 go build -o probe .        # fails
GOOS=linux GOARCH=arm64 go build .       # fails
go build -o probe . && ls -l probe       # 76 MB
```

## Limits

- The corpus is synthetic: real facts would have a different list of words and a different duplicate structure, but the
  measurement is about latency and index maintenance, and both follow the shape of the postings, not the meaning.
- One machine, one disk, one day; the numbers are relative, not absolute.
- The SQLite arm indexes unstemmed text where DuckDB stemmed it with Snowball; stemming moves postings a little, not
  the order of magnitude.
- DuckDB 2.0 was two days old at this measurement. The process model and the CGO requirement are the parts least
  likely to change, and they are what the decision rests on.
