"""Collect served pairs no label file holds yet into a pool for `gold_set.py label`. The prompts are read from
~/memcue-eval/eval.json.

  python3 -I recall_extra_pool.py OUT.json LABELS.jsonl... -- EXPLAIN_DIR...
"""
import json, re, sys
from pathlib import Path
out = sys.argv[1]
sep = sys.argv.index("--")
label_files, dirs = sys.argv[2:sep], sys.argv[sep + 1:]
ULID = re.compile(r"\b([0-9A-HJKMNP-TV-Z]{26})\b")
labels = {}
for path in label_files:
    for line in open(path):
        r = json.loads(line)
        labels.setdefault(r["id"], {}).update(r["labels"])
prompts = {p["id"]: p for p in json.load(open(Path.home() / "memcue-eval/eval.json"))["prompts"]}
need = {}
for d in dirs:
    for f in Path(d).glob("*.txt"):
        sec = None
        for line in f.read_text().splitlines():
            if line.startswith("final"):
                sec = "final"
                continue
            if sec == "final" and line.startswith("  served"):
                m = ULID.search(line)
                if m and m.group(1) not in labels.get(f.stem, {}):
                    need.setdefault(f.stem, set()).add(m.group(1))
pool = [{"id": pid, "text": prompts[pid]["text"], "needs_memory": None,
         "candidates": [{"id": i, "extra": 1} for i in sorted(ids)]} for pid, ids in sorted(need.items())]
json.dump(pool, open(out, "w"), ensure_ascii=False)
print(len(pool), "prompts", sum(len(p["candidates"]) for p in pool), "pairs")
