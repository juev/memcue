# Finite HNSW query-shape slice

Read [the report](../../postgres-674-ann-shape-report.md) and the frozen
[prospective declaration](ann_shape-declaration.json) before using this code.
This invocation stopped at its first full-corpus correctness query. It is not an
accepted ANN comparator, reusable performance result, or production change.

`ann_shape.py` compiles the single candidate: scan vectors in cosine-distance
order, check latest-version/status/expiry and all saved filters in a correlated
existence predicate before the inner limit, retain distance ties, then apply the
reference scope/project priority, parsed timestamp and ID order outside. The
outer history join is on the same already-eligible primary-key version. A stale
vector/index version cannot resurrect an older fact. AllTerms remains eligibility
inside the limit. No shortlist size or filter is tuned. Approximate HNSW traversal
can miss distance ties; FETCH WITH TIES cannot restore identities never visited.

`test_ann_shape.py` contains five offline helper tests and six PostgreSQL VALUES
fixture tests. The live tests use the real compiler, executor, PostgreSQL vector
operator and timestamp cast without creating any relation. Initial ONLY truncated
12 identical vectors at k=10: two assertions failed, zero errors. The sole SQL
repair was WITH TIES. The same 11 tests then passed. Invalid space/dimension,
nonfinite/unnormalized vectors and output collisions/links use the actual helpers.

`ann_shape_study.py` has explicitly bounded declare/metadata/test-red/test-green/run
commands. Its paths are fixed to this existing research worktree and prior
allowlisted packet. Do not rerun into any existing packet; exclusive output
creation rejects collisions. The red/green snapshots and measured source identity
are retained separately. Application SQL uses read-only autocommit connections;
libpq startup options set resource limits without issuing SET statements. Each
issued SELECT/EXPLAIN is journaled privately. Skips have executed_sql=false and
sql_ms=null; failed SQL has sql_ms=null and a separate attempted wall duration.
Automatic preparation is disabled and every execution requests prepare=false.
There is no SQL PREPARE, generic-plan experiment, planner forcing, write, index
build, inference or native episode. All heavy invocations used build-check.sh.

The stage budgets sum to 527 possible SQL attempts and 300 driver seconds. Exactly
31 SQL attempts occurred; the run stopped after its first exact-reference query
timed out with indexscan/bitmapscan disabled. That correctness-only setting also
removes ordinary B-tree lookup paths. No timings, recall results or full-corpus
candidate equality were obtained. A separately declared next attempt needs an
exact correctness method that preserves affordable history lookup, with the
reference timed arm unchanged. The existing output is immutable failure evidence.

A fresh independent review and parent acceptance of the exact staged tree remain
required. The previous matched-history driver, erratum, numeric artifacts,
production baseline and native budgetUNKNOWN episodes are untouched.

The first independent review rejected the author tree for lost completed timing
results after a later failure and ineffective latest-version fixtures. The
source-only repair adds `test_ann_shape_offline.py`, finally-saved partial timing
records, and competitive latest/status/expiry sentinels. Two regression methods
first produced four intended assertion failures, zero errors; the identical
methods then passed, and seven final offline tests passed. The SQL adapter is
limited to identical synthetic unit vectors, memory SQLite and julianday; it is
not PG/ANN or heterogeneous-vector evidence. No new PostgreSQL SQL was issued,
and revised fixtures have no new PG validation. Measured source-green remains
immutable; final source-final has separate hashes. The candidate SQL itself is
unchanged. See ann_shape-repair-declaration.json and the retained review receipts.
