# PostgreSQL research checkpoint report

**Decision: keep current SQLite/text production defaults. Continue research; issue #674 is incomplete.**

**Current qualification (2026-11-06):** [repair-cycle-1 erratum](postgres-674-erratum.md) records the confirmed output-preservation defect, its repair, and the corrected infrastructure authorization. The measurements below used the original frozen harness.

This is the first executable, local, synthetic checkpoint, run on 2026-11-06. It contains a working PostgreSQL/pgvector schema, content-only lexical/exact-vector/hybrid retrieval, a managed pooled HTTP service, wire and CLI clients, executed failure/recovery fixtures, and independent aggregate recomputation. No live database, private corpus, production deployment, or release was involved. See the [ADR](postgres-674-adr.md) for current semantics, authority choice, protocol examples and lifecycle design.

## Frozen inputs and provenance

Baseline source: `a5de0b989ceb1046f4933b19cae7ef60bed0ede2`, branch `hermes/memcue-postgres-674`; initial worktree clean. Production source, module files, plugin and CLI defaults remain unchanged. No other worktree or task's mutable artifacts were used.

The [declaration](scripts/postgres674/declaration.json) preceded measurements. The [fixture](scripts/postgres674/fixture.json) SHA256 is `2b82538726da62292706bfb055d3f449b98e9ccb8b1559a74b467261a73120a0`. It contains 38 history rows under 37 invented fact IDs, 26 held-out cases, two distinct tuning cases, and three mechanics cases. Parameters were not tuned. Labels were written with the facts, so “held-out” means separate from tuning, not blinded independent annotation. Synthetic useful targets and known harmful opposites are explicit; unlisted results count as non-useful under this strict label definition, not necessarily harmful.

The declared language groups contain 13 English-intent and 13 Russian-intent held-out cases. Several scope/lifecycle probes use English literal anchors: only four held-out query strings contain Cyrillic. This is a bilingual fixture, not a balanced multilingual evaluation. Reported per-language aggregates retain those declared intent labels; their interpretation is limited accordingly. The next corpus needs independently labeled natural queries in both languages.

Every source/input file and the executed Go bridge binary were hashed before execution. The final [manifest](scripts/postgres674/results/manifest.json) records hashes and binary identity. Private raw evidence is retained in ignored `.research-private/evidence/run-03`, including frozen driver copies, all ranked outputs, normalized terms/filters, genuine vectors, query/passage timing samples, transaction checks, source/history projections, PG logs, a dump, and stopped data directories. `recompute.py` independently regenerates [aggregates.json](scripts/postgres674/results/aggregates.json) without DB or model access.

Run 01 failed before transport measurement because a helper function shadowed Python's `http` module. Its outputs and failure record remain private. Run 02 completed after that driver repair. Run 03 repeats the completed experiment with formatted Go source and an explicit pinned-HEAD/unchanged-production-source check. Runs 02 and 03 have exactly equal retrieval ranks and scores. No quality parameter, label or fixture was changed after measurements.

Verified runtime: Go 1.27.0, Python 3.13.5, PostgreSQL 17.11 (Debian 17.11-0+deb13u1), pgvector 0.8.0, psycopg 3.2.13, ONNX Runtime 1.23.2, tokenizers 0.22.1, x86_64. [Package metadata](scripts/postgres674/packages.json) pins Debian downloads and hashes; [requirements.lock](scripts/postgres674/requirements.lock) captures exact Python versions. Actual cache/toolchain/scratch paths are in private `environment.txt`, omitted from public files. Scratch was verified outside Git discovery. Docker discovery found no socket; extracted worker-owned packages supplied a real rootless PG instead.

Model: `intfloat/multilingual-e5-small`, revision `614241f622f53c4eeff9890bdc4f31cfecc418b3`, original float32 ONNX, 384 dimensions, masked-mean pooling then L2 normalization, query/passage prefixes, max 512 tokens, CPU provider with two threads. ONNX SHA256: `ca456c06b3a9505ddfd9131408916dd79290368331e7d76bb621f1cba6bc8665`. [Model provenance](scripts/postgres674/results/embedding-provenance.json) includes all downloaded file hashes. These are genuine embeddings; hand-assigned vectors were not used for quality. Total model/tokenizer/config download: 487,352,338 bytes. No private facts were sent to a model or provider.

## Retrieval findings: synthetic only

All five arms use the same frozen history, repository normalization and request filters. Each arm/case runs twice: 310 retrieval requests total, including tuning/mechanics. Aggregates below use one repetition of the 26 held-out cases, so deterministic repetition is not treated as extra independent samples. SQLite calls the actual repository store and recall implementation. Both SQLite arms retain identical full history; only the derived index changes. All arms use result limit 10. SQLite floors are explicitly zero to compare candidate retrieval; production 0.6/0.45 floor calibration is outside this run. PostgreSQL `ts_rank` is not BM25.

