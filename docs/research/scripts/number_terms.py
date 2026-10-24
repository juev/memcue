#!/usr/bin/env python3
"""Measure numeric terms in memcue recall: the same binary, two arms, one base.

    MEMCUE_BIN=./bin/memcue ./number_terms.py --base ~/.claude/memory/facts.db --items items.json

Arms:
  off — MEMCUE_NUMBER_TERMS unset, index built word-only (the shipped behaviour);
  on  — MEMCUE_NUMBER_TERMS=1, index rebuilt from the same rows so only the tokens differ.

The base is copied; the live one is opened read-only for the copy and never for a query. Every prompt gets a fresh
session id: a fact is served once per session, and a repeated id answers with the next facts or with nothing, which
looks like a regression and is not. Scored on what the model reads — the served facts — never on the raw JSON:
the response echoes the query, so scoring it hands a free hit to any prompt that contains its own answer.

Anchor prompts are built from the base: for every fact holding a version, `<version>` alone and `<topic> <version>`.
`--items` adds ordinary prompts as a JSON list of {id, q, expect: [..]}, scored the same way; run the off arm twice
with different --tag values to size the judge noise before calling a difference an effect.
"""
import argparse
import json
import os
import random
import re
import shutil
import sqlite3
import subprocess
import sys
import time
from pathlib import Path

TOPICS = ["memcue", "hermes", "holographic", "forgejo", "state.db", "go", "python", "dream", "vet", "caddy", "git"]


def copy_base(src: Path, dst: Path) -> None:
    if dst.exists():
        os.remove(dst)
    s, d = sqlite3.connect(f"file:{src}?mode=ro", uri=True), sqlite3.connect(dst)
    s.backup(d)
    d.close()
    s.close()


def load_facts(path: Path):
    c = sqlite3.connect(f"file:{path}?mode=ro", uri=True)
    rows = c.execute("""select f.id, f.content from facts f
        join (select id, max(version) v from facts group by id) m on m.id = f.id and m.v = f.version
        where f.status = 'active' and f.review in ('', 'provisional')""").fetchall()
    c.close()
    return rows


def anchor_items(facts, limit: int):
    rx = re.compile(r"\b(v?\d+\.\d+(?:\.\d+)?)\b")
    seen, out = set(), []
    for fid, content in facts:
        low = content.lower()
        topic = next((t for t in TOPICS if t in low), None)
        for m in rx.finditer(content):
            ver = m.group(1)
            if ver in seen or len(ver) < 4 or not topic:
                continue
            # the version has to occur as a whole token, or the prompt names a version the fact does not hold
            if not re.search(r"(?<![\w.])" + re.escape(ver) + r"(?![\w.])", content):
                continue
            seen.add(ver)
            out.append({"id": f"anchor-{len(out)}", "q": f"{topic} {ver}", "bare": ver, "expect": [ver], "src": fid})
            break
        if len(out) >= limit:
            break
    return out


def env_for(db: Path, state: Path, numbers: bool) -> dict:
    e = dict(os.environ, MEMCUE_FACTS=str(db), XDG_STATE_HOME=str(state), MEMCUE_EXTRACT="0", MEMCUE_RECALL_LOG="0")
    e.pop("MEMCUE_NUMBER_TERMS", None)
    if numbers:
        e["MEMCUE_NUMBER_TERMS"] = "1"
    return e


def resolve(text: str, facts) -> list[str]:
    """Map served lines back to fact ids: the injected text carries content, not ids."""
    out = []
    for line in text.split("\n"):
        if not line.startswith("- "):
            continue
        body = " ".join(line[2:].split(" (learnt:")[0].lower().split())
        for fid, content in facts:
            if " ".join(content.lower().split()).startswith(body[:120]):
                out.append(fid)
                break
    return out


