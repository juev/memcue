"""Score the variants of a scope replay (#345) against U/R/N labels: on the prompts where some variant serves other
facts than the baseline, count per variant the labels of what it serves, the prompts that get a useful fact, and,
paired by prompt against the baseline, the prompts only one side gives a useful fact, with a two-sided sign test.

  python3 -I scope_eval.py scope.jsonl labels.json [FACTS.db]

With FACTS.db the useful facts a variant drops are broken down by scope family, and the prompts whose only useful
fact in the baseline was a service: fact are counted.
"""
import json
import math
import sqlite3
import sys

rows = [json.loads(line) for line in open(sys.argv[1])]
labels = json.load(open(sys.argv[2]))
scope = {}
if len(sys.argv) > 3:
    db = sqlite3.connect(f"file:{sys.argv[3]}?mode=ro", uri=True)
    scope = dict(db.execute("SELECT id, scope FROM facts f WHERE version = (SELECT max(version) FROM facts WHERE id = f.id)"))

rows = [r for r in rows if any(v != r["picks"]["base"] for v in r["picks"].values())]
variants = sorted(rows[0]["picks"]) if rows else []


def sign_p(a, b):
    n, k = a + b, min(a, b)
    if n == 0:
        return 1.0
    return min(1.0, 2 * sum(math.comb(n, i) for i in range(k + 1)) / 2 ** n)


def useful(r, v):
    got = labels.get(r["id"], {}).get("labels", {})
    return {i for i in r["picks"][v] if got.get(i) == "U"}


print(f"prompts where some variant differs from the baseline: {len(rows)}")
print(f"{'variant':<12} {'U':>4} {'R':>4} {'N':>4} {'none':>4} {'noise':>6} {'w/ U':>5} {'only N':>6} "
      f"{'+U':>4} {'-U':>4} {'p':>6}  needed: {'w/ U':>5} {'N':>4}")
for v in variants:
    c = {"U": 0, "R": 0, "N": 0, "": 0}
    with_u = only_n = gain = loss = need_u = need_n = 0
    for r in rows:
        got = labels.get(r["id"], {})
        ls = [got.get("labels", {}).get(i, "") for i in r["picks"][v]]
        for x in ls:
            c[x] += 1
        u, ub = useful(r, v), useful(r, "base")
        if u:
            with_u += 1
        if ls and all(x == "N" for x in ls):
            only_n += 1
        if u and not ub:
            gain += 1
        if ub and not u:
            loss += 1
        if got.get("needed"):
            need_u += bool(u)
            need_n += ls.count("N")
    labelled = c["U"] + c["R"] + c["N"]
    noise = c["N"] / labelled if labelled else 0
    print(f"{v:<12} {c['U']:>4} {c['R']:>4} {c['N']:>4} {c['']:>4} {noise:>6.0%} {with_u:>5} {only_n:>6} "
          f"{gain:>4} {loss:>4} {sign_p(gain, loss):>6.3f}  {'':>8}{need_u:>5} {need_n:>4}")

if scope:
    print("\nuseful facts of the baseline a variant no longer serves, by scope family")
    for v in variants:
        fam = {}
        for r in rows:
            for i in useful(r, "base") - set(r["picks"][v]):
                sc = (scope.get(i) or "").lower()
                f = sc.split(":", 1)[0] if ":" in sc else (sc or "empty")
                fam[f] = fam.get(f, 0) + 1
        print(f"  {v:<12} {fam}")
    lost_service = {}
    for v in variants:
        n = 0
        for r in rows:
            ub = useful(r, "base")
            if ub and all((scope.get(i) or "").lower().startswith("service:") for i in ub) and not useful(r, v):
                n += 1
        lost_service[v] = n
    print(f"\nprompts that lose their only useful service: facts: {lost_service}")