Vector search is exact cosine; hybrid is equal-weight RRF, constant 60, candidate depth 100 per channel. Both constrain latest/status/expiry/scope/shelf/kind/date before limit. `AllWords` constrains all channels; ordinary queries remain OR for lexical search. No ANN was used.

| Arm | Hit@1 | Hit@5 | Hit@10 | Target recall@5 | Strict useful precision@5 | Known harmful results@1 / @5 / @10 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| sqlite-full | 0.8846 | 0.8846 | 0.8846 | 0.8269 | 0.7051 | 0 / 5 / 5 |
| sqlite-content | 0.8462 | 0.8462 | 0.8462 | 0.7885 | 0.7051 | 0 / 5 / 5 |
| pg-lexical | 0.8077 | 0.8462 | 0.8462 | 0.7885 | 0.7051 | 1 / 5 / 5 |
| pg-vector-exact | 0.9615 | 1.0000 | 1.0000 | 0.9808 | 0.3077 | 1 / 6 / 8 |
| pg-hybrid-rrf60 | 0.9231 | 1.0000 | 1.0000 | 0.9808 | 0.3077 | 1 / 6 / 8 |

Hit@k is the fraction of cases with any labeled target. Target recall divides useful targets found by all labeled targets, then averages cases. Precision divides useful hits by returned results (up to k); an empty result scores zero. Harmful counts count labeled harmful results, not distinct cases or all unrelated results. No unlabeled overlap metric is called recall.

Removing auxiliary fields loses one English legacy-translation target: full SQLite hit@5 is 23/26, content-only SQLite 22/26. PG lexical stays at 22/26 at five results, but its changed ranking loses one top-one hit relative to content-only SQLite (21 versus 22). Exact vectors recover the four lexical-missing queries by five results, including legacy-language and paraphrase cases. RRF reaches the same hit@5 as vectors while ranking one fewer target first (24/26 versus 25/26). These differences come from a tiny authored fixture.

Vector and hybrid return more distractors because they fill a result limit without a calibrated semantic floor. At ten, each has eight known harmful results and strict useful precision 0.1962; lexical arms have five harmful results. A perfect target hit@5 therefore does not imply safe final answers or a production retrieval improvement.

All arms pass the declared synthetic hit@5 nonregression threshold and the four identifier/number top-one probes. Zero source changes and zero filter leaks meet integrity thresholds. The production gate remains **UNVERIFIED**, irrespective of synthetic thresholds.

Per-stratum hit@5 (all at 1/5/10, recall, precision, harmful counts, and intent-language groups are available in the aggregate JSON):

| Held-out stratum | Cases | Full SQLite | Content SQLite | PG lexical | PG exact vector | PG hybrid |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| and | 1 | 1.0000 | 1.0000 | 1.0000 | 1.0000 | 1.0000 |
| date | 1 | 1.0000 | 1.0000 | 1.0000 | 1.0000 | 1.0000 |
| expiry | 2 | 1.0000 | 1.0000 | 1.0000 | 1.0000 | 1.0000 |
| history | 2 | 1.0000 | 1.0000 | 1.0000 | 1.0000 | 1.0000 |
| identifier | 2 | 1.0000 | 1.0000 | 1.0000 | 1.0000 | 1.0000 |
| kind | 1 | 1.0000 | 1.0000 | 1.0000 | 1.0000 | 1.0000 |
| legacy-language | 2 | 1.0000 | 0.5000 | 0.5000 | 1.0000 | 1.0000 |
| negation | 2 | 1.0000 | 1.0000 | 1.0000 | 1.0000 | 1.0000 |
| number | 2 | 1.0000 | 1.0000 | 1.0000 | 1.0000 | 1.0000 |
| or | 1 | 1.0000 | 1.0000 | 1.0000 | 1.0000 | 1.0000 |
| paraphrase | 4 | 0.2500 | 0.2500 | 0.2500 | 1.0000 | 1.0000 |
| retired | 2 | 1.0000 | 1.0000 | 1.0000 | 1.0000 | 1.0000 |
| scope | 2 | 1.0000 | 1.0000 | 1.0000 | 1.0000 | 1.0000 |
| shelf-excluded | 1 | 1.0000 | 1.0000 | 1.0000 | 1.0000 | 1.0000 |
| shelf-included | 1 | 1.0000 | 1.0000 | 1.0000 | 1.0000 | 1.0000 |

## Actual transport and embedding measurements

