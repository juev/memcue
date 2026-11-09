"""Score the arms of a #682 run against the blind labels: useful prompts at 1 and 5, label mix, wins and losses."""
import json, sys
from pathlib import Path
here = Path(sys.argv[1]); run = json.loads(Path(sys.argv[2]).read_text()); tag = sys.argv[3]
labels = json.loads((here / "labels.json").read_text())
def lab(q, f): return labels.get(q + ":" + f)
arms = {"lexical": lambda r: r["lex5"], f"vector-{tag}": lambda r: r["vec20"][:5], f"hybrid-{tag}": lambda r: r["hyb5"]}
rows = [r for r in run if all(lab(r["qid"], f) for k in ("lex5", "hyb5") for f in r[k]) and all(lab(r["qid"], f) for f in r["vec20"][:5])]
n = len(rows)
print(f"prompts with every candidate labelled: {n} of {len(run)}")
print(f"{'arm':<14}{'U@1':>6}{'U@5':>6}{'U':>6}{'R':>6}{'N':>6}{'noise':>8}{'returned':>10}")
useful = {}
for name, pick in arms.items():
    u1 = sum(1 for r in rows if pick(r)[:1] and lab(r["qid"], pick(r)[0]) == "USEFUL")
    u5set = {r["qid"] for r in rows if any(lab(r["qid"], f) == "USEFUL" for f in pick(r))}
    useful[name] = u5set
    mix = {k: sum(1 for r in rows for f in pick(r) if lab(r["qid"], f) == k) for k in ("USEFUL", "RELATED", "NOISE")}
    tot = sum(mix.values())
    print(f"{name:<14}{u1:>6}{len(u5set):>6}{mix['USEFUL']:>6}{mix['RELATED']:>6}{mix['NOISE']:>6}{mix['NOISE']/max(tot,1):>8.0%}{tot:>10}")
ceiling = sum(1 for r in rows if any(lab(r["qid"], f) == "USEFUL" for f in set(r["lex5"]) | set(r["hyb5"]) | set(r["vec20"][:5])))
lex = useful["lexical"]
for name in list(arms)[1:]:
    print(f"{name}: wins over lexical (U@5 only here) {len(useful[name] - lex)}, losses {len(lex - useful[name])}")
print("pool ceiling U@5:", ceiling, "of", n)
