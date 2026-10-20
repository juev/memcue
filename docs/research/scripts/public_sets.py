"""Check Jev noul (the documented duplicate pattern) and bge-m3 cosine on public pair sets with known answers."""
import json
import random
import sys
import time
import urllib.error
import urllib.request
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path

key = Path(sys.argv[1]).read_text().strip()
out_dir = Path(sys.argv[2])
out_dir.mkdir(exist_ok=True)
N = 200
random.seed(20261020)


def rows(dataset, config, split, total):
    got = []
    for off in range(0, total, 100):
        url = (f"https://datasets-server.huggingface.co/rows?dataset={dataset}&config={config}&split={split}"
               f"&offset={off}&length=100")
        for attempt in range(8):
            try:
                with urllib.request.urlopen(url, timeout=60) as r:
                    got += [x["row"] for x in json.load(r)["rows"]]
                break
            except urllib.error.HTTPError as e:
                if e.code != 429:
                    raise
                time.sleep(5 * (attempt + 1))
        time.sleep(1)
    return got


def balanced(items, key_fn, per_class):
    by = {}
    for it in items:
        by.setdefault(key_fn(it), []).append(it)
    out = []
    for k, n in per_class.items():
        out += random.sample(by[k], n)
    random.shuffle(out)
    return out


sets_file = out_dir / "sets.json"
if sets_file.exists():
    sets = json.loads(sets_file.read_text())
else:
    paws = rows("google-research-datasets/paws-x", "en", "test", 800)
    para = rows("merionum/ru_paraphraser", "default", "test", 1924)
    xnli = rows("facebook/xnli", "ru", "test", 900)
    sets = {
        "paws_en": [{"a": r["sentence1"], "b": r["sentence2"], "y": r["label"] == 1, "cls": str(r["label"])}
                    for r in balanced(paws, lambda r: r["label"], {1: 100, 0: 100})],
        "paraphraser_ru": [{"a": r["text_1"], "b": r["text_2"], "y": r["class"] == "1", "cls": r["class"]}
                           for r in balanced(para, lambda r: r["class"], {"1": 70, "0": 70, "-1": 60})],
        "xnli_ru": [{"a": r["premise"], "b": r["hypothesis"], "y": r["label"] == 2, "cls": str(r["label"])}
                    for r in balanced(xnli, lambda r: r["label"], {2: 100, 0: 50, 1: 50})],
    }
    sets_file.write_text(json.dumps(sets, ensure_ascii=False))

SAME = {
    "question": "Does the potential duplicate state the same fact as the new record?",
    "true": "Both state the same fact, possibly in other words.",
    "false": "They are merely on a similar topic or about the same thing, but state a different fact or detail.",
}
CONTRA = {
    "question": "Does the other record state a state of things that cannot be true together with the new record?",
    "true": "Both cannot be true at once: the other record states a different, incompatible state of the same thing.",
    "false": "Both can be true at once, or the other record is about something else.",
}


def jev(model, pair, q):
    body = {"model": model, "state": {"new_record": pair["a"]},
            "questions": {"q": {"type": "noul", "instructions": {"question": q["question"], "potential_duplicate"
                                if q is SAME else "other_record": pair["b"]},
                                "criteria": {"true": q["true"], "false": q["false"]}}}}
    req = urllib.request.Request("https://routerai.ru/api/v1/decisions", data=json.dumps(body).encode(),
                                 headers={"Authorization": f"Bearer {key}", "Content-Type": "application/json"})
    for _ in range(3):
        try:
            with urllib.request.urlopen(req, timeout=90) as r:
                a = json.load(r)
            return a["answers"]["q"]["noul"], a["usage"]["input_tokens"]
        except Exception:  # retry transient errors
            pass
    return None, 0


def embed(texts):
    body = {"model": "baai/bge-m3", "input": texts}
    req = urllib.request.Request("https://routerai.ru/api/v1/embeddings", data=json.dumps(body).encode(),
                                 headers={"Authorization": f"Bearer {key}", "Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=120) as r:
        return [x["embedding"] for x in sorted(json.load(r)["data"], key=lambda x: x["index"])]


def cosine(u, v):
    dot = sum(x * y for x, y in zip(u, v))
    return dot / (sum(x * x for x in u) ** 0.5 * sum(y * y for y in v) ** 0.5)


def auc(scores, labels):
    pos = [s for s, y in zip(scores, labels) if y]
    neg = [s for s, y in zip(scores, labels) if not y]
    wins = sum((p > n) + 0.5 * (p == n) for p in pos for n in neg)
    return wins / (len(pos) * len(neg))


res_file = out_dir / "results.json"
results = json.loads(res_file.read_text()) if res_file.exists() else {}
for name, pairs in sets.items():
    q = CONTRA if name == "xnli_ru" else SAME
    for model in ("typesafe/jev-1.13", "typesafe/jev-1.14"):
        k = f"{name}|{model}"
        if k not in results:
            with ThreadPoolExecutor(8) as ex:
                got = list(ex.map(lambda p: jev(model, p, q), pairs))
            results[k] = {"scores": [g[0] for g in got], "tokens": sum(g[1] for g in got)}
            res_file.write_text(json.dumps(results))
    k = f"{name}|bge-m3"
    if k not in results:
        va = [e for i in range(0, len(pairs), 50) for e in embed([p["a"] for p in pairs[i:i + 50]])]
        vb = [e for i in range(0, len(pairs), 50) for e in embed([p["b"] for p in pairs[i:i + 50]])]
        results[k] = {"scores": [cosine(a, b) for a, b in zip(va, vb)], "tokens": 0}
        res_file.write_text(json.dumps(results))

print(f"{'set':16} {'method':18} {'AUC':>5} {'acc@0.5':>8} {'errors':>6}  mean score by class")
for name, pairs in sets.items():
    labels = [p["y"] for p in pairs]
    for method in ("typesafe/jev-1.13", "typesafe/jev-1.14", "bge-m3"):
        sc = results[f"{name}|{method}"]["scores"]
        ok = [(s, y, p["cls"]) for s, y, p in zip(sc, labels, pairs) if s is not None]
        a = auc([s for s, _, _ in ok], [y for _, y, _ in ok])
        acc = sum((s >= 0.5) == y for s, y, _ in ok) / len(ok) if "jev" in method else float("nan")
        by = {}
        for s, _, c in ok:
            by.setdefault(c, []).append(s)
        means = " ".join(f"{c}:{sum(v) / len(v):.2f}" for c, v in sorted(by.items()))
        print(f"{name:16} {method:18} {a:5.3f} {acc:8.3f} {len(sc) - len(ok):6}  {means}")
print("jev tokens", sum(v["tokens"] for v in results.values()))
