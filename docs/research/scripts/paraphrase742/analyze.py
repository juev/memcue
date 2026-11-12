"""memcue #742: metrics from the saved rankings. Reads nothing but the frozen queries, the run and the encoding
times, so every number in the report can be recomputed from the published files.

Usage: analyze.py QUERIES.json RUN.json.gz ENCODING.json OUT_DIR
Writes OUT_DIR/results.json and OUT_DIR/query-results.csv and prints the report tables.
"""
import csv
import gzip
import json
import math
import sys
from pathlib import Path

queries_path, run_path, enc_path, out_dir = sys.argv[1:5]
out_dir = Path(out_dir)
qs = {q["id"]: q for q in json.load(open(queries_path))["queries"]}
run = json.load(gzip.open(run_path))
enc = json.load(open(enc_path))
arms = run["arms"]
STRATA = ["literal", "low", "zero", "cross"]
COMPARED = [a for a in arms if a != "bm25"]


def rank5(r):
    return r["gold_rank"] if 0 < r["gold_rank"] <= 5 else 0


def wilson(k, n):
    if n == 0:
        return [0.0, 0.0]
    z, p = 1.959964, k / n
    d = 1 + z * z / n
    c = (p + z * z / (2 * n)) / d
    h = z * math.sqrt(p * (1 - p) / n + z * z / (4 * n * n)) / d
    return [round(c - h, 4), round(c + h, 4)]


def sign_p(a, b):
    """Exact two-sided sign test on the discordant pairs."""
    n, k = a + b, min(a, b)
    if n == 0:
        return 1.0
    return min(1.0, 2 * sum(math.comb(n, i) for i in range(k + 1)) / 2 ** n)


def pct(xs, p):
    xs = sorted(xs)
    return xs[max(0, math.ceil(p * len(xs)) - 1)]


