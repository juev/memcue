#!/usr/bin/env python3
"""Check the result JSON files of run.sh against what docs/research/git-growth-530-2026-10.md asserts (#530).

    python3 -I docs/research/scripts/gitgrowth/check.py RESULTS

Two kinds of assertion. Invariants are the properties the report relies on: the round trip compares equal, no
interrupted import leaves an unreadable target, every merge round converges without loss. Findings are the
deterministic defects the report describes: the duplicate after a stale merge, the retired fact a later correction
reopens, the evidence memcue dump leaves out. If a finding stops reproducing, the report is stale and the check fails
just the same. Counts that vary between runs (how many raw copies were malformed, timings, sizes) are not asserted.

Exit status 0 when every assertion holds, 1 otherwise, each failure printed on its own line.
"""

from __future__ import annotations

import json
import sys
from pathlib import Path

REQUIRED = ("synthetic", "roundtrip", "interrupt", "race", "semantics")
ROUND_INVARIANTS = ("sqlite_both_orders_equal", "events_both_orders_equal", "sqlite_equals_events",
                    "sqlite_no_lost_or_invented_versions", "events_no_lost_or_invented_versions", "versions_contiguous",
                    "index_matches_live_facts")
SEMANTIC_ROUNDS = 12
SYNTHETIC_VARIANTS = {"sqlite-every", "sqlite-batch5", "sqlite-batch20", "sqlite-vacuum", "text-single",
                      "text-perfact", "text-events"}


def fraction(s: str) -> tuple[int, int]:
    a, b = s.split("/")
    return int(a), int(b)


def check_repacks(name: str, d: dict, fail: list[str]) -> None:
    for vname, v in d["variants"].items():
        for rname, r in v["repacked"].items():
            if r.get("exit") != 0:
                fail.append(f"{name}: {vname} {rname} exited {r.get('exit')}")


def check(root: Path) -> list[str]:
    fail: list[str] = []
    data = {}
    for name in REQUIRED:
        p = root / f"{name}.json"
        try:
            data[name] = json.loads(p.read_text())
        except (OSError, ValueError) as e:
            fail.append(f"{name}: cannot read {p.name}: {e}")
    if fail:
        return fail

    syn = data["synthetic"]
    if set(syn["variants"]) != SYNTHETIC_VARIANTS:
        fail.append(f"synthetic: variants {sorted(syn['variants'])}, want {sorted(SYNTHETIC_VARIANTS)}")
    check_repacks("synthetic", syn, fail)
    real = root / "real.json"
    if real.exists():
        r = json.loads(real.read_text())
        check_repacks("real", r, fail)
        if r["variants"]["sqlite-every"]["commits"] != r["source"]["distinct_blobs"]:
            fail.append("real: the replay does not have one commit per distinct blob")

    rt = data["roundtrip"]
    for k in ("rows_equal", "jsonl_bytes_equal", "index_stats_equal", "card_equal", "memcue_dump_equal",
              "truncated_refused", "conflict_markers_refused"):
        if rt.get(k) is not True:
            fail.append(f"roundtrip: {k} is {rt.get(k)!r}, want true")
    same, total = fraction(rt["search_same_results"])
    if total == 0 or same != total:
        fail.append(f"roundtrip: search_same_results {rt['search_same_results']}, want all equal")
    found, n = fraction(rt["memcue_dump_has_evidence"])
    if n == 0 or found != 0:
        fail.append(f"roundtrip: memcue_dump_has_evidence {rt['memcue_dump_has_evidence']}, the report says dump "
                    "drops evidence (want 0/N, N > 0)")

    it = data["interrupt"]
    if it["target_other_or_unreadable"] != 0:
        fail.append(f"interrupt: {it['target_other_or_unreadable']} targets neither the previous nor the new base")
    if it["target_previous_base"] + it["target_complete"] != it["kills"]:
        fail.append("interrupt: previous + complete targets do not add up to the kills")
    if it.get("final_import_complete") is not True:
        fail.append("interrupt: the final import did not complete")

    rc = data["race"]
    if rc["raw_copies"] == 0:
        fail.append("race: no raw copies were taken")
    if rc["raw_copy_ok"] + rc["raw_copy_malformed"] + rc["raw_copy_other_error"] != rc["raw_copies"]:
        fail.append("race: copy verdicts do not add up to the copies")
    if rc["snapshot_exports_ok"] != rc["snapshot_exports"]:
        fail.append(f"race: {rc['snapshot_exports'] - rc['snapshot_exports_ok']} snapshot exports were not whole")

    sem = data["semantics"]
    rounds = sem["rounds"]
    if len(rounds) != SEMANTIC_ROUNDS:
        fail.append(f"semantics: {len(rounds)} rounds, want {SEMANTIC_ROUNDS}")
    for r in rounds:
        where = f"semantics: {r['scenario']}/{r['round']}"
        for k in ROUND_INVARIANTS:
            if r.get(k) is not True:
                fail.append(f"{where}: {k} is {r.get(k)!r}, want true")
        for e in r.get("errors") or []:
            fail.append(f"{where}: error {e}")
    if not any(r.get("remerge_changes_nothing") is False for r in rounds):
        fail.append("semantics: no round shows the duplicate of a repeated Merge the report describes")
    reopened = [r for r in rounds if r["scenario"] == "retire-then-correct"]
    if not reopened or (reopened[0].get("watched_fact") or {}).get("status") != "active":
        fail.append("semantics: retire-then-correct does not end active, as the report says")
    st = sem["stale"]
    for e in st.get("errors") or []:
        fail.append(f"semantics: stale: error {e}")
    if st["sqlite_rows"] != st["rows_expected"] + 1 or st["sqlite_duplicate_versions"] != 1:
        fail.append(f"semantics: stale: SQLite {st['sqlite_rows']} rows, {st['sqlite_duplicate_versions']} "
                    f"duplicates; the report says {st['rows_expected'] + 1} and 1")
    if st["events_rows"] != st["rows_expected"] or st["events_no_lost_or_invented_versions"] is not True:
        fail.append("semantics: stale: the event log does not give the expected rows")
    return fail


def main() -> None:
    if len(sys.argv) != 2:
        sys.exit("usage: check.py RESULTS")
    fail = check(Path(sys.argv[1]))
    for f in fail:
        print(f"FAIL {f}")
    if fail:
        sys.exit(1)
    print("all assertions hold")


if __name__ == "__main__":
    main()
