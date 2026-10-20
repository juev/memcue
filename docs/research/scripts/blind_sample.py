"""Draw 300 fact pairs from three sources alike (lexical, vector, random within one repository) for blind labelling."""
import json
import random
import sys
from pathlib import Path

import numpy as np

cands10, emb_path, out = Path(sys.argv[1]), Path(sys.argv[2]), Path(sys.argv[3])
random.seed(20261020)
rows = [json.loads(line) for line in cands10.open()]
facts = {r["fact"]["id"]: r["fact"] for r in rows}
ids = [r["fact"]["id"] for r in rows]

# lexical: neighbours by memcue recall (coverage >= 0.2, up to 10); the order of candidates is by recall rank
lex = {}
for r in rows:
    for rank, c in enumerate(r["candidates"] or []):
        lex.setdefault(frozenset((r["fact"]["id"], c["id"])), rank)

v = np.load(emb_path)
v /= np.linalg.norm(v, axis=1, keepdims=True)
sim = v @ v.T
np.fill_diagonal(sim, -1)
vec, vec_first = {}, set()
for i in range(len(ids)):
    for rank, j in enumerate(np.argsort(-sim[i])[:10]):
        vec[frozenset((ids[i], ids[int(j)]))] = float(sim[i, j])
        if rank == 0:
            vec_first.add(frozenset((ids[i], ids[int(j)])))

repo = lambda f: f["origin"].split(" ")[0]
by_repo = {}
for f in facts.values():
    by_repo.setdefault(repo(f), []).append(f["id"])

chosen = {}


def take(pairs, n, source):
    got = 0
    for p in pairs:
        if got == n:
            break
        if p not in chosen:
            chosen[p] = source
            got += 1


lex_pairs = sorted(lex, key=sorted)
take(random.sample([p for p in lex_pairs if lex[p] == 0], 400), 50, "lexical-top")  # a fact's first neighbour
take(random.sample(lex_pairs, 400), 50, "lexical-random")
vec_pairs = sorted(vec, key=sorted)
take(random.sample(sorted(vec_first, key=sorted), 400), 50, "vector-top")  # a fact's first neighbour
take(random.sample(vec_pairs, 400), 50, "vector-random")
big = [r for r, xs in by_repo.items() if len(xs) >= 2]
rand = []
while len(rand) < 400:
    r = random.choice(big)
    a, b = random.sample(by_repo[r], 2)
    rand.append(frozenset((a, b)))
take(rand, 100, "random-same-repo")

pairs = []
for p, src in chosen.items():
    a, b = random.sample(sorted(p), 2)  # random order: the labeller cannot tell which side was the query
    pairs.append({"a": facts[a], "b": facts[b], "source": src, "lex": p in lex, "vec": vec.get(p)})
random.shuffle(pairs)
for i, p in enumerate(pairs):
    p["n"] = i
out.write_text(json.dumps(pairs, ensure_ascii=False))
print(len(pairs), {s: sum(1 for p in pairs if p["source"] == s) for s in set(chosen.values())})
