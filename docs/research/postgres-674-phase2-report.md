# Immutable private replay and deployed PostgreSQL measurements

**Keep SQLite/text production defaults. Issue #674 remains open.** This checkpoint executes the frozen private-copy replay and deployed efficiency work. It provides no natural-task relevance labels, actual agent outcomes or production migration verdict. The [synthetic report](postgres-674-report.md), [ADR](postgres-674-adr.md), [repair erratum](postgres-674-erratum.md) and their measured packets remain unchanged.

## Inputs, identity and method

The immutable copy contains 7,713 fact files and 1,096 historical search records. Native `store.ReadFactFile` produces 33,371 unique version rows from 33,372 lines, under 7,713 IDs. One identical version is collapsed; conflicting versions would fail. Selecting the maximum version before status/expiry yields 3,462 latest active facts, including 19 expired on the fixed date. Every primary string and remaining metadata field roundtrips through SQLite and the candidate history. Original files and their hashes remain intact. The original snapshot consists of two complete identical source reads; it is not an atomic historical database snapshot.

The [declaration](scripts/postgres674/phase2-declaration.json) precedes arm execution. The immutable manifest SHA256 is `af40dad282183aafc6305651638284874283a6186881254586442d9f9526b626`. Evaluated production/module revision is `a5de0b989ceb1046f4933b19cae7ef60bed0ede2`; harness HEAD is `5ad8da71a478a97b87b3d27c5dfbf5ec7b30743d` plus the recorded research delta. Production/module bytes are verified against the evaluated revision, separately from harness HEAD/file hashes. A normal later research checkout passes; changed/dirty production and an old or mismatched native bridge fail. Bounded tests also commit documentation changes in an isolated fixture checkout. The separately built bridge records its production digest, source hash, binary hash and Go build info. No branch integration was needed or performed.

The deterministic sample contains 128 distinct normalized contexts: 64 Latin without Cyrillic, 63 containing Cyrillic and one other. Selection uses stable hashes within each stratum. It takes query text, the logged project key and explicit caller host, falling back to the immutable manifest host when absent. All arms use the same native `Query.Filter` output, UTC calendar date 2026-11-06, OR, limit 10 and zero SQLite score floor. Unnamed shelves remain excluded. Historical flags and real-work provenance are unknown. These are new controlled replays of logged contexts, not reconstructed historical sessions or reused scientific labels from #673.

Two separate SQLite databases call native `recall.Search`. Both preserve the full history; only the content candidate's derived index loses keywords/queries and their legacy translations. The PostgreSQL candidate omits those fields from its history schema and indexes primary content only. It preserves native terms/prefixes/numeric normalization, uses GIN `simple` lexemes with `ts_rank`, exact cosine vectors and equal-weight RRF60 over 100 candidates per channel. PG timestamp tie ordering parses the preserved timestamp; it does not rely on variable-precision text order. No old BM25 floor is applied to PG scores. The five original logical tables are present; imported text has no maintenance stamps to transfer, so this read-backend packet's stamps table is empty. Transaction/idempotency research remains in the separate synthetic stand.

The local cached model is unchanged: multilingual E5-small revision `614241f622f53c4eeff9890bdc4f31cfecc418b3`, original float32 ONNX, 384 dimensions, masked mean/L2, 512 tokens, `passage: ` and `query: ` prefixes. Only primary fact content enters passage inference. Every model file is authenticated against its retained hash. All 3,462 latest active facts receive vectors; expiry remains a read filter. No provider call or new model route is used.

The endpoint is authenticated PostgreSQL 18.6 with preinstalled public vector 0.8.7. The app creates new, absent table/index prefixes inside the existing research schema. It does not attempt extension/database/schema creation, use admin access, alter existing tables, or read operator sentinel rows. Password discovery uses libpq's private password file; no secret enters argv, a DSN or evidence. This client runs in the server worker and addresses the private tailnet endpoint. These results are not remote-user network latency.

## Unlabeled retrieval changes

[Aggregate records](scripts/postgres674/results/phase2-aggregates.json) contain results at 1/5/10 and every declared language category. Each arm's representative row uses one repetition; repetitions do not increase the number of independent contexts. Overlap below is intersection count divided by the reference arm's returned count, summed across contexts. It is neither Jaccard similarity nor relevance recall.

| Arm | Empty contexts | Returned facts | Primary characters | Overlap@10 with full SQLite | Overlap@10 with content SQLite |
| --- | ---: | ---: | ---: | ---: | ---: |
| sqlite-full | 5 | 1222 | 309528 | 1.0000 | 0.6326 |
| sqlite-content | 6 | 1214 | 281218 | 0.6285 | 1.0000 |
| pg-lexical | 6 | 1214 | 417210 | 0.5368 | 0.5980 |
| pg-vector-exact | 0 | 1280 | 341698 | 0.3052 | 0.2792 |
| pg-hybrid-rrf60 | 0 | 1280 | 390385 | 0.4787 | 0.4901 |

