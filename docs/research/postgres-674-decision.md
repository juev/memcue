# Current PostgreSQL / pgvector decision

**Keep SQLite/text as the default; no unconditional production replacement or
live cutover is justified by this research.** The isolated stand retains text/Git
history as authority and PostgreSQL as a replaceable research read backend.
Production stays SQLite/text. The shared-service design and fast indexed SQL are
demonstrated within research boundaries; a useful-answer improvement sufficient
to pay for the intended client path and operations is not established. Issue
[#674](https://github.com/juev/memcue/issues/674) remains open and the PR remains
draft. This is the current decision checkpoint; linked historical reports retain
their original measurements and qualifications rather than being rewritten.

PostgreSQL can supply shared transactional state, server-side eligibility and
version checks, lexical/vector storage and an indexed query path. It does not
supply a validated agent-quality policy, production authorization, offline
synchronization, an embedding job service, or a justified operating budget by
itself. Removing keywords/queries, introducing semantic retrieval, and choosing a
shared PostgreSQL backend are separate changes. No SQLite-plus-vector alternative
benchmark was run, so embedding gains cannot be credited uniquely to PostgreSQL.

The owner has separately chosen an **optional PostgreSQL backend selected by an
environment DSN**, while keeping SQLite as default: [#680](https://github.com/juev/memcue/issues/680).
That proposed product backend is not gated on a retrieval-quality victory,
pgvector, embeddings, or completion of this research. When explicitly selected,
the proposed PostgreSQL mode is a single authoritative shared store, not an
automatic writable mirror of text history. The derived-backend boundary above
describes this research stand, not a veto on that separate product direction.
[#681](https://github.com/juev/memcue/issues/681) separately defines opt-in embedded
Tailscale connectivity; neither issue is evidence that these features ship.
Direct pgx is the proposed initial fit for the DSN interface. The historical
pooled-HTTP preference remains a research hypothesis, not an API-only product
requirement or a measured remote winner. This paragraph qualifies product scope
after code review; it changes no executable examples or measurements.

The [original ADR](postgres-674-adr.md) preserves schema mapping, architecture,
protocol examples and the authority/cutover design. Its early statements that
ANN was not measured and agent outcomes were unavailable describe that historical
checkpoint; the newer evidence below supersedes those particular unknowns only.
Do not create two independently writable authorities. An authoritative shared PG
service needs a separate decision about writer cutover, export/rollback, Git
synchronization, conflicts and offline operation. The current derived-backend
choice permits rebuilding PG from verified text history and retaining local
SQLite fallback; it does not authorize live writes or promise a fresh offline
service cache.

Measured evidence, with its boundaries:

| Evidence | Actual result | Interpretation |
| --- | --- | --- |
| [26 authored synthetic quality cases](postgres-674-report.md), one representative repetition, limit10 | Hit@5: full SQLite23/26, content SQLite22/26, PG lexical22/26, exact vector/hybrid26/26. Useful precision@5:0.7051 lexical vs0.3077 vector/hybrid; known harmful results@10:5 vs8. | A small authored bilingual fixture shows target recovery and noise tradeoffs. Labels were not independently blind; several language probes use English anchors. Perfect hit@5 is not safe final-answer evidence. |
| [Current private-copy replay](postgres-674-phase2-report.md):128 normalized contexts, fixed date/filters,7,713IDs/33,371history rows/3,462vectors | Full/content SQLite empty5/6; PG lexical6; vector/hybrid0. Overlap differs substantially. | Unlabeled overlap and fewer empty results are not relevance recall or useful-answer gains. Auxiliary-field removal also removes legacy translations. |
| Same replay,256 library/SQL samples per arm/group | c1 p95:SQLite full20.21ms, content15.32ms, PG lexical24.15ms, exact vector62.91ms, hybrid73.69ms. | Adapter/library intervals exclude query embedding, normalization, startup and connection setup; different rankers/floors and schema effects remain. |
| [Accepted matched-history ANN checkpoint](postgres-674-ann-exact-report.md):30kIDs/129,758history rows/13,444vectors,8fixed reused contexts,3repetitions/c1,c2 | Reference p95:251.62/263.76ms; ANN ef40/100 p95:3.08–3.51ms.144timed SELECTs total,24/group, plus48warmups. Strict ID/version recall@10 and3e-6boundary validity1; no underfill. |144exact-context validations and16/16default HNSW probes preceded one cohort. Default compilers/filters stayed unchanged. Replica ties add scale, not semantic diversity. Individual timing calls were not each explained; ANN ties are not guaranteed complete. Small dependent tails are not sustained-load or full-tool results. |
| Two later invented native agent episodes | Both returned correct answers; both remain budgetUNKNOWN and ineligible as controlled quality evidence. | No natural-task, budget-comparable effectiveness claim. Their original failures/qualifications remain immutable; no episode was rerun here. |

The [matched-history report](postgres-674-matched-history-report.md) retains the
slower materialized-latest candidate and the SCI-01 distinction between actual
SQL and untimed lexical placeholders. The [first query-shape report](postgres-674-ann-shape-report.md)
retains its exact-mode timeout, failed reviews and source-only repairs. The later
accepted ANN result does not erase those failures. Current resource work does
not repeat or retune that latency cohort.

Pooled HTTP remains a protocol hypothesis, not a measured remote winner. In the
[local synthetic transport fixture](postgres-674-report.md),30warm samples per
route gave wire/HTTP p95 of3.92/5.02ms;10cold research-CLI samples gave185.14/51.73ms.
These were lexical loopback requests, and startup/import/pooling differences
confound the cold comparison. The [phase2 supplement](postgres-674-phase2-report.md)
measured fresh-normalization research wrappers: eight invocations/group, cached
vector PG p95 about290ms and hybrid298ms at c1; three fresh model processes gave
about2.92/3.20s. None measures the production Hermes tool/CLI path, hint-triggered
second searches, intended user's RTT, direct versus relay routing, or full remote
wire-versus-HTTP behavior. Low indexed SQL time does not remove those costs.

The new resource-only probe now supplies bounded PG-container observations. A
[prospective declaration](scripts/postgres674/resource-declaration.json) froze
sources and unchanged reference/default ANN SQL bindings before active SQL.
Read-only SSH discovery worked; one extended inspection wrapper failed and its
command/SSH stderr remain private, then the final allowed minimal inspection
succeeded. The historical collector's SSH255 cause remains unproven; no key,
authentication, known-host, host or container configuration was changed.

One sampler read cgroupv2 cumulative CPU and memory fields
for the exact approved container. Target ID, running state, PID, start time and
restart count matched before/after. The observed sampler was reaped with exit0 and handles EOF/BrokenPipe.
Independent review found that the measured implementation could block beyond
its intended90s input lifetime on a partial line. That rejected source and review
remain preserved. A source-only repair now buffers nonblocking stdin bytes,
rechecks the deadline between reads, and consumes complete buffered commands.
The successful measured run is retained, not invalidated or repeated. Fresh
repair review and parent exact acceptance remain separate required gates. A20s passive baseline preceded16fixed
requests per arm, one client, no query parallelism,5s statement timeout and
64MiB work/maintenance memory. All51SELECTs completed:48resource requests,
one settings control and two full count/fingerprint readbacks. Fingerprints
matched. Whole probe wall was29.99s under120s. There was no new EXPLAIN, latency
quantile/cohort, model load, embedding, semantic label or native episode.

| Resource window | Requests / samples | Window seconds | Container CPU seconds | Sampled memory.current MiB min–max |
| --- | ---: | ---: | ---: | ---: |
| Passive baseline |0 /41|20.0011|0.200492|778.44–784.86|
| Reference |16 /8|3.4013|3.389283|780.09–782.32|
| ANN ef40 |16 /2|0.0546|0.041253|780.00–781.65|
| ANN ef100 |16 /2|0.0525|0.039632|780.10–781.41|

Cgroup limits were2CPU cores and1,024MiB memory. Across these windows file-accounted
memory reached750.04MiB; anon maxima were11.52MiB baseline,8.96MiB reference and
8.19MiB in each ANN window. File accounting includes more than private heap and
is not a measurement of pure engine cache. Swap current was0 in the observed
samples, and memory-event counter deltas were0 over each bounded window. No
historical memory.peak or OOM total was treated as a new peak/event. Sampling was
nominally0.5s; the very short ANN windows have only their two boundary samples,
so transient maxima and a sustained resource envelope are unknown. Raw units
and fixed-key [resource aggregates](scripts/postgres674/resource-aggregates.json)
remain separate from historical latency numbers.

These are container-wide observations including preserved historical research
tables/caches and background PostgreSQL work, not a clean production footprint.
No baseline subtraction assumes constant background work. Host SSH/sampler CPU
is not charged to the target cgroup, but can perturb scheduling; it was not
separately measured. Connection initialization lies outside each active group's
counter boundaries. Memory.current, anon/file accounting, Docker working set,
process RSS and engine heap are different quantities; no PostgreSQL RSS sum or
heap estimate is reported. This slice closes the sampled PG-service gap only,
not embedding-worker RAM, complete-host resources or cost.

Past embedding measurements remain component evidence, not new readings:
[phase2](postgres-674-phase2-report.md) recorded487,352,338download bytes,
2,477.65ms authentication/load,3,462passages backfilled in78.61s wall and156.58s
client user CPU, and128warm query encodings with p95 of9.69ms. Its1,284,196KiB
driver high-water RSS included model, orchestrator and hashing buffers and
excluded PG; it is not pure model RSS. No monthly cost follows from these numbers,
short CPU windows, or zero provider API calls. Provider billing and operating
budget remain unknown.

Finite remaining gates are:

1. Preserve the narrow nonblocking-input repair and its independent approval.
   The accepting parent verified the exact reviewed source and non-executable
   product-scope qualification separately. The actual subprocess regression first failed with
   its writer held open, then passed unchanged after repair; split commands,
   buffered commands, EOF and parse-error behavior are covered. No resource or
   ANN workload was rerun.
2. Independently defined natural-task useful/harmful labels and genuinely
   budget-comparable agent outcomes before claiming useful-answer gains.
3. The intended client and network path: full Hermes/plugin/CLI invocation,
   embedding/cache policy, repeated searches, RTT/direct-relay behavior and
   wire/API comparison. Include tailnet/DB/model outages, cancellation, retry and
   freshness behavior; local lost-response/restart/restore fixtures are narrower.
4. A separate authoritative-service decision if desired: least-privilege roles,
   application identities/rotation, cutover/export/offline/sync and rollback.
   Proposed atomic batch/dream operations and durable version/hash/model-specific
   embedding jobs, bounded retries/fallback/model changes remain unimplemented.
5. A measured operating budget and sustained read/write/embedding/recovery envelope
   with deployed backup/restore roles and extension upgrade practice. Existing
   orderly restarts/dump-restore and tiny races do not certify crash recovery or
   production operations. Durable host changes require their own infrastructure
   review; none is authorized by this checkpoint.

The collector/parser contract had one intended CPU-unit assertion RED with
zero errors, then identical three-test GREEN. Four final offline checks also
exercise client deadline/finally and ordinary process cleanup, but missed the
partial-line remote deadline defect. Fresh review returned CHANGES_REQUESTED
with that reproducible logic finding; no security/scientific finding was asserted. A source-only90s work
guard was added after the29.99s measurement to reserve cleanup time inside the
120s outer limit; measured and final source hashes/snapshots are separate and no
probe was rerun. The later input repair has separate source/test identities:
seven identical tests went from two semantic failures, zero errors, to GREEN.
A supplementary blank-line control exposed and corrected a buffering parse-error
regression; all eight final tests pass. Children are supervised and reaped even
on assertion failure. These checks bound partial-line input waits only; remote
output backpressure, network failure and arbitrary blocking callbacks remain
untested, and no universal transport-stall containment is claimed. The historical
guard-red qualification remains unchanged: that four-test run passed and was not
a RED receipt. The earlier historical SSH255 root cause remains unresolved.

No duplicate tasks, production integration, publication or full-issue completion
is claimed. The evaluated source baseline remains`a5de0b989ceb1046f4933b19cae7ef60bed0ede2`;
starting published HEAD is`eed9196499096d1a5d7168e07c12c7e5230c15f6`. Historical
ADR/reports and102/7/119/111seals stay immutable. This document and aggregates
record the author-freeze state; independent review and parent publication remain
separate receipts.
