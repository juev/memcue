# Exact validation and one gated ANN cohort

**COHORT_COMPLETE, pending independent review and parent acceptance.** The unchanged
candidate passed exact ID/version order, score tolerance and eligible-membership
checks on all 144 saved contexts. All 16 default candidate plan probes used the
owned HNSW index. One 30k cohort then completed with lower observed client SQL
latency and complete strict ID/version recall on these reused development cases.
This does not accept production migration, held-out relevance, or full #674.
Production remains SQLite/text; the PR remains draft.

The [declaration](scripts/postgres674/ann_shape-exact-declaration.json) froze
sources and all query bindings before PostgreSQL execution. The correctness-only
helper adds floating zero to the inner distance ordering. It leaves the selected
score expression, parameters, latest/status/expiry and every other filter, WITH
TIES boundary, outer parsed timestamp/ID order and limit10 unchanged. Adding zero
to finite distances preserves their order and ties, while breaking the direct
operator-order expression used by HNSW. Ordinary B-tree and bitmap paths stay
enabled. Neither timed compiler changed: published candidate/reference files and
all default candidate SQL/bindings match the released `3c827fd2` checkpoint.
A materialized vector input was considered but not selected. No method, cases,
vectors, dates, model, ANN parameters or planner costs were tuned from quality.

The focused actual-helper contract first failed one intended assertion, with
zero errors; the identical three tests passed after the minimal ordering change.
Nine final offline tests pass, including the actual driver refusing SQL after
input loading consumes the deadline. A single monotonic deadline covers launcher
admission, imports/loading, both execution children, source correction time and
final readback. It is never reset at a stage or continuation. Connections use
read-only autocommit, timeout5s, no PG parallel workers, work/maintenance64MiB,
prepare_threshold=None and prepare=false. Startup options issue no SQL statements.

Revised real PostgreSQL VALUES fixtures were exercised before corpus validation.
All six default-candidate and all six exact-candidate methods passed. Removing
latest or status independently caused one semantic assertion failure in each
six-method suite, zero errors. The first expiry mutation instead caused six
`42P18` datatype errors: an intentionally true parameter expression lacked a
server-side type. This was a failed control, not evidence of guard detection.
Its complete 63-operation failed packet, logs and source snapshot remain intact.

A separate [prospective control supplement](scripts/postgres674/ann_shape-exact-control-supplement.json)
explicitly typed only that test-local parameter as text, raised the maximum from
565 to 578 within the 600-operation cap, reused the successful controls, and kept
the original absolute deadline. The repaired six-method expiry suite then caused
one intended semantic assertion failure, zero errors. No cohort had run before
this repair; no baseline/case was dropped, and no measured cohort was rerun.
The initial source and corrected measured source have separate private snapshots.

Six representative EXPLAIN ANALYZE probes covered the first selected current
context and first two selected 30k contexts, each with natural default reference
and exact candidate. None used HNSW; all showed ordinary history index access.
Reference/exact-candidate executor times were 63.12/56.83ms current,
263.02/297.44ms and 174.09/174.86ms at 30k. These correctness diagnostics are not
performance arms. The reference query/settings were not made slower to produce
a comparison. All 128 current and eight contexts at each replica then completed
288 ranked SELECTs. Every pair matched strict ordered ID/version identity and
scores within3e-6; live reference also matched the frozen exact oracle and saved
eligible membership. Initial/final five-table fingerprints and counts matched
the previously sealed snapshot, including derived/vector tables.

After equality, all eight selected 30k contexts at ef_search40 and100 received
default-plan probes: 16/16 used HNSW. The owned full cosine index remains
m16/ef_construction64, 7,151,616bytes. Iterative scan was observed off and was
never changed. No index/schema/data writes or forced plans occurred. Automatic
preparation was disabled on every call; generic-plan behavior was not tested.
Probe coverage spans every selected template/parameter and ef setting, but
individual timing calls were not separately explained. The observed index
coverage must not be presented as plan telemetry for every timed execution.

The one cohort retained the declared order: concurrency1 then2; reference,
ann40, ann100; fixed worker chunks, one warmup per context/group, three ordered
repetitions. Every vector query remained, including the empty-lexical context.
Each cell has24 executed timed SELECTs from eight contexts, plus eight warmups.

| Arm | Clients | p50 ms | p95 ms | p99 ms |
| --- | ---: | ---: | ---: | ---: |
| Reference | 1 | 219.10 | 251.62 | 264.34 |
| ANN ef40 | 1 | 1.93 | 3.51 | 3.54 |
| ANN ef100 | 1 | 2.14 | 3.08 | 3.15 |
| Reference | 2 | 220.92 | 263.76 | 290.79 |
| ANN ef40 | 2 | 1.96 | 3.21 | 4.19 |
| ANN ef100 | 2 | 2.12 | 3.50 | 3.72 |

All six groups had strict ID/version recall@10 mean1, kth-distance-boundary valid
fraction1 within3e-6, all hits eligible, zero underfilled requests and all24
ordered identity/score comparisons equal to the oracle. These are observations
on the fixed reused cases. ANN cannot promise complete tie identities; WITH TIES
cannot recover rows never visited by approximate traversal. Complete observed
identity agreement is not a guarantee for other vectors or filters. Nearest-rank
p95 is rank23 and p99 is max/rank24; these small dependent samples, ordered groups,
warmed caches and shared deployment do not establish stable tails, sustained-load
behavior or natural semantic relevance. [Exact numeric aggregates](scripts/postgres674/ann_shape-exact-aggregates.json)
retain unrounded values and separate identity/boundary/underfill measures.

Exactly578 SQL operations were attempted: two initial metadata SELECTs, two final
fingerprint SELECTs,72 fixture/control SELECTs, six representative EXPLAINs,
288 correctness SELECTs,16 default-coverage EXPLAINs,48 warmups and144 timed
SELECTs. Of these,572 completed and six failed with the preserved datatype error.
No skipped operations or zero timing placeholders exist. Started/completed rows,
returned hits, quality and counts are journaled incrementally; finally-save
retains partial evidence on failure. There was exactly one cohort start.

Whole driver wall was140.10s, including the failed control and correction gap;
wrapped wall was140.18s on the same absolute clock. Whole-child client user/system
CPU totaled3.38/0.25s across the two children; maximum per-process high-water RSS
was218,792KiB. Eighteen session establishments had measured client initialization
intervals retained privately. PG server CPU/RAM/cost remain unknown; the SSH
collector was not repaired. No embedding/provider/model query or native episode
was run; cached E5 modeltag remains `e5-small-614241f-f32-mean-l2-384`.

Published HEAD remains `3c827fd2c4eb065485fb6e30d21d659d7802da90`, production baseline
`a5de0b989ceb1046f4933b19cae7ef60bed0ede2`. Only new scoped research files are
staged; no commit, push, upstream integration or PR change was performed. Old
31-SQL failure evidence, source snapshots, failed reviews and102/7/119 seals are
immutable. Historical virtual WORKTREE entries refer to their frozen trees.
Fresh-context independent review of the exact new tree and parent acceptance are
required separately; this report records the author-freeze state. Native episodes
remain budgetUNKNOWN. The next finite action is exact parent acceptance of this
bounded packet, followed by a separately declared generalization/resource study
if authorized; no further cohort or tuning belongs to this invocation.
