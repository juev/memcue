# Reproducing the finite matched-history study

Read the [report](../../postgres-674-matched-history-report.md) and frozen [declaration](matched-history-declaration.json) first. This is an app-owned PostgreSQL research harness. It never loads a model, reads the original fact snapshot or invokes an agent.

Use the existing research virtualenv and wrap each heavy invocation separately:

```sh
/opt/data/scripts/build-check.sh env TMPDIR=/opt/data/cache/scratch/m674-matched /opt/data/home/src/.artifacts/memcue-674-local/venv/bin/python -B -m unittest discover -s docs/research/scripts/postgres674 -p 'test_matched*.py'
/opt/data/scripts/build-check.sh /opt/data/home/src/.artifacts/memcue-674-local/venv/bin/python -B docs/research/scripts/postgres674/matched_study.py declare
/opt/data/scripts/build-check.sh /opt/data/home/src/.artifacts/memcue-674-local/venv/bin/python -B docs/research/scripts/postgres674/matched_study.py run
```

The exact measured output directory and namespaces already exist. Repeating these commands must refuse them. For another authorized repetition, first choose a new absent output child and absent synthetic/replica prefixes in the explicit constants in `matched_study.py`, and create a fresh declaration. Preserve the existing completed source namespace and all packets. Changing these constants produces a separately hashed experiment. The driver freezes its own exact sources, SQL/parameters, cached input hashes, chosen IDs and argv before opening a PostgreSQL session. The passfile goes directly to libpq; Python never opens it. Errors print a fixed category and retain private details only for an output claimed by that invocation.

`matched_query.py` builds both variants from the existing filter/tie functions. The candidate groups the complete history before any eligibility filtering; it does not assume current derived indexes prove latest-version status. Scale loading copies every version of each selected template, SQL-side, and checks all copied values in both directions. There is one writer, at most two clients, and bounded session settings. A conservative preflight and post-write byte/count checks enforce the declared budgets.

`matched_audit.py PACKET NEW_OUTPUT` independently recomputes quantiles, repeated results, eligibility membership and copy/schema assertions from saved records. `matched_plans.py PACKET NEW_OUTPUT` emits fixed numeric plan categories. Both require new output files. Raw SQL, parameters, context IDs, vectors and plans stay private; public results exclude those fields. `matched_stats.py` is the separately hashed, read-only name-discovery fallback collector; it only observes an active study and stops/reaps its own SSH process in `finally`.

The measured driver sources are retained in each private packet. The final candidate additionally has the tested output-collision guard; SQL and measurement logic are unchanged. The saved audit sources and collector sources have their own hashes. The public declaration intentionally records the measured source hashes, not the later final guard's hash. Old production/full-suite baselines are reused, not rerun or requalified as new checks.
