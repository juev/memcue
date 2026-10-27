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
    print("| вариант | коммитов | loose, MiB | после gc, MiB | gc, с | gc, пик RSS MiB | после gc --aggressive, MiB "
          "| aggressive, с | aggressive, пик MiB | push всех коммитов из loose, MiB | push всех коммитов после gc, MiB "
          "| clone после gc, MiB |")
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
    print("| вариант | blob'ов, логически MiB | push: медиана thin pack, KiB | push: max, KiB | время всех push-паков, с "
          "| пик pack-objects, MiB |")
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
        print("| вариант | push+fetch на каждый коммит | получено fetch, MiB | remote: loose / pack, MiB | peer: loose / pack, MiB "
              "| push, с | fetch, с | пик, MiB |")
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
        print("## Реальная история (изолированная копия)\n")
        print(f"- git: {d['git']}, SQLite (Python): {d['sqlite']}, дата прогона {d['date']}, "
              f"полное время {d['elapsed_seconds']:.0f} с")
        print(f"- коммитов в репозитории: {s['all_commits']}; меняющих базу: {s['commits_touching']} "
              f"(merge: {s['merge_commits']}); различных blob'ов базы: {s['distinct_blobs']}; дней: {s['days']}, "
              f"максимум {s['max_commits_per_day']} коммитов в день")
        print(f"- blob'ы базы в исходном хранилище: логически {mib(st['logical_bytes'])} MiB, на диске "
              f"{mib(st['disk_bytes'])} MiB, с delta-базой: {st['with_delta_base']}; min/max blob "
              f"{mib(st['min_bytes'])}/{mib(st['max_bytes'])} MiB")
        co = st["count_objects"]
        print(f"- `git count-objects -v` исходного репозитория: loose {co['count']} объектов, {co['size'] / 1024:.1f} MiB; "
              f"в паках {co['in-pack']} объектов, {co['size-pack'] / 1024:.1f} MiB\n")
        pb = d["per_blob"]
        print("| показатель | первая версия | последняя | медиана | p90 | max |")
        print("|---|---:|---:|---:|---:|---:|")
        for k, label, f in [("file_bytes", "файл SQLite, MiB", mib), ("vacuum_bytes", "после VACUUM INTO, MiB", mib),
                            ("text_bytes", "JSONL одним файлом, MiB", mib),
                            ("facts_table_bytes", "страницы таблицы facts, MiB", mib),
                            ("facts_table_unused", "из них пусто, MiB", mib),
                            ("freelist", "свободных страниц", str), ("rows", "строк (версий фактов)", str),
                            ("ids", "фактов", str), ("rows_added", "новых строк к прошлой версии", str),
                            ("pages_new_anywhere", "страниц, которых нет в прошлой версии", str)]:
            if k in pb:
                x = pb[k]
                print(f"| {label} | {f(x['first'])} | {f(x['last'])} | {f(x['median'])} | {f(x['p90'])} | {f(x['max'])} |")
        for k, label in [("share_pages_new_anywhere", "доля страниц, которых нет нигде в прошлой версии"),
                         ("share_pages_changed_at_offset", "доля страниц, изменившихся на своём смещении")]:
            if k in pb:
                x = pb[k]
                print(f"| {label} | — | — | {x['median']:.3f} | {x['p90']:.3f} | {x['max']:.3f} |")
        print()
        variants_table(d)
    syn = root / "synthetic.json"
    if syn.exists():
        d = json.loads(syn.read_text())
        w = d["workload"]
        print("## Синтетическая нагрузка\n")
        print(f"- seed {w['seed']}: {w['initial_facts']} фактов в первом коммите, затем {w['commits']} коммитов по "
              f"{w['writes_per_commit']} записей; операции: {json.dumps(w['ops'], ensure_ascii=False)}")
        print(f"- итог: {w['rows']} строк, {w['ids']} фактов, retired {w['retired_ids']}; файл SQLite "
              f"{mib(w['sqlite_file_bytes'])} MiB, после VACUUM {mib(w['sqlite_vacuum_bytes'])} MiB, JSONL "
              f"{mib(w['text_single_bytes'])} MiB; пусто в страницах facts {w['facts_table_unused_pct']:.0f} %; "
              f"время записей через store {w['write_seconds']:.1f} с")
        print("- время подготовки представления к коммиту (export) и git add+commit, с, за всю историю: " + "; ".join(
            f"{n} {r['export_seconds']:.1f} + {r['commit_seconds']:.1f}" for n, r in w["repos"].items()) + "\n")
        variants_table(d)
    for name in ("roundtrip", "interrupt", "race"):
        p = root / f"{name}.json"
        if p.exists():
            print(f"## {name}\n\n```json\n{p.read_text().strip()}\n```\n")
    p = root / "semantics.json"
    if p.exists():
        d = json.loads(p.read_text())
        print("## Семантика слияний\n")
        print("| сценарий | раунд | SQLite: оба порядка равны | события: оба порядка равны | SQLite = события "
              "| SQLite без потерь | события без потерь | индекс | повторный Merge без изменений | итог факта "
              "| конфликтов line merge | merge=union |")
        print("|---|---|---|---|---|---|---|---|---|---|---:|---|")
        yn = lambda b: "да" if b else "**нет**"  # noqa: E731
        for r in d["rounds"]:
            wf = r.get("watched_fact")
            fact = f"{wf['status']} (v{wf['versions']}, {wf['last_author']})" if wf else "—"
            rem = r.get("remerge_changes_nothing")
            rem_s = "—" if rem is None else ("да" if rem else f"**нет** (+{r.get('remerge_extra_rows', 0)} строк)")
            print(f"| {r['scenario']} | {r['round']} | {yn(r['sqlite_both_orders_equal'])} "
                  f"| {yn(r['events_both_orders_equal'])} | {yn(r['sqlite_equals_events'])} "
                  f"| {yn(r['sqlite_no_lost_or_invented_versions'])} | {yn(r['events_no_lost_or_invented_versions'])} "
                  f"| {yn(r['index_matches_live_facts'])} | {rem_s} | {fact} | {r['text_single_line_merge_conflicts']} "
                  f"| {r['text_single_union_merge']} |")
            for e in r.get("errors") or []:
                print(f"|  | ошибка: {e} |||||||||||")
        print(f"\n```json\n{json.dumps(d['stale'], indent=1, ensure_ascii=False)}\n```\n")


if __name__ == "__main__":
    main()
