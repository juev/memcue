#!/usr/bin/env python3
"""How stable is the pair of facts the judge serves? Score 0.5.3 against the margin of #298.

The words give the same candidates for the same prompt; only the judge's numbers move between runs, so a prompt whose
served pair changes across identical runs was decided by that noise. The script runs one prompt N times per arm and
reports how many prompts got more than one distinct pair.

Needs the judge endpoint in MEMCUE_JUDGE_URL / _MODEL / _KEY and a copy of the base (never the live one: `recall`
opens and may migrate it).

    python3 judge_cut.py --prompts prompts.json --base /tmp/facts-copy.db \\
        --old ./memcue-0.5.3 --new ./memcue-margin --runs 6

`prompts.json` is a JSON array of prompt strings; the research note used 12 real user messages.
"""
import argparse
import json
import os
import statistics
import subprocess
import sys
import tempfile
import time


def served(binary, prompt, env_extra, state):
    env = {k: v for k, v in os.environ.items()
           if not k.startswith("MEMCUE_") or k in ("MEMCUE_JUDGE_URL", "MEMCUE_JUDGE_MODEL", "MEMCUE_JUDGE_KEY")}
    env.update(env_extra, XDG_STATE_HOME=state)
    started = time.perf_counter()
    run = subprocess.run([binary, "recall", "-cwd", os.getcwd(), "-explain", prompt],
                         capture_output=True, text=True, env=env)
    took = (time.perf_counter() - started) * 1000
    facts, section = [], None
    for line in run.stdout.splitlines():
        if line.startswith("final:"):
            section = "final"
            continue
        if not line.startswith("  "):
            section = None
            continue
        if section == "final" and line.strip().startswith("served"):
            facts.append(line.split("|")[0].split()[-1])
    return facts, took


def arm(name, binary, prompts, base, state, runs, margin=None):
    extra = {"MEMCUE_FACTS": base}
    if margin is not None:
        extra["MEMCUE_JUDGE_RERANK_MARGIN"] = str(margin)
    rows, times = [], []
    for i, prompt in enumerate(prompts, 1):
        seen = [served(binary, prompt, extra, state) for _ in range(runs)]
        pairs = {tuple(facts) for facts, _ in seen}
        times.append(statistics.median(took for _, took in seen))
        rows.append({"id": i, "pairs": sorted(pairs), "runs": [list(p) for p in pairs]})
        print(f"  {name} #{i:2d}: {len(pairs)} distinct pair(s) over {runs} runs: {sorted(pairs)}", flush=True)
    unstable = sum(1 for r in rows if len(r["pairs"]) > 1)
    return {"name": name, "runs": runs, "unstable": unstable, "p50_ms": round(statistics.median(times)),
            "rows": rows}


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--prompts", required=True, help="JSON array of prompt strings")
    ap.add_argument("--base", required=True, help="copy of the fact base")
    ap.add_argument("--old", required=True, help="binary of the release to beat, e.g. 0.5.3")
    ap.add_argument("--new", required=True, help="binary with the margin")
    ap.add_argument("--runs", type=int, default=6)
    ap.add_argument("--margin", type=float, default=None, help="override MEMCUE_JUDGE_RERANK_MARGIN for the new arm")
    ap.add_argument("-json", action="store_true", help="print the result as JSON instead of a table")
    args = ap.parse_args()
    if not all(os.environ.get(k) for k in ("MEMCUE_JUDGE_URL", "MEMCUE_JUDGE_MODEL", "MEMCUE_JUDGE_KEY")):
        sys.exit("set MEMCUE_JUDGE_URL, MEMCUE_JUDGE_MODEL and MEMCUE_JUDGE_KEY: this measures the judge")
    prompts = json.load(open(args.prompts))
    with tempfile.TemporaryDirectory() as state:
        old = arm("0.5.3", args.old, prompts, args.base, state, args.runs)
        new = arm(f"margin {args.margin if args.margin is not None else 'default'}",
                  args.new, prompts, args.base, state, args.runs, args.margin)
    result = {"prompts": len(prompts), "arms": [old, new]}
    if args.json:
        print(json.dumps(result, ensure_ascii=False, indent=1))
        return
    print(f"\n{len(prompts)} prompts, {args.runs} runs each")
    for a in (old, new):
        print(f"  {a['name']:>16}: unstable on {a['unstable']} prompts, median recall {a['p50_ms']} ms")
    changed = sum(1 for o, n in zip(old["rows"], new["rows"])
                  if {tuple(p) for p in o["pairs"]} != {tuple(p) for p in n["pairs"]})
    print(f"  the two arms disagree on {changed} prompts")
    print(f"\n{'prompt':>7}  {'0.5.3':>2}  {'margin':>6}")
    for o, n in zip(old["rows"], new["rows"]):
        print(f"{o['id']:>7}  {len(o['pairs']):>6}  {len(n['pairs']):>6}")


if __name__ == "__main__":
    main()