def median(xs):
    xs = sorted(xs)
    n = len(xs)
    return xs[n // 2] if n % 2 else (xs[n // 2 - 1] + xs[n // 2]) / 2


rows = run["queries"]
quality, versus = {}, {}
for s in STRATA + ["all-positive"]:
    sel = [r for r in rows if (qs[r["id"]]["stratum"] == s) or (s == "all-positive" and qs[r["id"]]["gold"])]
    n = len(sel)
    quality[s] = {"n": n, "targets": len({qs[r["id"]]["gold"][0] for r in sel})}
    for a in arms:
        h5 = sum(rank5(r["arms"][a]) > 0 for r in sel)
        h1 = sum(r["arms"][a]["gold_rank"] == 1 for r in sel)
        mrr = sum(1 / rank5(r["arms"][a]) for r in sel if rank5(r["arms"][a])) / n if n else 0
        quality[s][a] = {"hit5": h5, "hit5_ci95": wilson(h5, n), "hit1": h1, "mrr5": round(mrr, 4)}
    versus[s] = {}
    for a in COMPARED:
        rescues = [r["id"] for r in sel if rank5(r["arms"][a]) and not rank5(r["arms"]["bm25"])]
        losses = [r["id"] for r in sel if rank5(r["arms"]["bm25"]) and not rank5(r["arms"][a])]
        versus[s][a] = {"rescues": rescues, "losses": losses, "sign_p": round(sign_p(len(rescues), len(losses)), 4)}

# Exploratory splits, added after the first results; not part of the frozen metrics.
exploratory = {"cross_by_overlap": {}, "by_fact_language": {}, "hybrid_vs_vector": {}, "bm25_gold_listed": {}}
for label, test in (("0", lambda o: o == 0), (">=1", lambda o: o >= 1)):
    sel = [r for r in rows if qs[r["id"]]["stratum"] == "cross" and test(qs[r["id"]]["overlap"])]
    exploratory["cross_by_overlap"][label] = {"n": len(sel), **{a: sum(rank5(r["arms"][a]) > 0 for r in sel)
                                                                 for a in arms}}
for s in STRATA:
    for lang in ("en", "ru"):
        sel = [r for r in rows if qs[r["id"]]["stratum"] == s and qs[r["id"]]["fact_lang"] == lang]
        exploratory["by_fact_language"][f"{s}/{lang}"] = {"n": len(sel), **{a: sum(rank5(r["arms"][a]) > 0
                                                                                  for r in sel) for a in arms}}
    sel = [r for r in rows if qs[r["id"]]["stratum"] == s]
    exploratory["bm25_gold_listed"][s] = {"n": len(sel), "listed": sum(r["arms"]["bm25"]["gold_rank"] > 0 for r in sel),
                                          "listed_below_5": sum(r["arms"]["bm25"]["gold_rank"] > 5 for r in sel)}
    exploratory["hybrid_vs_vector"][s] = {}
    for m in sorted({a[len("vector-"):] for a in arms if a.startswith("vector-")}):
        v, h = f"vector-{m}", f"hybrid-{m}"
        exploratory["hybrid_vs_vector"][s][m] = {
            "hybrid_only": [r["id"] for r in sel if rank5(r["arms"][h]) and not rank5(r["arms"][v])],
            "vector_only": [r["id"] for r in sel if rank5(r["arms"][v]) and not rank5(r["arms"][h])]}

neg = [r for r in rows if qs[r["id"]]["stratum"] == "negative"]
negatives = {"n": len(neg)}
for a in arms:
    returned = [len(r["arms"][a]["top"]) for r in neg]
    negatives[a] = {"any": sum(x > 0 for x in returned), "returned": sum(returned), "slots": 5 * len(neg)}
# Does the best cosine tell an answerable query from a negative? Top-1 cosine of each vector arm.
separation = {}
for a in arms:
    if not a.startswith("vector-"):
        continue
    hit_top = [r["arms"][a]["top"][0]["score"] for r in rows if qs[r["id"]]["gold"] and r["arms"][a]["gold_rank"] == 1]
    neg_top = [r["arms"][a]["top"][0]["score"] for r in neg if r["arms"][a]["top"]]
    separation[a] = {"positive_gold_first_median": round(median(hit_top), 4), "negative_top1_median":
                     round(median(neg_top), 4), "negative_top1_max": round(max(neg_top), 4),
                     "positives_gold_first": len(hit_top),
                     "positives_gold_first_at_or_below_negative_max": sum(x <= max(neg_top) for x in hit_top)}

timing = {}
for a in arms:
    first = [r["times_ms"][a][0] for r in rows]
    warm = [t for r in rows for t in r["times_ms"][a][1:]]
    timing[a] = {"first_median": round(median(first), 4), "first_p95": round(pct(first, 0.95), 4),
                 "warm_median": round(median(warm), 4), "warm_p95": round(pct(warm, 0.95), 4),
                 "samples": len(first) + len(warm)}
encoding = {}
for m in ("e5", "bge"):
    t = enc[m]["query_ms"]
    first = [v[0] for v in t.values()]
    warm = [x for v in t.values() for x in v[1:]]
    encoding[m] = {"first_median": round(median(first), 3), "first_p95": round(pct(first, 0.95), 3),
                   "warm_median": round(median(warm), 3), "warm_p95": round(pct(warm, 0.95), 3),
                   "samples": len(first) + len(warm), "meta": enc[m]["meta"]}

results = {"arms": arms, "passes": run["passes"], "eligible": run["eligible"], "quality": quality,
           "versus_bm25": versus, "exploratory": exploratory, "negatives": negatives, "vector_score_separation": separation,
           "search_ms": timing, "query_encoding_ms": encoding}
(out_dir / "results.json").write_text(json.dumps(results, indent=1, ensure_ascii=False) + "\n")

with open(out_dir / "query-results.csv", "w", newline="") as f:
    w = csv.writer(f, lineterminator="\n")
    w.writerow(["query", "stratum", "lang", "fact_lang", "overlap", "gold", "arm", "gold_rank", "hit1", "hit5",
                "returned", "top5", "text"])
    for r in rows:
        q = qs[r["id"]]
        for a in arms:
            x = r["arms"][a]
            w.writerow([q["id"], q["stratum"], q["lang"], q["fact_lang"], q["overlap"], " ".join(q["gold"]), a,
                        x["gold_rank"], int(x["gold_rank"] == 1), int(0 < x["gold_rank"] <= 5), len(x["top"]),
                        " ".join(t["id"] for t in x["top"]), q["text"]])

# Tables for the report.
print("| Stratum | n | " + " | ".join(arms) + " |")
print("|---|---:|" + "---:|" * len(arms))
for s in STRATA + ["all-positive"]:
    qd = quality[s]
    print(f"| {s} | {qd['n']} | " + " | ".join(f"{qd[a]['hit5']}" for a in arms) + " |")
print()
for metric in ("hit1", "mrr5"):
    print(metric)
    for s in STRATA + ["all-positive"]:
        print(s, {a: quality[s][a][metric] for a in arms})
print()
for s in STRATA + ["all-positive"]:
    print(s, {a: (len(v["rescues"]), len(v["losses"]), v["sign_p"]) for a, v in versus[s].items()})
print(json.dumps(exploratory["cross_by_overlap"]))
print(json.dumps(exploratory["bm25_gold_listed"]))
for k, v in exploratory["by_fact_language"].items():
    print(k, v)
for s_, v in exploratory["hybrid_vs_vector"].items():
    print(s_, {m: (len(x["hybrid_only"]), len(x["vector_only"])) for m, x in v.items()})
print(json.dumps(negatives))
print(json.dumps(separation))
print(json.dumps(timing))
print(json.dumps({m: {k: v for k, v in e.items() if k != "meta"} for m, e in encoding.items()}))
