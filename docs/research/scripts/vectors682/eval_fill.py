import json, sys
from pathlib import Path
here = Path(sys.argv[1]); run = json.loads(Path(sys.argv[2]).read_text())
labels = json.loads((here / "labels.json").read_text())
lab = lambda q, f: labels.get(q + ":" + f)
def fill(lex, vec, keep, size=5):
    out = lex[:keep]
    for f in vec + lex[keep:]:
        if len(out) >= size: break
        if f not in out: out.append(f)
    return out
arms = {
    "lexical": lambda r: r["lex5"],
    "lex+vec into empty slots": lambda r: fill(r["lex5"], r["vec20"][:5], 5),
    "lex top-4 + 1 vector": lambda r: fill(r["lex5"], r["vec20"][:5], 4),
    "lex top-3 + 2 vectors": lambda r: fill(r["lex5"], r["vec20"][:5], 3),
    "hybrid RRF": lambda r: r["hyb5"],
}
base = None
print(f"{'arm':<26}{'U@1':>5}{'U@5':>5}{'U':>5}{'N':>5}{'noise':>7}{'slots':>7}{'wins':>6}{'losses':>7}")
for name, pick in arms.items():
    u5 = {r["qid"] for r in run if any(lab(r["qid"], f) == "USEFUL" for f in pick(r))}
    u1 = sum(1 for r in run if pick(r) and lab(r["qid"], pick(r)[0]) == "USEFUL")
    picks = [(r["qid"], f) for r in run for f in pick(r)]
    assert all(lab(q, f) for q, f in picks), name
    U = sum(1 for q, f in picks if lab(q, f) == "USEFUL"); N = sum(1 for q, f in picks if lab(q, f) == "NOISE")
    base = base or u5
    print(f"{name:<26}{u1:>5}{len(u5):>5}{U:>5}{N:>5}{N/len(picks):>7.0%}{len(picks):>7}{len(u5-base):>6}{len(base-u5):>7}")
empty = [r for r in run if len(r["lex5"]) < 5]
print("prompts where lexical returns fewer than 5:", len(empty), "none at all:", sum(1 for r in run if not r["lex5"]))
