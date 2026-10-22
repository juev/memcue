"""Score `recall -explain` outputs against gold labels and name the step where each miss lost its USEFUL facts.

  python3 -I recall_misses.py OUT_DIR labels.jsonl [extra-labels.jsonl ...]
"""
import json, re, sys
from collections import Counter
from pathlib import Path

explain_dir, labels_path = Path(sys.argv[1]), sys.argv[2]
ULID = re.compile(r"\b([0-9A-HJKMNP-TV-Z]{26})\b")
labels = {}
for path in [labels_path] + sys.argv[3:]:
    for line in open(path):
        r = json.loads(line)
        # A later file only adds labels of pairs the gold set did not have; it never overrides the gold set.
        for k, v in r["labels"].items():
            labels.setdefault(r["id"], {}).setdefault(k, v)

def parse(text):
    sec, out, stopped = None, {"lexical": {}, "vector": {}, "judge": {}, "final": {}}, ""
    for line in text.splitlines():
        if line.startswith("stopped:"):
            stopped = line
        elif line.startswith("lexical:"):
            sec = "lexical"
        elif line.startswith("vector"):
            sec = "vector"
        elif line.startswith("judge"):
            sec = "judge"
        elif line.startswith("final"):
            sec = "final"
        elif line.startswith("  ") and sec:
            m = ULID.search(line)
            if m:
                head = line[:m.start()].strip()
                verdict = re.split(r"\s{2,}|\s[\d.]+(\s|$)", head)[0].strip()
                score = re.findall(r"\s(\d\.\d\d)\b", " " + head)
                out[sec][m.group(1)] = (verdict, float(score[0]) if score else None)
    return out, stopped

served_useful1 = served_useful2 = 0
served = Counter()
miss = Counter()
examples = {}
prompts_with_useful = 0
for f in sorted(explain_dir.glob("*.txt")):
    pid = f.stem
    lab = labels.get(pid, {})
    useful = {k for k, v in lab.items() if v == "USEFUL"}
    if useful:
        prompts_with_useful += 1
    s, stopped = parse(f.read_text())
    got = [i for i, (v, _) in s["final"].items() if v == "served"]
    for i in got:
        served[lab.get(i, "UNLABELLED")] += 1
    if got[:1] and got[0] in useful:
        served_useful1 += 1
    if useful & set(got):
        served_useful2 += 1
        continue
    if not useful:
        continue
    if stopped:
        step = "stopped before search"
    else:
        steps = []
        for u in useful:
            if u in s["final"]:
                steps.append((5, "cut by max facts"))
            elif u in s["judge"]:
                steps.append((4, "judge below min"))
            elif s["lexical"].get(u, ("",))[0].startswith("passed, over"):
                steps.append((3, "over the judge's candidate limit"))
            elif u in s["vector"] and s["vector"][u][0] == "candidate":
                steps.append((3, "vector candidate, not judged"))
            elif u in s["lexical"]:
                steps.append((2, "lexical gate: " + s["lexical"][u][0] + ", not in vector top"))
            else:
                steps.append((1, "not found: no FTS hit, not in vector top"))
        step = max(steps)[1]
    miss[step] += 1
    examples.setdefault(step, []).append(pid)

print(f"prompts {len(list(explain_dir.glob('*.txt')))}, with a USEFUL fact in the pool {prompts_with_useful}")
print(f"served a USEFUL fact: top-1 {served_useful1}, any served {served_useful2}")
print("served labels:", dict(served))
print("misses by the furthest step a USEFUL fact reached:")
for k, v in miss.most_common():
    print(f"  {v:3d}  {k}  e.g. {' '.join(examples[k][:6])}")