Removing auxiliary fields changes ordering substantially: only 5/128 complete ranked lists remain identical to full SQLite. Changing the content-only ranker to PG lexical leaves 6/128 ranked lists identical to content SQLite. Vector/hybrid return ten facts for every context, including contexts where lexical search is empty. These counts establish changed context, not recovered useful facts or improved answers.

Overlap@10 with full SQLite by script category:

| Arm | Latin (64) | Cyrillic (63) | Other (1) |
| --- | ---: | ---: | ---: |
| sqlite-full | 1.0000 | 1.0000 | undefined: empty reference |
| sqlite-content | 0.6571 | 0.6016 | undefined: empty reference |
| pg-lexical | 0.5473 | 0.5270 | undefined: empty reference |
| pg-vector-exact | 0.2939 | 0.3159 | undefined: empty reference |
| pg-hybrid-rrf60 | 0.4848 | 0.4730 | undefined: empty reference |

A separate first-16-context native-floor diagnostic keeps 111 full-index and 106 content-index results, versus 150 each at zero floor. This isolates an additional floor/message-coverage effect; it does not calibrate a semantic floor. Case-level differences, primary character counts and native filters stay private. No useful precision, natural recall, harmful-match rate, semantic-language verdict or final-answer gain can be computed from these unlabeled records.

## Latency, embedding and concurrency

All quantiles use nearest rank. SQLite library samples number 256 per arm. PG warm samples number 256 per arm/concurrency, with one retained session per worker chunk. The SQL wall interval includes adapter execute/fetch and hybrid fusion, and excludes embedding, connection setup and native normalization. It is not internal executor time.

| Arm | Concurrency | Library/SQL p50 / p95 / p99 ms |
| --- | ---: | ---: |
| sqlite-full | 1 | 10.20 / 20.21 / 24.27 |
| sqlite-content | 1 | 7.23 / 15.32 / 18.49 |
| pg-lexical | 1 | 9.96 / 24.15 / 30.42 |
| pg-lexical | 2 | 10.41 / 25.72 / 34.49 |
| pg-vector-exact | 1 | 43.81 / 62.91 / 83.93 |
| pg-vector-exact | 2 | 45.58 / 56.27 / 68.99 |
| pg-hybrid-rrf60 | 1 | 55.65 / 73.69 / 91.36 |
| pg-hybrid-rrf60 | 2 | 57.20 / 81.50 / 93.59 |

The completed main packet retains its original 86 replay-CLI samples using frozen normalization. A separately declared 86-sample supplement performs fresh native normalization, process startup/imports, cached query-vector file reading or actual model load/inference, connection setup, search, primary-context materialization and JSON serialization. Every supplemental rank/version and primary string matches the audited packet. Both paths use the same Python research wrapper; SQLite also starts the native bridge. They do not measure the production Go command or Hermes plugin. [Supplement records](scripts/postgres674/results/phase2-supplement.json) retain both cache-component and full invocation summaries.

Fresh-normalization research CLI, eight invocations per arm/concurrency, with an existing query-vector cache for vector arms:

| Arm | Concurrency | p50 / p95 / p99 ms |
| --- | ---: | ---: |
| sqlite-full | 1 | 204.99 / 225.65 / 225.65 |
| sqlite-full | 2 | 214.92 / 238.50 / 238.50 |
| sqlite-content | 1 | 200.70 / 208.88 / 208.88 |
| sqlite-content | 2 | 223.23 / 263.68 / 263.68 |
| pg-lexical | 1 | 211.63 / 223.36 / 223.36 |
| pg-lexical | 2 | 231.83 / 244.08 / 244.08 |
| pg-vector-exact | 1 | 265.89 / 289.85 / 289.85 |
| pg-vector-exact | 2 | 280.96 / 294.06 / 294.06 |
| pg-hybrid-rrf60 | 1 | 279.45 / 298.45 / 298.45 |
| pg-hybrid-rrf60 | 2 | 297.17 / 312.88 / 312.88 |

Three fresh model-process invocations per vector arm give the following full CLI times. The OS page cache is retained; this is not disk-cold or server-cold timing. With three/eight samples p95/p99 are the maximum, so these are smoke-scale tails.

| Arm | Cold-model CLI p50 / p95 / p99 ms |
| --- | ---: |
| pg-vector-exact | 2889.68 / 2918.88 / 2918.88 |
| pg-hybrid-rrf60 | 2919.76 / 3202.04 / 3202.04 |

