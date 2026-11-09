import json, statistics as s, sys
r = json.load(open(sys.argv[1]))
def q(a, p): return sorted(a)[max(0, int(len(a) * p) - 1)]
for k in ("embed_ms", "vsearch_ms", "lex_ms"):
    a = [x[k] for x in r if x[k] is not None]
    print(f"{k}: n {len(a)} median {s.median(a):.1f} p95 {q(a, .95):.1f}")
ov = [len(set(x["lex5"]) & set(x["vec20"][:5])) for x in r]
print("lex5 & vec5 overlap mean", round(s.mean(ov), 2), "prompts with no overlap", sum(1 for o in ov if o == 0), "of", len(r))
c1 = [x["cos20"][0] for x in r if x["cos20"]]
print("top-1 cosine median", round(s.median(c1), 3), "p10", round(q(c1, .10), 3), "p90", round(q(c1, .90), 3))
