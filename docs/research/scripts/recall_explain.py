"""Run `memcue recall -explain` for every prompt of a gold set and keep the raw output, one file per prompt.

  python3 -I recall_explain.py MEMCUE_BINARY eval.json OUT_DIR
"""
import json, os, subprocess, sys
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path

binary, eval_path, out_dir = sys.argv[1], sys.argv[2], Path(sys.argv[3])
out_dir.mkdir(parents=True, exist_ok=True)
prompts = json.load(open(eval_path))["prompts"]

def one(p):
    target = out_dir / f"{p['id']}.txt"
    if target.exists():
        return
    r = subprocess.run([binary, "recall", "-explain", "-cwd", p.get("cwd") or ".", p["text"]],
                       capture_output=True, text=True, timeout=120)
    target.write_text(r.stdout + ("\nSTDERR: " + r.stderr if r.stderr else ""))

with ThreadPoolExecutor(6) as pool:
    list(pool.map(one, prompts))
print(len(list(out_dir.glob("*.txt"))))
