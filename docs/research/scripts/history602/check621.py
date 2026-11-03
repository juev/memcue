#!/usr/bin/env python3
"""Check that `memcue sessions` reproduces the windows of sessions.py on the #602 queries (#621).

Run: python3 -I check621.py --queries Q.jsonl --results RESULTS.jsonl [--memcue memcue] [--k 5]

Q.jsonl is the queries file of sessions.py; RESULTS.jsonl the results.jsonl of its text arm over the same transcripts.
For each query the command runs with -before the query time, -session its excluded sessions and -n k, and its
passages are compared with the windows of the parser: the same text in the same order. The transcripts are read live,
so a query whose windows the parser cut before a transcript was deleted or compacted can differ; the output counts
those apart, by the sessions the windows name. stdout carries counts only; no window leaves the private data.
"""
import argparse
import json
import subprocess
import sys


def main(argv=None) -> int:
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    ap.add_argument("--queries", required=True)
    ap.add_argument("--results", required=True)
    ap.add_argument("--memcue", default="memcue")
    ap.add_argument("--k", type=int, default=5)
    a = ap.parse_args(argv)
    queries = {}
    for line in open(a.queries, encoding="utf-8"):
        if line.strip():
            q = json.loads(line)
            queries[q["qid"]] = q
    results = {}
    for line in open(a.results, encoding="utf-8"):
        if line.strip():
            r = json.loads(line)
            results[r["qid"]] = r
    same = differ = 0
    details = []
    for qid, r in sorted(results.items()):
        q = queries[qid]
        cmd = [a.memcue, "sessions", "-json", "-n", str(a.k), "-before", q["time"]]
        if q.get("exclude_sessions"):
            cmd += ["-session", ",".join(q["exclude_sessions"])]
        cmd += ["--", q["query"]]
        out = subprocess.run(cmd, capture_output=True, text=True, check=True).stdout
        got = json.loads(out)
        want_terms = r["terms"]
        texts_got = [p["text"] for p in got["passages"]]
        texts_want = [w["text"] for w in r["windows"]]
        if got["terms"] != want_terms:
            details.append(f"{qid}: terms differ")
            differ += 1
        elif texts_got == texts_want:
            same += 1
        else:
            # Which windows are missing, and whether the sessions they come from are still on disk.
            sessions_got = {p["session"] for p in got["passages"]}
            missing = [w for w in r["windows"] if w["text"] not in texts_got]
            extra = [p for p in got["passages"] if p["text"] not in texts_want]
            details.append(f"{qid}: {len(texts_want)} parser windows, {len(texts_got)} command passages, "
                           f"{len(missing)} missing (sessions still seen: "
                           f"{sum(1 for w in missing if w['session'] in sessions_got)}), {len(extra)} extra")
            differ += 1
    print(f"queries {len(results)}: same windows {same}, differ {differ}")
    for d in details:
        print(" ", d)
    return 0 if differ == 0 else 1


if __name__ == "__main__":
    sys.exit(main())
