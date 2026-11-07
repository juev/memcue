# PostgreSQL research decision: first executable checkpoint

Status: **keep SQLite for production; continue isolated evaluation**. This checkpoint implements and runs a synthetic local stand. It does not complete issue #674 or authorize migration. The source baseline is `a5de0b989ceb1046f4933b19cae7ef60bed0ede2`. The owner authorized research on 2026-11-06.

## Current behavior and authority

`internal/store/store.go` defines schema version 7. Every fact version has `(id, version)` identity. Corrections and retirement append history. The original SQLite-only base remains supported by `cmd/memcue/base.go`.

`internal/store/textstore.go`, `OpenText`, reconciles Git-synchronized text history with an external SQLite cache in both directions. Text changes enter the cache; cache versions missing from text are recovered into files. Replacing that cache does not replace the history, Git merge, export, or recovery system. Historical collisions must survive import and require the existing merge policy; a PostgreSQL primary-key conflict is insufficient evidence to discard a version.

`reindex` currently indexes content, keywords, and queries. Query entries beginning `= ` carry legacy translations (`internal/extract/translate.go`). Removing auxiliary fields therefore removes both expansion terms and those translations. The candidate adds no hidden translations or query expansions.

`internal/text/text.go` supplies English/Russian stems, Russian prefix matching, ticket terms, and exact version/numeric terms. `SearchFiltered` uses OR unless `AllTerms` is true and filters before its limit. `recall.Query.Filter` defines caller project/host defaults, origin matching, kinds, dates, expiry, and shelf exclusion. `all` still excludes shelves unless named; `all,shelf:research` explicitly adds that shelf. Legacy candidate/provisional reviews do not independently exclude active facts. `recall.Search` adds contextual tie ordering, message coverage, and relative BM25 floors (normally 0.6, message 0.45). `Store.Open` empties the old vector table: vector retrieval is new work.

The stand calls the actual repository normalization and filter code through `sqlite_bridge.go`. SQLite arms call `recall.Search` against real repository SQLite stores. It sets `MinScore=0` in every evaluated case to isolate candidate retrieval and ranking. This does not evaluate the production floor policy. PG uses `to_tsvector('simple', normalized_content)` and explicit `|`/`&` tsquery construction, including Russian `:*` prefixes. `ts_rank` is a different ranker from FTS5 BM25. No BM25-relative score floor is transferred to vector, PG lexical, or hybrid scores.

## Authority choice

Continue with **retained text history as authority, PostgreSQL as a replaceable read backend**. Initially only an explicit import/export boundary may feed PG; production clients continue writing through the existing text/cache path. Recovery can rebuild PG from a verified immutable export. Git synchronization, portable exports, rollback to the SQLite cache, and offline local search remain possible.

An authoritative shared PG service would provide centralized transactions and one write order, but it would replace the synchronization and offline-write contract. Its adoption requires a separate cutover design: freeze writers, export every history row, retain collisions privately, verify counts and hashes, back up and restore, switch one writer authority, then verify clients. Rollback must stop service writes and export committed versions before returning to text. Two independently writable authorities are rejected.

The stand's writable API and operations table are **transaction research fixtures**, separate from its frozen retrieval database. They test a possible future authority boundary; they do not implement synchronization with text or permission to use both writers.

## Logical schema mapping

The executable mapping is [schema.sql](scripts/postgres674/schema.sql).

| SQLite structure | Research PostgreSQL representation |
| --- | --- |
| `facts` | Append-only `(id text, version integer)` primary key. Content, origin, created, status, author, expires, kind, scope, source, evidence, review, prompt_version, channel preserved. Only keywords and queries omitted. |
| created / expires | Original UTC RFC3339 text / calendar date text retained for lossless export. Request `Since` is normalized to UTC by repository code; expiry includes its last calendar day. Production import must validate historical timestamp forms before relying on lexical ordering. |
| `fact_index` | Identity `rid`, unique fact ID, latest active version, normalized content stems, numeric-normalizer version. Retirement removes derived entries; expiry is request-time. |
| `fact_fts` | Ordinary table containing a `tsvector`, linked to `fact_index`; GIN index replaces SQLite's external-content FTS5 virtual table. |
| `fact_vectors` | `(id, tag)` key, expected history version, dimension, SHA256 of content, `vector(384)`. Exact cosine scan, no ANN index. |
| `stamps` | Named maintenance values; frozen source snapshot hash in this stand. |
| new `operations` | Principal/key, canonical request hash and durable response; commit together with history and derived index changes. |

