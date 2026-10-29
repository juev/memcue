#!/usr/bin/env python3
"""Print the tables of docs/research/git-growth-530-2026-10.md from the result JSON files of run.sh."""

from __future__ import annotations

import json
import sys
from pathlib import Path

MiB = 2**20


def mib(n: float | None) -> str:
    return "—" if n is None else f"{n / MiB:.2f}"


def sec(n: float | None) -> str:
    return "—" if n is None else f"{n:.1f}"


def rss(kib: int | None) -> str:
    return "—" if not kib else f"{kib / 1024:.0f}"


def variants_table(d: dict) -> None:
    print("| variant | commits | loose, MiB | after gc, MiB | gc, s | gc, peak RSS MiB | after gc --aggressive, MiB "
          "| aggressive, s | aggressive, peak MiB | push of all commits from loose, MiB | push of all commits after gc, MiB "
          "| clone after gc, MiB |")
    print("|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|")
    for name, v in d["variants"].items():
        gc = v["repacked"].get("gc", {})
        ag = v["repacked"].get("gc-aggressive", {})
        print(f"| {name} | {v['commits']} | {mib(v['loose']['store']['total_bytes'])} "
              f"| {mib(gc.get('store', {}).get('total_bytes'))} | {sec(gc.get('seconds'))} | {rss(gc.get('peak_rss_kib'))} "
              f"| {mib(ag.get('store', {}).get('total_bytes')) if ag else '—'} | {sec(ag.get('seconds')) if ag else '—'} "
              f"| {rss(ag.get('peak_rss_kib')) if ag else '—'} | {mib(v['loose']['incremental']['bytes_total'])} "
              f"| {mib(gc.get('incremental', {}).get('bytes_total'))} | {mib(gc.get('full_pack', {}).get('bytes'))} |")
    print()
    print("| variant | blobs, logical MiB | push: median thin pack, KiB | push: max, KiB | time of all push packs, s "
          "| pack-objects peak, MiB |")
    print("|---|---:|---:|---:|---:|---:|")
    for name, v in d["variants"].items():
        inc = v["loose"]["incremental"]
        blobs = v["loose"]["objects"].get("blob", {})
        print(f"| {name} | {mib(blobs.get('logical'))} | {inc['bytes_median'] / 1024:.1f} | {inc['bytes_max'] / 1024:.1f} "
              f"| {sec(inc['seconds'])} | {rss(inc['peak_rss_kib'])} |")
    print()
    rows = [(n, v["sync"]) for n, v in d["variants"].items() if "sync" in v]
    rows += [(n + ", transfer.unpackLimit=1", v["sync_keep_packs"]) for n, v in d["variants"].items()
             if "sync_keep_packs" in v]
    if rows:
        print("| variant | push+fetch per commit | fetch received, MiB | remote: loose / pack, MiB | peer: loose / pack, MiB "
              "| push, s | fetch, s | peak, MiB |")
        print("|---|---:|---:|---:|---:|---:|---:|---:|")
        for n, s in rows:
            r, p = s["remote_store"], s["peer_store"]
            print(f"| {n} | {s['pushes']} | {mib(s['fetch_pack_bytes'])} | {mib(r['loose_bytes'])} / {mib(r['pack_bytes'])} "
                  f"| {mib(p['loose_bytes'])} / {mib(p['pack_bytes'])} | {sec(s['push_seconds'])} | {sec(s['fetch_seconds'])} "
                  f"| {rss(s['peak_rss_kib'])} |")
        print()