These are real local roundtrips: PostgreSQL TCP/SCRAM wire versus loopback HTTP/JSON to a persistent service with a one-to-two-connection pool. Warm sessions retain connections and include the repository-normalizer subprocess. Cold samples start the research Python CLI subprocess and include process startup, imports, JSON serialization, normalization, connection establishment, and search. Each route has 30 warm and 10 cold samples, executed serially; only the mutation race uses concurrency two. Every transport sample returned identical ranks/scores.

| Route / phase | Samples | End-to-end p50 / p95 / p99 ms | SQL p50 / p95 ms |
| --- | ---: | ---: | ---: |
| wire-warm-session | 30 | 3.19 / 3.92 / 6.10 | 0.43 / 0.92 |
| http-warm-session | 30 | 4.39 / 5.02 / 6.75 | 0.55 / 1.16 |
| wire-cold-cli | 10 | 164.53 / 185.14 / 185.14 | 2.65 / 2.82 |
| http-cold-cli | 10 | 47.71 / 51.73 / 51.73 | 0.50 / 0.62 |

Quantiles use the predeclared nearest-rank rule. With ten cold samples p95/p99 equal the maximum and cannot characterize a reliable tail. The local latency thresholds (warm p95 <100 ms, cold <250 ms) pass. HTTP's smaller cold invocation time reflects this Python HTTP client's lighter startup and the service's pooled DB connection. It does not isolate protocol encoding efficiency or establish a universal API advantage. A Go direct client or different invocation architecture could change that result.

Transport arms are lexical: embedding time is exactly zero **by design for those requests**, not an estimate of semantic-search overhead. SQL timings are adapter wall time around execute/fetch, not PostgreSQL internal executor-only time. Serialization and connection overhead remain included in end-to-end residuals rather than independently instrumented components. This research CLI is not the production Hermes plugin/tool path; plugin invocation, second-search behavior, actual agent reformulation, user RTT, Tailscale direct/relay routing and outage behavior are all unmeasured.

| Embedding component | Samples | p50 / p95 / p99 ms |
| --- | ---: | ---: |
| passage, one genuine text per call | 37 | 6.91 / 8.11 / 8.12 |
| query, one genuine text per call | 31 | 5.55 / 9.03 / 9.45 |

Model initialization took 2461.51 ms once, separately from embedding samples. The query embedding p95 threshold (<500 ms) passes on these short synthetic inputs after model load. There is one embedding per case/text, not 31 repetitions of a single query; no cache-hit economics or long-input distribution was tested. Passage inference samples sum to 261.83 ms. This is not a measured large-corpus backfill throughput.

## Integrity, failure and recovery evidence

Independent recomputation validates both repetitions, all expected arm/case counts, every returned version and eligibility filter, source/history equality, and field-isolation controls. Content-only SQLite and PG lexical return no result for keyword/query/metadata marker tokens; full-index positive controls find the auxiliary markers. Content-only vector hashes cover primary content only. No source-history rows were rewritten or removed.

Twenty-three assertions passed in one bounded failure run:

- Identical durable-key retry replays the reply; changed payload rejects with 409.
- A real HTTP mutation commits while its response remains unread; closing the socket and retrying produces one history row.
- Two DB sessions race correction and retirement against the same expected version: exactly one commits and the other reports conflict; history contains two versions.
- Old-version and wrong-hash vector completions reject; current completion publishes; retirement removes the vector and prevents stale completion.
- API authorization excludes other projects/hosts before limiting, hides unauthorized history, rejects wrong tokens and server-host substitution, and reads preserved history versions.
- A clean PG restart preserves all committed history and durable replies; an identical retry after restart returns the original reply.
- Real `pg_dump -Fc` and `pg_restore --exit-on-error` roundtrip facts, operations, lexical/index/vector tables and stamps exactly.
- Stopped PostgreSQL produces an explicit direct connection failure.

These are deterministic protocol fixtures, not agent behavior. The unread-response fixture establishes application-level retry after an uncertain reply, not packet-loss or tailnet failure. Restart is orderly, not power-loss/WAL crash recovery. The outage fixture tests direct DB failure, not a live API pool recovering during a network outage. Backup comparison checks table contents. A separately declared post-run audit restored the same saved dump again, verified 155 assertions, and performed a successful new write through the restored identity sequence. It independently checked every frozen history row, primary-content stems, stored vector content hashes/model/dimensions/payloads, and marker absence. It also exercised a real SQL statement timeout and connection recovery after rollback. This audit used the disposable owner role; restore under a future least-privilege production role remains unverified. The [audit summary](scripts/postgres674/results/audit-summary.json) records its source and dump hashes.

All owned PostgreSQL starts ended with exit 0, no postmaster PID file remains, and no service/container was registered. Data directories and the 83,504-byte dump remain in private evidence for review; they are stopped snapshots, not a deployed service. The [lifecycle record](scripts/postgres674/results/lifecycle.json) records starts, stops and restore.