PostgreSQL transactions atomically append a history row, invalidate the previous lexical/vector entries, publish the new lexical entry, and record the reply. Vectors may complete later. The fixture uses an application append-only path; it has not installed database roles/triggers that prohibit arbitrary history updates. Its disposable owner role is not a production least-privilege configuration.

## Protocol decision and examples

Prefer testing a thin persistent **versioned HTTP/JSON service with a pool** as the future shared service boundary. It can centralize application authorization, schema changes, idempotency and embedding jobs. Local measured overhead is reported separately in [the checkpoint report](postgres-674-report.md); local latency cannot decide the remote protocol winner. Direct wire remains a measured local alternative. A restricted role/RLS or equivalent stored-procedure boundary is required before direct access becomes an authorized user route.

The stand executes POST `/v1/search`, `/v1/get`, `/v1/history`, and `/v1/mutate` with `add`, `correct`, `retire`, and `restore`. It uses an ephemeral bearer identity, loopback binding, a pool of one to two DB connections, 64 KiB request bodies, result limits of 1–100, and statement timeouts up to 3000 ms. Authentication binds the synthetic caller to project `demo` and host `client-a`, with an explicit scope allowlist. Every API read and write is scoped. The server's hostname never fills in for the caller.

Example search (JSON field spelling matches the Go bridge):

```json
{"caller":{"project":["demo"],"host":"client-a"},"deadline_ms":3000,"query":{"Text":"TASK-4821","N":10,"MinScore":0,"Project":["demo"],"Host":"client-a","Today":"2026-11-06","In":["project","global"]}}
```

Example response, with illustrative timings:

```json
{"hits":[{"id":"ticket-a","version":1,"score":0.1}],"sql_ms":1.2,"embed_ms":0.0}
```

Get/history use `{"caller":{"project":["demo"],"host":"client-a"},"id":"ticket-a"}` and return `{"facts":[...]}` with all preserved fields. Get returns the latest version, including retirement; history returns permitted versions in ascending order. An absent or unauthorized ID returns `404 not_found`.

Mutations require `Idempotency-Key`. The current fixture's add payload contains the exact remaining `facts` fields, with a synthetic ID and version 1. A production service should assign ULIDs and write timestamps itself; the fixture accepts authenticated caller-supplied UTC `now` to make recovery assertions deterministic. It does not establish production clock authority.

```json
{"op":"correct","id":"ticket-a","expected_version":1,"content":"TASK-4821 requires the cobalt adapter and an audit.","caller":{"project":["demo"],"host":"client-a"},"now":"2026-11-06T12:00:00Z"}
```

```json
{"op":"retire","id":"ticket-a","expected_version":2,"caller":{"project":["demo"],"host":"client-a"},"now":"2026-11-06T12:00:01Z"}
```

Restore uses the same shape with `op: restore` and the retired version. It appends an active version. Correcting a retired fact is refused; changing content clears the old channel label, matching the existing correction lifecycle. Identical requests replay the committed reply. Reusing a key with any changed payload returns `409 idempotency_payload_mismatch`. Concurrent mutation of the same expected version yields one success and `409 version_conflict` for the loser. Operations are serialized by transaction advisory locks, then checked against durable rows; hash collisions can serialize unrelated work but do not grant access or replace payload validation.

Errors have `{"error":{"code":"version_conflict","retryable":false}}` shape. The prototype implements authentication, identity, validation, payload, version, and unavailable errors. Unknown endpoint/version returns 404. A full production error registry and compatibility negotiation beyond the `/v1/` path remain proposed.

Proposed batch/dream protocol (not implemented):

```json
{"caller":{"project":["demo"],"host":"client-a"},"operations":[{"op":"correct","id":"ticket-a","expected_version":1,"content":"A revised synthetic fact."},{"op":"retire","id":"ticket-b","expected_version":1}]}
```