def main() -> None:
    root = Path(sys.argv[1])
    real = root / "real.json"
    if real.exists():
        d = json.loads(real.read_text())
        s, st = d["source"], d["source_store"]
        print("## Real history (isolated copy)\n")
        print(f"- git: {d['git']}, SQLite (Python): {d['sqlite']}, run date {d['date']}, "
              f"total time {d['elapsed_seconds']:.0f} s")
        print(f"- commits in the repository: {s['all_commits']}; changing the base: {s['commits_touching']} "
              f"(merge: {s['merge_commits']}); distinct blobs of the base: {s['distinct_blobs']}; days: {s['days']}, "
              f"at most {s['max_commits_per_day']} commits a day")
        print(f"- the base's blobs in the source storage: logical {mib(st['logical_bytes'])} MiB, on disk "
              f"{mib(st['disk_bytes'])} MiB, with a delta base: {st['with_delta_base']}; min/max blob "
              f"{mib(st['min_bytes'])}/{mib(st['max_bytes'])} MiB")
        co = st["count_objects"]
        print(f"- `git count-objects -v` of the source repository: loose {co['count']} objects, {co['size'] / 1024:.1f} MiB; "
              f"in packs {co['in-pack']} objects, {co['size-pack'] / 1024:.1f} MiB\n")
        pb = d["per_blob"]
        print("| metric | first version | last | median | p90 | max |")
        print("|---|---:|---:|---:|---:|---:|")
        for k, label, f in [("file_bytes", "SQLite file, MiB", mib), ("vacuum_bytes", "after VACUUM INTO, MiB", mib),
                            ("text_bytes", "JSONL as one file, MiB", mib),
                            ("facts_table_bytes", "facts table pages, MiB", mib),
                            ("facts_table_unused", "of them unused, MiB", mib),
                            ("freelist", "free pages", str), ("rows", "rows (fact versions)", str),
                            ("ids", "facts", str), ("rows_added", "new rows vs the previous version", str),
                            ("pages_new_anywhere", "pages not in the previous version", str)]:
            if k in pb:
                x = pb[k]
                print(f"| {label} | {f(x['first'])} | {f(x['last'])} | {f(x['median'])} | {f(x['p90'])} | {f(x['max'])} |")
        for k, label in [("share_pages_new_anywhere", "share of pages not found anywhere in the previous version"),
                         ("share_pages_changed_at_offset", "share of pages changed at their offset")]:
            if k in pb:
                x = pb[k]
                print(f"| {label} | — | — | {x['median']:.3f} | {x['p90']:.3f} | {x['max']:.3f} |")
        print()
        variants_table(d)
    syn = root / "synthetic.json"
    if syn.exists():
        d = json.loads(syn.read_text())
        w = d["workload"]
        print("## Synthetic workload\n")
        print(f"- seed {w['seed']}: {w['initial_facts']} facts in the first commit, then {w['commits']} commits of "
              f"{w['writes_per_commit']} writes; operations: {json.dumps(w['ops'], ensure_ascii=False)}")
        print(f"- result: {w['rows']} rows, {w['ids']} facts, retired {w['retired_ids']}; SQLite file "
              f"{mib(w['sqlite_file_bytes'])} MiB, after VACUUM {mib(w['sqlite_vacuum_bytes'])} MiB, JSONL "
              f"{mib(w['text_single_bytes'])} MiB; unused in facts pages {w['facts_table_unused_pct']:.0f} %; "
              f"time of writes through store {w['write_seconds']:.1f} s")
        print("- time to prepare the representation for a commit (export) and git add+commit, s, over the whole history: " + "; ".join(
            f"{n} {r['export_seconds']:.1f} + {r['commit_seconds']:.1f}" for n, r in w["repos"].items()) + "\n")
        variants_table(d)
    for name in ("roundtrip", "interrupt", "race"):
        p = root / f"{name}.json"
        if p.exists():
            print(f"## {name}\n\n```json\n{p.read_text().strip()}\n```\n")
    p = root / "semantics.json"
    if p.exists():
        d = json.loads(p.read_text())
        print("## Merge semantics\n")
        print("| scenario | round | SQLite: both orders equal | events: both orders equal | SQLite = events "
              "| SQLite no losses | events no losses | index | repeated Merge changes nothing | fact outcome "
              "| line merge conflicts | merge=union |")
        print("|---|---|---|---|---|---|---|---|---|---|---:|---|")
        yn = lambda b: "yes" if b else "**no**"  # noqa: E731
        for r in d["rounds"]:
            wf = r.get("watched_fact")
            fact = f"{wf['status']} (v{wf['versions']}, {wf['last_author']})" if wf else "—"
            rem = r.get("remerge_changes_nothing")
            rem_s = "—" if rem is None else ("yes" if rem else f"**no** (+{r.get('remerge_extra_rows', 0)} rows)")
            print(f"| {r['scenario']} | {r['round']} | {yn(r['sqlite_both_orders_equal'])} "
                  f"| {yn(r['events_both_orders_equal'])} | {yn(r['sqlite_equals_events'])} "
                  f"| {yn(r['sqlite_no_lost_or_invented_versions'])} | {yn(r['events_no_lost_or_invented_versions'])} "
                  f"| {yn(r['index_matches_live_facts'])} | {rem_s} | {fact} | {r['text_single_line_merge_conflicts']} "
                  f"| {r['text_single_union_merge']} |")
            for e in r.get("errors") or []:
                print(f"|  | error: {e} |||||||||||")
        print(f"\n```json\n{json.dumps(d['stale'], indent=1, ensure_ascii=False)}\n```\n")


if __name__ == "__main__":
    main()
