# PostgreSQL checkpoint erratum — repair cycle 1

Current qualification, 2026-11-06. This updates the [checkpoint report](postgres-674-report.md); it adds no retrieval, ranking, model-inference or transport measurements. Issue #674 acceptance remains pending.

## Authorization correction

The owner authorized a separate private research PostgreSQL deployment on a private server over Tailscale. A separate infrastructure owner handles it. The original report's statement that infrastructure changes were unauthorized was stale. Live-memory migration, production cutover and live-data movement remain unauthorized. This repair did not access or verify the separate deployment, its host settings or its databases. Intended-client endpoint evidence remains a separate acceptance gate.

## Confirmed output-preservation defect

Independent review failed R1: rejecting an existing `experiments.py --out` directory still wrote `failure-traceback.txt` there. That could truncate a previous failure record or follow a symlink. The SQLite bridge wrote `normalized.json` before checking for an existing database and followed existing output-file symlinks. The reviewer reproduced the defect with isolated sentinels; the original failed review and its artifacts remain private and unchanged.

The repaired experiment driver logs failures only within a directory created by that invocation. It creates the failure record exclusively through an owned directory descriptor, preserving an existing file, symlink or hardlink. The bridge requires a new directory before any output, requires an existing parent, and creates JSON/database files exclusively. Both refuse symlink components in output paths.

The same overwrite class was checked in audit and recompute. Audit now refuses dangling output symlinks instead of resolving them into new directories. Recompute requires a new output file; existing files, symlinks and hardlinks are refused. Its stdout mode remains available. Internal incremental assertion records remain within newly created owned directories.

## Source and evidence provenance

The original current report was archived privately before correction. Its SHA256 is `7cffaef1c5380e24786da9c38d6fdbde976f33c0ad42565bc1a3722a2fc07b07`. The reviewed tree was `a35424206caf2b864ecf756be592918f277c7af2`. Original frozen packets, failed runs, declarations, fixtures, frozen driver copies, run manifests and public measurement JSON remain unchanged.

The [published manifest](scripts/postgres674/results/manifest.json) identifies the original measured code. Its hashes must not be interpreted as hashes of the repaired harness. Audit has its own original source declaration. The source changes below received focused output-guard tests; the original experiments were not rerun with them.

| Source | Original measured SHA256 | Repaired SHA256 |
| --- | --- | --- |
| `experiments.py` | `94948cd98896d6ac51f5c234547a108808c3c0b2cf93755a52e212b5152e7f02` | `294ba9588f4f676a1e2dd6c546a25466def5b970ab80636f6dae3812c1776c73` |
| `sqlite_bridge.go` | `4a32df72ba179329bec11d96f46a2c3b1e13af9b0d752febbce27626ea2e1edf` | `dbfd67988f3a276d6193032341dd946a514b63a5a0797c133f419be745609626` |
| `audit.py` | `a56f05d8e1d74654e292221b792c61659723113f5ea046f6703177bcd949033c` | `70b04dbfee2d1caba3253df3b13a2f8e758f514b07e38a5bd91e2f66e0df03a9` |
| `recompute.py` | `7265936736fe4f804a3df087f3ea282f99c6d2a79585f7029701277bcc989ee4` | `f30bd14e0e1d911cb8c0e939799f4737a9c0555eb41b2a1fb46a5befc2f7fe58` |
| `outputs.py` | `absent` | `88c45abc384c566d992145a22e18dbb5d39a818fdefb12d6c7c7ee292e46825e` |

The original bridge binary remains unchanged, SHA256 `9c49be26dd6fe8ddff25d22a36d2e9e645450ded4683205cbb1a1401e8c3d2b4`. Repaired builds use a separate private path. Existing retrieval/ranking evidence remains independently verified within the synthetic checkpoint's stated limits. Historical errors and the failed review remain part of the record.

## Repair verification

Behavioral tests first failed against the affected source, then passed after each repair. Private repair evidence retains source snapshots, exact argv, exit statuses and logs for that Red/Green chronology. Tests use bounded isolated scratch sentinels outside Git discovery, covering regular files, symlinks, hardlinks, dangling links, directory aliases, unchanged victim bytes/metadata, preservation of prior failure records, and fresh-output controls. No hostile probe targeted retained evidence or source.

Focused Python tests and Go bridge tests use individual `build-check.sh` invocations with at most two workers; no nested wrapper or unattended service is needed. Production source and module files remain byte-identical to the baseline, so the previously successful full Go gate is reused. The [stand README](scripts/postgres674/README.md) gives the current output contract and focused commands. Formatter/diff and privacy checks cover the repair candidate.

The next step is narrow independent re-review of R1 and this qualification, followed by parent acceptance/report/acknowledgment. This repair does not establish full issue completion, production readiness, private-corpus quality or intended-client performance.
