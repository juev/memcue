#!/usr/bin/env python3
"""Judge every think/quick run against its session's claude run, then sum the verdicts per mode and run.

    python3 -I extract_judge_all.py RUNS_DIR
"""
import collections
import glob
import json
import os
import subprocess
import sys
from concurrent.futures import ThreadPoolExecutor

root = sys.argv[1]
here = os.path.dirname(os.path.abspath(__file__))


def nfacts(path):
    try:
        return len(json.load(open(path, encoding="utf-8")).get("facts") or [])
    except (OSError, ValueError):
        return -1


jobs = []
for ref in sorted(glob.glob(os.path.join(root, "*-claude-1", "out.json"))):
    name = os.path.basename(os.path.dirname(ref))[: -len("-claude-1")]
    for mode in ("think", "quick"):
        for run in (1, 2):
            cand = os.path.join(root, "%s-%s-%d" % (name, mode, run), "out.json")
            jobs.append((name, mode, run, ref, cand))


def judge(job):
    name, mode, run, ref, cand = job
    out = os.path.join(root, "%s-%s-%d" % (name, mode, run), "verdict.json")
    nr, nc = nfacts(ref), nfacts(cand)
    if nr < 0 or nc < 0:
        return job, {"error": "missing output"}
    if nr == 0 and nc == 0:
        return job, {"ref": [], "cand": [], "n_ref": 0, "n_cand": 0, "cost": 0}
    if not os.path.exists(out):
        r = subprocess.run([sys.executable, "-I", os.path.join(here, "extract_judge.py"), ref, cand], capture_output=True,
                           text=True)
        if r.returncode != 0:
            return job, {"error": r.stderr[-300:]}
        open(out, "w", encoding="utf-8").write(r.stdout)
    return job, json.load(open(out, encoding="utf-8"))


with ThreadPoolExecutor(max_workers=6) as pool:
    results = list(pool.map(judge, jobs))

tot = collections.defaultdict(lambda: collections.Counter())
cost = 0.0
for (name, mode, run, ref, cand), v in results:
    k = (mode, run)
    if "error" in v:
        tot[k]["errors"] += 1
        print("error", name, mode, run, v["error"], file=sys.stderr)
        continue
    cost += v.get("cost", 0)
    tot[k]["ref"] += v["n_ref"]
    tot[k]["covered"] += sum(1 for r in v["ref"] if r.get("match"))
    tot[k]["cand"] += v["n_cand"]
    for c in v["cand"]:
        tot[k][c["verdict"]] += 1
    o = json.load(open(cand, encoding="utf-8"))
    tot[k]["output_tokens"] += (o.get("tokens") or {}).get("output", 0)
    tot[k]["sessions_with_facts"] += 1 if v["n_cand"] else 0

print("mode  run  covered/ref  cand  same useful junk  output_tokens  errors")
for k in sorted(tot):
    t = tot[k]
    print("%-5s %d    %3d/%-3d     %4d  %4d %6d %4d  %13d  %d" % (k[0], k[1], t["covered"], t["ref"], t["cand"], t["same"],
                                                              t["useful"], t["junk"], t["output_tokens"], t["errors"]))
print("judge cost $%.2f" % cost)
