# Why the release binary is 15 MB, October 2026

Status: **measured 2026-10-24 ([#325](https://github.com/juev/memcue/issues/325)); the review page and its template are
removed (12,001,440 B); the other decisions are open.** The stripped linux/amd64 release binary is 14,794,912 B after #324. Three things make it up: the SQLite
driver, 5.35 MB with everything it drags in; the HTTP and TLS client, 3.60 MB; and `html/template`, which costs
2.31 MB, mostly not its own code. The template's reflection turns off the linker's removal of unused methods for the
whole binary. Size barely moves the start of the hook: with a cold page cache every build starts in about 15 ms. The
template and the package inits cost about 2 ms of a warm start.

## Method

All sizes are `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w"`, the flags of
`.goreleaser.yaml`, go1.27.0 cross-compiling from darwin/arm64, at `1675838`. The ladder is a set of probes under
[scripts/binsize](scripts/binsize), each a `package main` that adds one layer to the one below it:

- `empty`: the runtime alone;
- `sqlite`: `modernc.org/sqlite` and one FTS5 query, nothing of memcue;
- `store`: `internal/store` and one `Search`;
- `hook`: the recall hook without a judge (decode the hook input, open the base, `recall.Recall`, encode);
- `hookjev`: the same with `internal/rerank`, the Jev client;
- `memcue`: the release binary.

A probe's size includes the `.gopclntab` and type metadata its functions bring, so the difference between two rungs is
what that layer costs in the file, not only its `.text`. [report](scripts/binsize/report/main.go) prints the sections
of a binary and, for a build that keeps its symbols, the bytes per package.

Times are from loffe (x86_64, 8 cores) on a copy of the darwin Claude Code base (114 facts). The prompt got no fact, so
they time the start and the search, not the output. Warm: the mean of 200 runs. Cold: the mean of 10 runs, each after
`echo 3 > /proc/sys/vm/drop_caches`.

## Where the bytes are

| rung | bytes | added by the layer |
|---|---|---|
| `empty` | 1,294,496 | the runtime |
| `sqlite` | 6,639,776 | +5,345,280 |
| `store` | 6,828,192 | +188,416 |
| `hook` (words only) | 7,663,776 | +835,584: recall, text, tier, JSON, regexp, the stemmer |
| `hookjev` | 11,260,064 | +3,596,288: `net/http`, `crypto/tls`, HTTP/2 |
| `memcue` | 14,794,912 | +3,534,848: the other commands, of which 2,314,240 is `html/template` |

Sections of the release binary: `.text` 7,335,025 (49.6%), `.gopclntab` 5,384,945 (36.4%), `.go.type` 996,312
(6.7%), `.rodata` 555,897, `.noptrdata` 397,665, `.data` 101,138. In the symbol table of the unstripped build,
`modernc.org/sqlite` is 2.14 MB in 4,382 symbols (25.5% of all symbol bytes), `crypto` is 1.25 MB, `net` is 0.77 MB,
the runtime 0.54 MB, and memcue's own `main` 0.22 MB.

## Hypotheses

### 1. SQLite's true share: 5.35 MB, 36% of the file

The driver probe is 5.35 MB above `empty`, against its 2.14 MB of symbols: the transpiled C comes as thousands of small
Go functions, and each pays for its pclntab. The alternatives:

| driver | probe size | against `modernc` | price |
|---|---|---|---|
| `modernc.org/sqlite` (today), linux/amd64 | 6,639,776 | — | none: pure Go, static, cross-compiled |
| `ncruces/go-sqlite3` (wasm on wazero), linux/amd64 | 8,106,144 | +1,466,368 | larger, as expected |
| `modernc.org/sqlite`, darwin/arm64 | 6,518,082 | — | reference for the two rows below |
| `mattn/go-sqlite3`, bundled amalgamation, darwin/arm64 | 3,738,738 | −2,779,344 | cgo: a C toolchain per target, no cross-compilation from one runner |
| `mattn/go-sqlite3` with `-tags libsqlite3`, darwin/arm64 | 2,450,722 | −4,067,360 | cgo plus a system `libsqlite3` with FTS5 at run time; the binary is no longer self-contained |

The cgo rows were built natively on darwin/arm64, because cgo cross-compilation needs a C cross toolchain. The
saving is the difference against the `modernc` probe of the same platform.

**Not a go, as it stands:** the only real saving, 2.8–4.1 MB, costs the static, cgo-free, cross-compiled build that
the plugin download relies on. A simpler store than SQLite would also replace FTS5 and BM25, which is a redesign, not
a size cut.

### 2. A hook-only binary: 7.66 MB words only, 11.26 MB with Jev

A words-only hook binary is 48% of the release binary. With the Jev client it is 76%: `net/http` and `crypto/tls` cost
3.60 MB, the second largest layer after SQLite. The direction of #331 makes the words the default on the read path,
so a hook binary would need no HTTP client at all.

The price is two binaries in the plugin: two downloads in `install`, two version checks, and a hook that cannot fall
back to the judge on its own. Warm, the words-only probe starts in 4.9 ms against 8.0 ms for the release binary; cold,
both take 15 ms. Part of the warm gap is work the probe does not do (the seen-set, the log), so this is an upper bound.

### 3. Code kept alive by reflection: 2.31 MB for `html/template`

`cmd/memcue/review.go` renders the review page with `html/template`. Its executor calls `reflect.Value.MethodByName`.
The linker then marks the binary `<ReflectMethod>` (`text/template.(*state).evalField` in `go build -ldflags=-dumpdep`)
and keeps every exported method of every reachable type. A build with the template replaced by a stub (the review page
blank, nothing else changed) is **12,480,672 B, −2,314,240 (−15.6%)**, and its dependency dump has no
`<ReflectMethod>` at all. `html` and `text/template` themselves are 0.22 MB of symbols. The other 2.1 MB are methods
nobody calls.

The template is also parsed at package init on every start. In `GODEBUG=inittrace=1` on loffe, `main`'s init takes
0.55 ms, `internal/policy` 0.69 ms (regexp compilation), `internal/text` 0.27 ms and `internal/vet` 0.21 ms. Warm,
the stub build starts in 6.2 ms against 8.0 ms.

**The cheapest cut found.** One page written with `html.EscapeString` and `strings.Builder` instead of a template keeps
the review page and removes 2.3 MB and the init parse. `encoding/json` stays: the hook itself needs it, and a
hand-written marshaler was not measured.

### 4. Levers of the toolchain

| lever | bytes | against release | price |
|---|---|---|---|
| `upx --best` | 6,126,832 | −8,668,080 | warm start 65.1 ms against 8.0, cold 76.5 against 15.1 |
| `upx --lzma` | 4,823,520 | −9,971,392 | warm start 322.4 ms |
| `-gcflags=all=-l` (no inlining) | 13,967,520 | −827,392 (−5.6%) | slower code; not timed |
| `GOAMD64=v3` | 14,786,720 | −8,192 | nothing worth it |

**upx is a no-go:** the hook starts on every prompt, and decompressing costs 8–40 times the whole warm start. The loader and antivirus concerns the
issue lists were not checked; the start time alone settles it. Turning off inlining saves 0.8 MB of a speed that was not measured.

### 5. The floor

A static, cgo-free binary that opens the base and searches it cannot go below the `store` rung: **6.83 MB**, of
which 1.29 MB is the runtime and 5.35 MB the driver. The words-only hook adds 0.84 MB to that (7.66 MB). Everything
above 7.66 MB is a design choice: the HTTP client (3.60 MB), the template's method retention (2.31 MB), and the other
commands (about 1.2 MB).

## Start time is not the size

| build | bytes | warm | cold |
|---|---|---|---|
| release | 14,794,912 | 8.05 ms | 15.1 ms |
| without `html/template` | 12,480,672 | 6.24 ms | 14.5 ms |
| words-only hook probe | 7,663,776 | 4.88 ms | 15.2 ms |
| release under `upx --best` | 6,126,832 | 65.1 ms | 76.5 ms |

With a cold cache all three plain builds start in 15 ms: the kernel pages in what the process touches, not the file.
The warm differences come from init work and from what each binary does, not from the bytes. What a smaller binary
does buy is a lighter download.

## Open decisions

- **Taken:** the review page is removed entirely, with `html/template` (#325, 2026-10-24): 12,001,440 B, −2,793,472
  (−18.9%) against 14,794,912, and no `<ReflectMethod>` left in the build.
- A separate hook binary without an HTTP client: −7.1 MB for the hook, at the price of two binaries in the plugin.
  It only makes sense after #331.
- A CI budget for the stripped linux/amd64 binary. It should be set after the first decision: 12.5 MB with the
  template gone, 15 MB as things are.

## Reproducing

```sh
docs/research/scripts/binsize/ladder.sh OUT           # every rung, stripped and with symbols
go run -tags research ./docs/research/scripts/binsize/report OUT/memcue OUT/memcue.sym
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags=-dumpdep -o /dev/null ./cmd/memcue 2>&1 | grep ReflectMethod
GODEBUG=inittrace=1 memcue version 2>&1 >/dev/null | sort -k5 -rn | head
```

The template stub and the driver probes are not in the tree. The stub replaces `reviewTemplate` with a type whose
`Execute` writes nothing. The driver probes are the `sqlite` probe with the driver import and its name swapped, built
in a module of their own.

## Limitations

- One base of 114 facts and one prompt that got no fact: the times describe the start, not a large search.
- Cold runs are 10 per build; their differences under 1 ms are noise.
- The cgo rows were measured on darwin/arm64, not on the release's linux/amd64.
