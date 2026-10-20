"""Embed every active fact with bge-m3 on RouterAI and compare cosine neighbours with the lexical candidates."""
import json
import sys
import urllib.request
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path

import numpy as np

cands_path, lex10_path, key_path, out_dir = map(Path, sys.argv[1:5])
key = key_path.read_text().strip()
rows = [json.loads(line) for line in cands_path.open()]
facts = [r["fact"] for r in rows]
ids = [f["id"] for f in facts]

emb_file = out_dir / "emb.npy"
if not emb_file.exists():
    def embed(batch):
        body = {"model": "baai/bge-m3", "input": [f["text"] for f in batch]}
        req = urllib.request.Request("https://routerai.ru/api/v1/embeddings", data=json.dumps(body).encode(),
                                     headers={"Authorization": f"Bearer {key}", "Content-Type": "application/json"})
        for _ in range(3):
            try:
                with urllib.request.urlopen(req, timeout=120) as resp:
                    d = json.load(resp)
                return [x["embedding"] for x in sorted(d["data"], key=lambda x: x["index"])], d["usage"]["prompt_tokens"]
            except Exception as e:  # retry transient errors
                err = e
        raise err

    batches = [facts[i:i + 32] for i in range(0, len(facts), 32)]
    with ThreadPoolExecutor(4) as ex:
        out = list(ex.map(embed, batches))
    vecs = np.array([v for b, _ in out for v in b], dtype=np.float32)
    print("embedding tokens", sum(t for _, t in out))
    np.save(emb_file, vecs)
vecs = np.load(emb_file)
vecs /= np.linalg.norm(vecs, axis=1, keepdims=True)
sim = vecs @ vecs.T
np.fill_diagonal(sim, -1)

lex30 = {frozenset((r["fact"]["id"], c["id"])) for r in rows for c in r["candidates"] or []}
lex10 = {frozenset((json.loads(l)["fact"]["id"], c["id"]))
         for l in lex10_path.open() for c in json.loads(l)["candidates"] or []}

top = {}
for i in range(len(ids)):
    for j in np.argsort(-sim[i])[:10]:
        top[frozenset((ids[i], ids[j]))] = float(sim[i, j])
print("facts", len(ids), "top10 pairs", len(top))
for t in (0.70, 0.75, 0.80, 0.85, 0.90):
    sel = {p for p, s in top.items() if s >= t}
    print(f"cos>={t:.2f}: pairs {len(sel):5d}, not in lexical 0.2/10: {len(sel - lex10):5d}, "
          f"not in lexical 0.1/30: {len(sel - lex30):5d}")
json.dump([{"a": sorted(p)[0], "b": sorted(p)[1], "cos": s} for p, s in top.items() if p not in lex30],
          (out_dir / "vec_only.json").open("w"))
