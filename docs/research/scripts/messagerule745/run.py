#!/usr/bin/env python3
"""Run every query of #745 through each arm on a copy of the base.

    run.py QUERIES.jsonl BIN_DIR BASE.db STATE_DIR OUT.json

Each arm is `memcue search -json -n 5` with the query's own -in, -kind, -since and -expired and its session
directory as -cwd, so scope resolution, the widening of #712 and the tie order are the CLI's:

    current   memcue-current            the default today
    one-term  memcue-one-term           messageTerms = 1, floor 0.45
    search    memcue-search             no query is a message: floor 0.6, no two-term rule
    wide      memcue-current -min-score 0   reference only

The environment holds no MEMCUE_* variable but MEMCUE_FACTS (the copy) and MEMCUE_STATE_DIR, so no run reaches the
live server, the search log or a usage log. The facts table is hashed before and after: a search must not write.
"""
import hashlib
import json
import os
import sqlite3
import subprocess
import sys

ARMS = [("current", "memcue-current", []), ("one-term", "memcue-one-term", []), ("search", "memcue-search", []),
        ("wide", "memcue-current", ["-min-score", "0"])]


def facts_hash(db):
    con = sqlite3.connect(f"file:{db}?mode=ro", uri=True)
    h = hashlib.sha256()
    for row in con.execute("select id, version, status, content from facts order by id, version"):
        h.update(json.dumps(row).encode())
    n = con.execute("select count(distinct id) from facts where status='active'").fetchone()[0]
    con.close()
    return h.hexdigest(), n


def main():
    queries_path, bin_dir, base, state, out_path = sys.argv[1:6]
    env = {k: v for k, v in os.environ.items() if not k.startswith("MEMCUE_")}
    env.update(MEMCUE_FACTS=base, MEMCUE_STATE_DIR=state)
    os.makedirs(state, exist_ok=True)
    before, active = facts_hash(base)
    queries = [json.loads(line) for line in open(queries_path)]
    results = []
    for q in queries:
        flags = []
        for name in ("in", "kind", "since"):
            value = q[name]
            if name == "in" and "project" in [p.strip() for p in value.split(",")]:
                # -in project is gone since #712; the CLI points to the default, this repository, host and global.
                value = ""
            if value:
                flags += ["-" + name, value]
        if q["expired"]:
            flags.append("-expired")
        row = {"qid": q["qid"], "arms": {}}
        for arm, binary, extra in ARMS:
            cmd = [os.path.join(bin_dir, binary), "search", "-json", "-n", "5", "-cwd", q["cwd"], *flags, *extra,
                   "--", q["text"]]
            r = subprocess.run(cmd, env=env, capture_output=True, text=True, stdin=subprocess.DEVNULL, timeout=60)
            if r.returncode != 0:
                raise SystemExit(f"{q['qid']} {arm}: exit {r.returncode}: {r.stderr.strip()[:300]}")
            answer = json.loads(r.stdout)
            row["arms"][arm] = {"facts": [f["id"] for f in answer["facts"]],
                                "content": {f["id"]: f["content"] for f in answer["facts"]},
                                "widened": bool(answer.get("widened"))}
        results.append(row)
    after, _ = facts_hash(base)
    if before != after:
        raise SystemExit("the facts table changed during the run")
    json.dump({"facts_sha256": before, "active_facts": active, "results": results}, open(out_path, "w"),
              ensure_ascii=False, indent=1)
    print(json.dumps({"queries": len(results), "active_facts": active, "facts_sha256": before}))


if __name__ == "__main__":
    main()