| Embedding component | Samples | p50 / p95 / p99 ms |
| --- | ---: | ---: |
| passage | 3462 | 19.79 / 44.19 / 58.18 |
| query | 128 | 6.89 / 9.69 / 10.89 |
| query-cache | 256 | 0.001860 / 0.002651 / 0.004579 |

Model authentication/load took 2,477.65 ms. Passage backfill took 78.61 seconds wall and 156.58 seconds client user CPU; one identical primary content hit the content-hash cache. One passage reached the 512-token limit. The warm query encoding stage took 0.94 seconds. In-process query-cache samples include hash lookup; the CLI cache additionally pays JSON file reading/deserialization. Fresh CLI component summaries report that cost separately. Serialized response byte totals are saved; actual network bytes remain unknown.

A separate, predeclared 160-sample transport probe measured SQL `SELECT 1` roundtrips and new libpq sessions. At concurrency one, warm SQL p50/p95/p99 is 0.076/0.173/0.400 ms; connection establishment is 7.89/11.34/11.34 ms over 16 samples. Concurrency two and all separate connection/query intervals are in the supplement. A SQL roundtrip is not a network-only RTT measurement. Remote-client direct/relay routing, intended-device latency and the deployed HTTP/API alternative remain unmeasured.

PG warm throughput, including each workload's session setup:

| Arm | Requests/s, concurrency 1 | Requests/s, concurrency 2 |
| --- | ---: | ---: |
| pg-lexical | 85.88 | 157.23 |
| pg-vector-exact | 21.89 | 42.83 |
| pg-hybrid-rrf60 | 17.79 | 33.80 |

These short read workloads are not sustained load or write-concurrency tests. Imports use one writer. Server CPU/process RSS, total deployment RAM, network traffic and monthly operating cost remain unknown. The app role cannot read backend memory contexts (SQLSTATE 42501); no elevated permission was requested. Driver peak RSS is 1,284,196 KiB, including model/orchestrator and hashing buffers, excluding the PG server. Saved per-stage CPU records distinguish client and child CPU; RSS values are cumulative high-water marks, not independent engine footprints. Worker bounds remain two CPU workers, 8 GiB RAM and 512 MiB swap.

## Controlled scale and storage

The performance-only table has 10,000 then 30,000 deterministic replicas of latest active primary rows and genuine saved vectors, with new identities. It supplies no new corpus diversity, semantic evidence or task labels. This denormalized table omits the primary history join, so its timing cannot be extrapolated from or divided by the history-backed current-corpus timing. Eight fixed contexts run three repetitions per arm/concurrency.

| Replica rows | Arm | Concurrency | SQL p50 / p95 / p99 ms |
| --- | --- | ---: | ---: |
| 10000 | pg-lexical | 1 | 7.82 / 10.98 / 11.05 |
| 10000 | pg-lexical | 2 | 9.70 / 12.17 / 12.56 |
| 10000 | pg-vector-exact | 1 | 24.20 / 30.15 / 31.54 |
| 10000 | pg-vector-exact | 2 | 26.42 / 34.72 / 40.00 |
| 30000 | pg-lexical | 1 | 26.85 / 46.58 / 48.29 |
| 30000 | pg-lexical | 2 | 25.87 / 38.89 / 39.79 |
| 30000 | pg-vector-exact | 1 | 75.54 / 101.17 / 108.79 |
| 30000 | pg-vector-exact | 2 | 77.06 / 97.15 / 117.91 |

The 30k exact-vector p95 at concurrency one slightly exceeds the predeclared 100 ms investigation threshold. A bounded ANN study is justified as the next performance test, with a matched schema, selective filters and exact top-k oracle. ANN remains **not measured** here; no approximate recall or underfill claim follows from these results.

| Storage snapshot | Bytes |
| --- | ---: |
| sqlite-full, complete DB file | 57901056 |
| sqlite-full, allocated FTS pages | 847872 |
| sqlite-content, complete DB file | 57901056 |
| sqlite-content, allocated FTS pages | 397312 |
| PG current facts, including indexes/TOAST | 20660224 |
| PG current fact_index, including indexes/TOAST | 1499136 |
| PG current fact_fts, including indexes/TOAST | 3473408 |
| PG current fact_vectors, including indexes/TOAST | 7684096 |
| PG current stamps, including indexes/TOAST | 16384 |
| Performance-only 10000 rows, total / indexes | 31170560 / 5758976 |
| Performance-only 30000 rows, total / indexes | 83320832 / 7004160 |