## Resources, costs and scale limits

| Item | Measured value / limit |
| --- | --- |
| Worker budget | 2 CPU, 8 GiB RAM, 512 MiB swap; at most two executor/pool workers |
| Heavy command scheduling | Individual `build-check.sh` wrappers; no nested wrapper |
| Driver peak RSS | 1,204,428 KiB (model + orchestrator; excludes PG and child-process totals) |
| Driver CPU user / system | 3.316 / 0.883 seconds; excludes children/PG |
| SQLite full / content DB files | 57,344 / 57,344 bytes |
| PG research DB | 8,345,267 bytes including PostgreSQL catalog overhead |
| PG facts / fact_index / fact_fts / fact_vectors | 49,152 / 65,536 / 57,344 / 122,880 bytes, including their indexes |
| Shared buffers / per-sort work memory | 32 MiB / 4 MiB; no JIT, no query parallelism |
| Total stand peak RAM, network traffic, scale crossover | unknown |
| Provider token usage / billing / operating budget | null / null / unknown |

There were no provider API calls. The declared provider spend ceiling was zero; unknown cost/usage fields remain null, including local infrastructure operating cost. Download, model-load and tiny-corpus DB size are not a monthly cost estimate. Private current-corpus, controlled larger-corpus, sustained query/write throughput, filtered ANN, index-growth, concurrent embedding, monitoring overhead, network traffic and scale crossover measurements are pending. No scale or operational-cost conclusion is supported by 37 facts.

## Verification and next executable work

Executed checks and exits:

| Check | Exit / outcome |
| --- | --- |
| `build-check.sh go build -p 2 -o .research-private/sqlite_bridge ./docs/research/scripts/postgres674/sqlite_bridge.go` | 0 |
| `build-check.sh python3 docs/research/scripts/postgres674/experiments.py --pgroot <owned-cache>/pgroot --model-cache <owned-cache>/e5-small --bridge .research-private/sqlite_bridge --out .research-private/evidence/run-03` | 0; 310 retrieval requests, 80 transport samples, 23 failure/recovery assertions |
| `python3 docs/research/scripts/postgres674/recompute.py .research-private/evidence/run-03 --output .research-private/evidence/run-03/aggregates.json` | 0; independently reconstructed aggregates and invariants |
| `build-check.sh python3 docs/research/scripts/postgres674/audit.py --pgroot <owned-cache>/pgroot --evidence .research-private/evidence/run-03 --bridge .research-private/sqlite_bridge --out .research-private/evidence/audit-03` | 0; 155 separately declared integrity/restore/deadline assertions |
| `python3 -m unittest discover -s docs/research/scripts/postgres674 -p 'test_*.py' -v` | 0; five deterministic tests |
| `python3 -m py_compile docs/research/scripts/postgres674/*.py` | 0 |
| `build-check.sh go test -p 2 -parallel 2 ./...` | 0; all repository packages passed |

The [stand README](scripts/postgres674/README.md) records the environment-clearing, private scratch and dependency commands. Private `checks.json` records executed worker argv and exits; raw PG logs and source copies are private. The initial author check reported no proven S1/S2 defect within this disposable fixture scope. Subsequent independent review confirmed R1, an output-overwrite defect; see the [erratum](postgres-674-erratum.md). The structured review-report tool was unavailable; this is not an independent approval. Public artifacts passed a hostname/path/secret-pattern scan, local Markdown links exist, prose scanning found no signals, and `git diff --check` passed. Parent independent review and acceptance remain pending.

Next executable seam: supply an authorized immutable private export and independently labeled English/Russian queries, plus the approved model/tool-loop route and a parent-provisioned private endpoint reachable from the intended client. Reuse the five-arm driver through an explicit corpus adapter, add the actual Hermes invocation and bounded same-model/same-budget loop, then measure direct/relay RTT and full client latency. Keep deterministic protocol retries separate from agent reformulations. Before moving to a service authority, implement durable embedding jobs, all-or-nothing batch/dream behavior, least-privilege DB roles, deadline/cancellation propagation, model/database/tailnet outages, and least-privilege writable restore/crash recovery checks. Do not use the fixture owner role or this loopback stand as deployment configuration.

There is no missing local PG or model prerequisite for this checkpoint. The remaining gates require an authorized private corpus, model-driven tool-loop setup, intended-client endpoint verification, and independent acceptance evidence. The owner has authorized a separate private research PostgreSQL deployment on a private server over Tailscale, handled by a separate infrastructure owner. Live-memory migration, production cutover and live-data movement remain unauthorized. This checkpoint does not verify the separate deployment or its intended-client endpoint; see the [current qualification](postgres-674-erratum.md).
