# Read-only HNSW query-shape experiment

**CORRECTNESS_TIMEOUT.** One fixed candidate used the existing HNSW index in a
default-plan diagnostic, and its synthetic semantic tests passed. The experiment
then stopped on its first full-corpus exact-reference query. Full equality,
all-context index coverage, ANN recall and performance remain unestablished. Keep
production SQLite/text defaults and the draft PR / full #674 unchanged.

The [prospective declaration](scripts/postgres674/ann_shape-declaration.json) froze
the sources, cases, four diagnostic contrasts, single candidate, 527-operation
maximum and 300-second aggregate driver caps before new SQL. The only prospective
TDD repair replaced inner ONLY with WITH TIES; sources before and after that
repair are retained separately. The [aggregate packet](scripts/postgres674/ann_shape-aggregates.json)
contains only fixed categories, numbers and public source hashes. No model,
embedding, provider or native episode was called. Production baseline remains
`a5de0b989ceb1046f4933b19cae7ef60bed0ede2`; the accepted starting HEAD was
`d14af092d6271c807a7f8ff83a75d8a4bcda0850`. Cached E5 vectors retain
`e5-small-614241f-f32-mean-l2-384`. This is plan engineering on reused development
cases, with no held-out or natural relevance evidence.

Initial read-only catalog inspection found PostgreSQL 18.6 and pgvector 0.8.7.
The owned 30k cosine index is valid/ready, full rather than partial, on the raw
384-dimensional vector column, with m=16 / ef_construction=64 and size 7,151,616
bytes. Actual column definitions were retained privately. Initial primary-ID,
history and vector counts matched the accepted current/10k/30k packets, and all
three whole-history fingerprints matched. Additional derived-table fingerprints
were frozen for this attempt; the final readback was not reached. No server-side
preservation beyond the initial comparison is claimed. Every issued SQL statement
was SELECT or EXPLAIN ANALYZE in a read-only connection.

The five ranked hypotheses were: complete ordering prevents an ordered index
path; joins/latest semantics select a join-first sort; expression/operator/index
mismatch; planner cost/selectivity preference; and prepared/generic plan effects.
The four fixed probes used the first previously selected context/vector, without
favorable-case selection or forcing. Their results update those hypotheses:

| Fixed diagnostic | Default HNSW | Executor ms | Interpretation |
| --- | --- | ---: | --- |
| Unmodified reference | No | 236.789 | Join-first plan, full four-key sort; latest correlated lookup ran 12,426 times. |
| Raw distance-only KNN | Yes | 2.086 | Existing operator/column/opclass can form an ordered HNSW path. |
| Same raw KNN plus ID tie key | Yes | 0.373 | Incremental Sort consumed an ordered HNSW prefix; an extra tie key alone does not explain the failure. |
| Single eligibility-filtered candidate | Yes | 0.905 | Correlated eligibility inside the distance limit retains a default ordered HNSW path in this one context. |

These are four ordered, dependent diagnostics, not a timing cohort or a speedup
estimate. The raw probes omit eligibility and are not semantic comparators. The
candidate inspected 13 vector results and its inner limit returned 12 tied rows;
the outer order returned ten. Joins/global latest materialization in the saved
plans and the new reference sort remain the strongest explanation for the lost
ordered path. The experiment does not isolate a sole cost or prove HNSW cannot
serve every reference parameterization. Expression/operator/index validity
mismatch is weakened by actual use. A full index has no partial predicate to
prove here. No forced-index diagnostic was used.

The [candidate helper](scripts/postgres674/ann_shape.py) checks the original
latest version before status/expiry and keeps scope, shelf/origin, kind, date,
exclusions and AllTerms inside a correlated EXISTS before the inner distance-only
limit. OFFSET 0 retains correlation rather than allowing join pull-up. The inner
FETCH WITH TIES includes exact equal-distance boundary rows in an exact traversal;
the outer join uses the same eligible ID/version, then applies the reference
scope/project priority, parsed timestamp descending and ID tie order. It retains
all saved queries, vectors and filters, including the fixed empty-lexical context
when running vector queries. Approximate HNSW traversal cannot promise complete
tie identities: outer sorting only orders the identities it receives.

Tests came first. Six synthetic PostgreSQL VALUES cases exercised these real
helpers without creating or changing relations. Truncating inner ties produced
two semantic assertion failures, zero errors. Replacing ONLY with WITH TIES made
the identical 11-test suite pass: six live SQL tests plus five offline helper
tests. The original cases included latest retirement/expiry and stale derived
versions, selective scope/kind/date, shelf/origin and exclusions, AllTerms, tied
vectors and stable order; helpers rejected invalid vector dimension/space and
output collisions or links. Independent review subsequently showed that removing
the latest guard still passed all six original live fixture methods: bad global
rows remained below k. Those original tests do not establish sensitivity to a
missing latest guard. Fixtures include timestamp offsets and the actual parsed timestamp
operator, but do not isolate a timestamp lexical-order reversal as a separate
negative control. Existing nine-row synthetic relations were untouched.

