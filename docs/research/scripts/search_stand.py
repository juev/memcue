#!/usr/bin/env python3
"""Score the pull search (0.8.0) on a set of real prompts.

Since 0.8.0 the agent searches the base itself with ``memcue search``: there is
no recall, no judge and no ``eval`` verb, so the only cheap way to see what a
change did to the found facts is to run the search command over prompts whose
answers are known.

The prompts file is a JSON list of objects::

    [{"prompt": "where does the service write its log", "labels": "UNRNN"}]

``labels`` is optional and one character per fact of the search result, in rank
order: ``U`` the fact answers the prompt, ``R`` it is on the topic but does not
answer, ``N`` it is unrelated. With labels the stand reports the fact-level
shares and how many prompts keep a useful fact in the top k; without them it
reports only coverage, latency and size, so a run without labels still catches a
search that broke.

    python3 docs/research/scripts/search_stand.py --prompts prompts.json \\
        --base /path/to/facts.db --sweeps 2,5,10

The state directory is a scratch path: probes must not write into the live one.
"""

from __future__ import annotations

import argparse
import json
import os
import statistics
import subprocess
import sys
import tempfile
import time
from pathlib import Path


def search(binary: str, base: str, state: str, cwd: str, prompt: str, n: int) -> tuple[list[dict], float]:
    env = dict(os.environ, MEMCUE_FACTS=base, MEMCUE_STATE_DIR=state)
    started = time.perf_counter()
    proc = subprocess.run(
        [binary, "search", "-json", "-n", str(n), "-cwd", cwd, "--", prompt],
        capture_output=True, text=True, env=env,
    )
    ms = (time.perf_counter() - started) * 1000
    if proc.returncode != 0:
        raise SystemExit(f"search failed for {prompt[:60]!r}: {proc.stderr.strip()}")
    out = proc.stdout.strip()
    if not out:
        return [], ms
    facts = json.loads(out)
    return facts if isinstance(facts, list) else [], ms


def grade(runs: list[dict], k: int) -> dict:
    """The numbers for one top-k: what the agent gets if it reads k facts."""
    useful_prompts = useful = related = noise = 0
    chars = 0
    for run in runs:
        labels = (run.get("labels") or "")[:k]
        if not labels:
            continue
        labels = labels[:len(run["facts"])]
        useful_prompts += 1 if "U" in labels else 0
        useful += labels.count("U")
        related += labels.count("R")
        noise += labels.count("N")
        chars += sum(len(f.get("content") or "") for f in run["facts"][:len(labels)])
    graded = useful + related + noise
    return {
        "k": k,
        "prompts": len(runs),
        "prompts_with_useful": useful_prompts,
        "useful": useful,
        "related": related,
        "noise": noise,
        "useful_share": useful / graded if graded else 0.0,
        "noise_share": noise / graded if graded else 0.0,
        "chars": chars,
        "tokens": round(chars / 3.2),
    }


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--prompts", required=True, help="JSON list of {prompt, labels?}")
    ap.add_argument("--base", required=True, help="facts.db of the base to search")
    ap.add_argument("--binary", default="memcue", help="memcue binary (default: memcue from PATH)")
    ap.add_argument("--cwd", default=".", help="the session directory the search is told about")
    ap.add_argument("--state", default="", help="state directory for the probe logs (default: a temp directory)")
    ap.add_argument("--sweeps", default="", help="top-k values to compare, e.g. 2,5,10 (default: --n only)")
    ap.add_argument("--repeat", type=int, default=1, help="searches per prompt, the slowest one is reported")
    ap.add_argument("--json", action="store_true", help="print JSON instead of markdown")
    args = ap.parse_args()

    prompts = json.loads(Path(args.prompts).read_text(encoding="utf-8"))
    ks = [int(k) for k in args.sweeps.split(",") if k.strip()] or [10]
    state = args.state or tempfile.mkdtemp(prefix="memcue-stand-")

    runs: list[dict] = []
    for item in prompts:
        prompt = item["prompt"].strip().replace("\n", " ")
        facts, ms = [], 0.0
        for _ in range(max(1, args.repeat)):
            got, took = search(args.binary, args.base, state, args.cwd, prompt, max(ks))
            if len(got) >= len(facts):
                facts, ms = got, max(ms, took)
        runs.append({"prompt": prompt, "labels": item.get("labels"), "facts": facts, "ms": ms})

    latencies = [r["ms"] for r in runs]
    report = {
        "prompts": len(runs),
        "searches": len(runs) * args.repeat,
        "base": args.base,
        "covered": sum(1 for r in runs if r["facts"]),
        "ms_median": statistics.median(latencies) if latencies else 0.0,
        "ms_p90": sorted(latencies)[int(len(latencies) * 0.9) - 1] if latencies else 0.0,
        "sweeps": [grade(runs, k) for k in ks],
    }
    if args.json:
        print(json.dumps(report, ensure_ascii=False, indent=1))
        return 0

    print(f"**{report['prompts']} prompts, {report['searches']} searches.** "
          f"Prompt got a fact in {report['covered']} of {report['prompts']}; "
          f"search p50 {report['ms_median']:.1f} ms, p90 {report['ms_p90']:.1f} ms.")
    print()
    print("| top-k | prompts with a useful fact | useful | related | noise | useful share | noise share | chars | ~tokens |")
    print("|---|---|---|---|---|---|---|---|---|")
    for row in report["sweeps"]:
        if not row["useful"] and not row["noise"]:
            print(f"| {row['k']} | — | — | — | — | — | — | {row['chars']} | {row['tokens']} |")
            continue
        print(f"| {row['k']} | {row['prompts_with_useful']}/{row['prompts']} | {row['useful']} | {row['related']} | "
              f"{row['noise']} | {row['useful_share']:.0%} | {row['noise_share']:.0%} | {row['chars']} | {row['tokens']} |")
    print()
    print(f"Labels are one rater's, from the fact text alone; without `labels` the table shows characters only.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