def run(binary: str, prompt: str, expect, tag: str, db: Path, state: Path, numbers: bool, facts):
    payload = json.dumps({"session_id": tag, "cwd": os.getcwd(), "prompt": prompt})
    t0 = time.perf_counter()
    p = subprocess.run([binary, "hook"], input=payload, capture_output=True, text=True,
                       env=env_for(db, state, numbers), timeout=180)
    ms = (time.perf_counter() - t0) * 1000
    try:
        text = json.loads(p.stdout)["hookSpecificOutput"]["additionalContext"]
    except Exception:
        text = ""
    low = " ".join(text.lower().split())
    return {"chars": len(text), "ms": round(ms, 1), "facts": resolve(text, facts),
            "hit": any(e.lower() in low for e in expect)}


def arm(binary, items, numbers, db, state, facts, tag):
    rows = []
    for it in items:
        r = run(binary, it["q"], it["expect"], f"{tag}-{it['id']}", db, state, numbers, facts)
        r["id"] = it["id"]
        r["src_hit"] = it.get("src") in r["facts"] if it.get("src") else None
        rows.append(r)
    return rows


def summary(name, rows):
    hits = sum(r["hit"] for r in rows)
    print(f"{name:26} hits {hits}/{len(rows)}  chars {sum(r['chars'] for r in rows)}  "
          f"p50 {sorted(r['ms'] for r in rows)[len(rows) // 2]:.0f} ms")
    return {"name": name, "hits": hits, "n": len(rows), "chars": sum(r["chars"] for r in rows)}


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--base", required=True, help="live fact base to copy")
    ap.add_argument("--bin", default=os.environ.get("MEMCUE_BIN", "memcue"))
    ap.add_argument("--work", default="cache/number-terms")
    ap.add_argument("--items", help="JSON list of {id, q, expect} for ordinary prompts")
    ap.add_argument("--anchors", type=int, default=12)
    ap.add_argument("--tag", default=time.strftime("%H%M%S"))
    a = ap.parse_args()

    work = Path(a.work).resolve()
    work.mkdir(parents=True, exist_ok=True)
    state = work / "state"
    if state.exists():
        shutil.rmtree(state)
    off_db, on_db = work / "base-off.db", work / "base-on.db"
    copy_base(Path(a.base), off_db)
    copy_base(Path(a.base), on_db)
    # empty the derived index so the on arm rebuilds it with numeric terms on open
    c = sqlite3.connect(on_db)
    c.execute("delete from fact_index")
    c.commit()
    c.close()

    facts = load_facts(off_db)
    anchors = anchor_items(facts, a.anchors)
    bare = [{"id": a_["id"] + "-bare", "q": a_["bare"], "expect": a_["expect"], "src": a_["src"]} for a_ in anchors]
    items = json.loads(Path(a.items).read_text()) if a.items else []

    print(f"anchors: {len(anchors)} (version-only and topic+version each), ordinary prompts: {len(items)}")
    results = {}
    for label, prompts in (("bare", bare), ("anchors", anchors), ("ordinary", items)):
        if not prompts:
            continue
        print(f"\n--- {label} ---")
        off = arm(a.bin, prompts, False, off_db, state, facts, a.tag + "off" + label)
        on = arm(a.bin, prompts, True, on_db, state, facts, a.tag + "on" + label)
        results[label] = {"off": off, "on": on, "summary": [summary("numbers OFF", off), summary("numbers ON", on)]}
        for o, n in zip(off, on):
            print(f"  {prompts[off.index(o)]['q'][:34]:34} off {int(o['hit'])}/src {int(bool(o['src_hit']))}"
                  f"  on {int(n['hit'])}/src {int(bool(n['src_hit']))}")
        same = sum(1 for o, n in zip(off, on) if o["facts"] == n["facts"])
        print(f"  identical fact sets: {same}/{len(prompts)}")

    (work / "results.json").write_text(json.dumps(results, ensure_ascii=False, indent=1))
    print(f"\nwritten {work / 'results.json'}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