The subsequent full-corpus gate was declared for all 128 current contexts and
eight contexts at each replica scale, checking live exact reference/candidate
results against saved eligible identities and oracle ranks within 3e-6. Its first
current reference query hit the five-second statement timeout with ordinary
index scans and bitmap scans disabled on the separate correctness connection.
The same reference's preceding default-plan diagnostic used B-tree history
lookups. This implicates the correctness configuration removing affordable
lookup paths; no completed exact-mode plan isolates the cause. No context pair
completed, no mismatch was concealed, and the failed attempt remains intact.
The default reference query bytes were unchanged. No full-corpus candidate
correctness, strict ID/version recall@10, kth-boundary validity, eligible
membership, filtered underfill or ANN deterministic ordering result is available.
Those fields are null, rather than successful zeros, in the aggregate packet.

Preparation was exactly psycopg prepare_threshold=None and prepare=false on every
SQL execution, including EXPLAIN. There was no SQL PREPARE, forced generic/custom
plan or partial-index planning experiment. Generic-plan behavior remains
unmeasured; automatic preparation cannot change plans within this invocation.
The planned post-equality default coverage of all eight contexts at ef_search
40/100 was not reached. One candidate diagnostic at ef_search40 does not certify
other templates/parameters, settings or individual timing executions. This is a
correctness timeout, not INDEX_NOT_USED and not verified ANN coverage.

Exactly 31 PostgreSQL SQL attempts occurred: two initial metadata SELECTs, twelve
RED fixture SELECTs, twelve GREEN fixture SELECTs, four diagnostic EXPLAIN ANALYZE
calls and one failed correctness SELECT. Thirty SQL attempts completed; one was
canceled. Both TDD suites issued all twelve SQL statements even when semantic
assertions failed. There were zero skipped operations, timing SQL calls, warmups,
coverage plans or completed full-corpus pairs. Failed timing is null with a
separate attempted wall duration. No empty-lexical placeholders are used. The
planned 192 warmup/timed calls were never issued; no timed cohort was attempted.
No p50/p95/p99 latency estimates exist. Even the planned n=24 per group would be
dependent small samples (nearest-rank p95 rank23, p99/max rank24), not stable tails.

Driver stage wall time totaled 7.895 seconds, client user/system CPU
0.185/0.012 seconds, and maximum observed per-process Linux high-water RSS was
218,224 KiB, including input loading and Python/libpq allocations. Wrapped-command
wall totals, per-stage resources, argv/exits and connection initialization times
are retained privately; stage timers exclude earlier input loading/process
startup. Actual wrapped wall remained below the 300-second declaration. One
client was used at a time, no PG parallel workers, query timeout five seconds,
work and maintenance memory 64 MiB each. These are client measurements; PostgreSQL
server resources remain unknown. The unavailable collector was not repaired.

The first independent review rejected author tree `db61c521` for two source
issues: a later timed failure could discard earlier returned hits/quality, and
the latest fixture guard was ineffective. Both findings and the rejected tree
remain intact. A [separate source-only declaration](scripts/postgres674/ann_shape-repair-declaration.json)
preceded two new offline regression tests: four intended assertion failures,
zero errors. The identical tests then passed after minimal repairs. The driver
now journals returned warmup/timing rows and finally saves completed hit/quality
records and counts when a later worker fails. Latest/status/expiry sentinels now
outrank eligible ties if a guard is lost; independent guard-removal controls must
fail. Seven final offline tests pass, including the actual driver failure path.

These regression controls use memory-only SQLite, a synthetic fake libpq path,
and a test-only adapter for identical unit vectors. They provide no new PG,
heterogeneous-vector, ANN or full-corpus equality evidence. The revised fixtures
were not rerun against PostgreSQL. The candidate compiler itself is byte-identical
to the measured compiler; instrumentation and tests differ. Measured `source-green`
and final `source-final` snapshots have separate explicit hashes in the aggregate
packet. The original 31 SQL attempts and timeout are unchanged; fresh independent
re-review of the final source and parent acceptance are separate required gates.

All attempted/failed shapes, raw plans, SQL/parameters, source snapshots and
partial failure records remain private under the owned packet. Prior public
numerical artifacts and prior drivers are immutable; the old SCI-01 reporting
qualification and two native budgetUNKNOWN episodes retain their status. Only
new research files are staged, with no commit, push, upstream integration or
production change. Fresh independent review of this exact result and parent
acceptance are separate required gates; the public aggregates record their
author-freeze status. Later review receipts remain separate.

The next finite action is a separately declared correctness-method attempt that
keeps affordable B-tree history lookup while establishing exact vector ordering,
then validates the same 144 contexts against the frozen oracle. It must retain
this failed attempt, the single candidate and all filters, and leave timed
reference bytes/settings unchanged. Only a completed equality gate permits the
all-context default-index coverage and one timed cohort. No production or
semantic-quality conclusion follows from the present diagnostics.