Both SQLite copies retain auxiliary history; the content candidate is not vacuumed and retains free pages. Equal file bytes therefore do not establish equal index occupancy or projected schema savings. PG omits auxiliary fields from history, so a SQLite-to-PG total-size ratio would mix schema and backend effects. All PG relation/index snapshots are saved; autonomous maintenance changes pending-list/page allocation between snapshots. Table history and vector identities remain unchanged. Model/tokenizer/config bytes total 487,352,338, separate from database storage. Provider calls are zero; provider billing is null and local operating cost is unknown.

## Verification, failures and provenance

The separate saved-record implementation calls no model or PG server. It independently parses original JSONL, verifies every original file hash, compares native full histories and all remaining PG fields, checks 20,985 returned hits for exact latest/filter eligibility, and compares 512 vector repetitions against independently computed filtered cosine scores (tolerance 3e-6). It recomputes every reported count, overlap, quantile and throughput from records. Baseline repetitions match. The earlier attempt's 2,048 SQLite/PG rank-and-score rows equal the completed run. An additional 86 fresh-normalization CLI samples match those ranks and primary text exactly. These are separate implementations by the author, not an independent source-review verdict or independent semantic annotation.

Two failed packets and their namespaces remain intact: the first loaded history/vectors before the optional memory-view privilege failure; the second completed retrieval/CLI measurements before `generate_series` rejected ambiguous smallint bounds. The loader now casts those bounds to integer. The completed run uses a third, initially absent namespace and finishes in 197.56 seconds. No table was dropped or truncated. Every new relation remains app-owned; metadata identities of pre-existing relations are unchanged. Read-only statistics show no updates/deletes in the completed namespace. Operator-owned sentinel rows were neither read nor written.

The first offline audit assumed the native SQLite index stored a version column. It failed before publishing aggregates. The corrected auditor joins native history at the maximum version, passes the complete audit, and preserves the failed attempt. Measured driver copies, binary, declarations, raw results, every executed helper (including inline `sys.orig_argv`) and original source hashes are retained privately. The fresh-normalization supplement has its own later source hash/declaration; the original packet is not rewritten. Parent-owned mutable checkpoint bookkeeping is excluded from frozen-evidence equality and recorded as an exception.

Executed worker checks use one `build-check.sh` wrapper each, never nested: exact golangci-lint 2.13.2 reproduces the two original deferred-close errcheck findings and passes after narrow handling fixes; `go test -p 2 -parallel 2 ./docs/research/scripts/postgres674` passes; all 23 Python behavioral/regression tests pass; complete private replay, offline audit, storage/transport supplement and fresh-normalization CLI/audit exit zero. Exact argv, exits and failure logs remain in the private checkpoint. Prior production/module paths are byte-identical; the accepted full Go gate is retained, not claimed as a new run. Output collision tests archive and verify their owned fixture bytes/modes/links before cleanup; valid SQLite databases close safely. No unpacked invalid-header fixture remains in the private worktree.

The [README](scripts/postgres674/README.md) gives current commands and output rules. Previous independent reviews cover their exact historical blobs only. All new source remains uncommitted for parent review; PR/CI publication and acceptance belong to the parent.

## Actual agent gate and next executable work

The installed native Codex app-server 0.166.0 supports [dynamic tool callbacks](https://developers.openai.com/codex/app-server). Two supervised, credential-free local request captures retain the exact wire tool offering and injected context before any provider inference. They preserve the configured `gpt-6.1-sol` model and high effort. The synthetic memory tool appears inside code mode, alongside file-changing/image-reading/resource and collaboration capabilities, plus global skill/environment instructions, despite disable flags. Configuration alone therefore does not prove isolation. Both processes stopped; actual inference calls and actual agent search/retry/answer outcomes are zero/unmeasured. An initial private route interpretation missed the nested tool declaration; the corrected wire-based qualification is retained alongside it.

The precise next agent step is to restrict that native runtime's effective capabilities/context and recapture the outgoing request. Then freeze one synthetic scenario per arm, the same inherited model/effort, one turn, at most three real search calls, 60 seconds and a 4,096-output-token ceiling. The controller must execute real isolated search, return only its result, and let the model choose queries, retries and answer. The installed turn schema has no output-token-limit field; enforcing that ceiling also needs a verified native route. No hand-written retry, OAuth copying, private-corpus transmission or large A/B is substituted for this gate.

Independent natural-task labels and final-answer evaluation remain required. A matched-schema filtered ANN study, actual Hermes tool path, intended remote-client latency, deployed outage/concurrency/recovery qualification and an operating budget remain separate executable gates. This client checkpoint does not claim the sibling infrastructure owner's backup/restore evidence as its own. Retain text history as authority and PG as an isolated replaceable research read backend until those gates support a different decision.