POST `/v1/batch` would use one idempotency key and one transaction; sort locks by fact ID to avoid deadlocks, validate every expected version, and commit everything or nothing. POST `/v1/dream` would accept an immutable source snapshot, policy/model revision and budget, return a job ID, and apply approved changes through the same versioned batch boundary. It must not regenerate removed auxiliary fields or restore automatic fact-card recall. No actual dream/model tool loop is executed here.

Production service requirements still to implement/test: TLS or an authenticated local proxy over the private path; scoped short-lived identities and key rotation; least-privilege SQL roles and schema ownership; strict per-operation JSON schemas; deadline propagation including pool wait and embedding work; server-side query cancellation on disconnect; bounded retries with jitter only for safe reads or identical durable keys; and a consistent status endpoint after uncertain writes. On outage the client must report unavailability. No silent successful offline writes, durable queue, or read-cache freshness promise is introduced.

## Embedding and ranking lifecycle

The measured route is public `intfloat/multilingual-e5-small`, revision `614241f622f53c4eeff9890bdc4f31cfecc418b3`, original float32 ONNX, 384 dimensions, attention-masked mean pooling, L2 normalization, `query: ` and `passage: ` prefixes, 512-token limit. The file hashes are in evidence. It fits this worker and supports both languages; this is a bounded candidate choice, not a production model recommendation. The model's [published instructions](https://huggingface.co/intfloat/multilingual-e5-small) prescribe the prefixes. Model loading, passage embedding and query embedding are measured separately. No private facts or provider keys were used. Provider usage/billing and ongoing operating cost are unknown.

The quality arms use genuine content-only embeddings, exact cosine distance, and equal-weight reciprocal-rank fusion with constant 60 and 100 candidates per channel. Vector retrieval still applies latest-version/status/expiry/scope/shelf/kind/date filters before the limit. `AllWords` also constrains vector/hybrid candidates through the lexical conjunction; ordinary OR queries permit semantic paraphrases. This explicit choice avoids silently turning normal OR queries into AND. Exact ticket and numeric terms survive normalization, but vector/hybrid ranking does not guarantee their precedence. The experiment tests those regressions rather than assuming a semantic model solves them.

No HNSW/ANN is installed or measured. At this scale exact search is sufficient; pgvector's [documentation](https://pgxn.org/dist/vector/0.8.0/) distinguishes exact search from optional approximate indexes. ANN and filtered top-k underfill need a larger authorized corpus and an exact-search reference.

The prototype synchronously backfills the frozen snapshot and embeds each query before search measurements. Search SQL times exclude embedding; transport comparisons are lexical. It publishes a completed vector only if fact version, content hash and active status still match under the same fact lock. Retirement removes vectors atomically. Expiry remains read-time and is not a reason to erase history. Mechanics fixtures use genuine vectors as arbitrary DB payloads, separately from retrieval quality.

Proposed service lifecycle: publish lexical content in the write transaction; queue version/hash/model-specific embedding work in a durable outbox; use bounded retries and per-space caches; expose lexical fallback and its reason when embeddings time out. Query-time embedding must use the same model tag. Backfill and model upgrades write a separate space/tag, verify coverage, then switch the active read space atomically; never fuse distances from incompatible spaces. Durable jobs, timeout fallback, model outage, cache economics, and model-swap behavior are not implemented here.

## Operations and next decision

The stand owns a disposable rootless PostgreSQL process and stops it in `finally`. It uses extracted Debian binaries, SCRAM, loopback only, 32 MiB shared buffers, 4 MiB work memory, no JIT or query parallelism. No Docker daemon, live DB, infrastructure configuration, or production client is changed. A real custom-format dump/restore is exercised. Raw dumps and data directories remain private evidence, with stopped processes.

Any durable deployment belongs in the infrastructure repository. It needs tailnet ACLs, application authorization, service secret rotation, PG bindings restricted behind the API, max-two initial pool sizing, CPU/RAM limits, query/job/backup monitoring, extension upgrade rehearsals, encrypted off-host backups, and periodic restore checks. Direct DB exposure needs its own authorization design. None of these is established by loopback connectivity.

The next decision requires a private frozen corpus with verified provenance, genuine agent tool loops under the same model/budget, an authorized private endpoint, scale measurements, and an operating budget. Keep current SQLite/text defaults until those gates have evidence.
