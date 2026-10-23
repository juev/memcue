"""Train a gradient-boosted tree ranker over the words' own signals and write its scores as judge_ab.py readers (#316).

  python3 -I local_ranker.py candidates.jsonl features.jsonl labels.json OUTDIR jev-1.jsonl [jev-2.jsonl ...]

features.jsonl comes from rankfeat; the jev files are judge_ab.py runs over the same candidates. Every prompt is scored by
a model that never saw it: grouped k-fold by prompt (FOLDS, default 5). Readers written to OUTDIR:

  tree-jev    regression on the mean Jev probability of the pair: the judge distilled into trees
  tree-opus   binary classifier, USEFUL against the rest, on the pairs Opus labelled; the other pairs are not used
  tree-mix    regression on the Opus label where there is one (USEFUL 1, RELATED 0.25, NOISE 0), the Jev mean elsewhere
  words       what recall serves with no reader: the lexical answer rankfeat recorded, scored 1
  bm25        the first two candidates in BM25 order, no gate: scores 1 and 0.99

Scores of every tree reader are Platt-calibrated inside the training folds against the Opus label USEFUL, so one
threshold means the same for all of them. Needs lightgbm, scikit-learn and numpy.
"""
import json
import os
import sys
from pathlib import Path

import lightgbm as lgb
import numpy as np
from sklearn.linear_model import LogisticRegression
from sklearn.model_selection import GroupKFold

FOLDS = int(os.environ.get("FOLDS", "5"))
SEED = 316
KINDS = ("rule", "decision", "pitfall", "environment")
SCOPES = ("global", "host", "repo")
SOURCES = ("user", "user_confirmed", "tool", "agent", "note", "memory")
LABEL_VALUE = {"USEFUL": 1.0, "RELATED": 0.25, "NOISE": 0.0}

cand_path, feat_path, labels_path, outdir = sys.argv[1:5]
jev_paths = sys.argv[5:]
# A prompt with fewer than two candidates never reaches a reader, and judge_ab.py skips it the same way.
prompts = [r for r in map(json.loads, open(cand_path)) if len(r["candidates"] or []) >= 2]
feats = {r["id"]: r for r in map(json.loads, open(feat_path))}
labels = json.loads(Path(labels_path).read_text())
# EXTRA names a reader JSONL (a cross-encoder's, #317) whose score becomes one more feature of every pair.
extra = {}
if os.environ.get("EXTRA"):
    for r in map(json.loads, open(os.environ["EXTRA"])):
        for fid, s in zip(r["facts"], r["scores"]):
            extra[(r["id"], fid)] = s
jev = {}
for path in jev_paths:
    for r in map(json.loads, open(path)):
        if "error" in r:
            continue
        for fid, s in zip(r["facts"], r["scores"]):
            jev.setdefault((r["id"], fid), []).append(s)


def row_features(f, p, ctx):
    covs = ctx["covs"]
    best, second = covs[0], covs[1] if len(covs) > 1 else 0.0
    return [
        f["rank"], f["rank"] / max(1, p["n"]),
        float(f["passed"]), f["coverage"], f["matched"], f["matched_max"],
        f["content_cov"], f["keyword_cov"], f["query_cov"], float(f["ticket"]),
        f["runes"], f["keywords"], f["queries"], float(f["provisional"]), f["age_days"], float(f["expires"]),
        f["versions"],
        *[float(f["kind"] == k) for k in KINDS], *[float(f["scope"] == s) for s in SCOPES],
        *[float(f["source"] == s) for s in SOURCES],
        # The list around the fact: what abstention needs.
        f["coverage"] - best, f["coverage"] / best if best else 0.0, ctx["cov_rank"][f["id"]],
        best, best - second, ctx["passed"], p["n"],
        f["matched"] / max(1, p["known"]),
        p["terms"], p["known"], p["runes"], p["tickets"], p["weights"],
    ]


NAMES = ["rank", "rank_rel", "passed", "coverage", "matched", "matched_max", "content_cov", "keyword_cov", "query_cov",
         "ticket", "runes", "keywords", "queries", "provisional", "age_days", "expires", "versions",
         *[f"kind_{k}" for k in KINDS], *[f"scope_{s}" for s in SCOPES], *[f"source_{s}" for s in SOURCES],
         "cov_minus_best", "cov_over_best", "cov_rank", "best_cov", "top_gap", "list_passed", "list_n",
         "matched_share", "p_terms", "p_known", "p_runes", "p_tickets", "p_weights",
         *(["extra", "extra_minus_best"] if extra else [])]

def build(prompts, feats, jev, labels, extra):
    X, groups, pairs, y_jev, y_opus, y_mix = [], [], [], [], [], []
    for pr in prompts:
        fr = feats[pr["id"]]
        fs = fr["features"]
        if not fs:
            continue
        p = {**fr, "n": len(fs)}
        covs = sorted((f["coverage"] for f in fs), reverse=True)
        order = sorted(fs, key=lambda f: -f["coverage"])
        ctx = {"covs": covs, "cov_rank": {f["id"]: i for i, f in enumerate(order)},
               "passed": sum(f["passed"] for f in fs),
               "extra_best": max((extra[(pr["id"], f["id"])] for f in fs), default=0.0) if extra else 0.0}
        for f in fs:
            key = (pr["id"], f["id"])
            X.append(row_features(f, p, ctx) + ([extra[key], extra[key] - ctx["extra_best"]] if extra else []))
            groups.append(pr["id"])
            pairs.append(key)
            scores = jev.get(key)
            y_jev.append(float(np.mean(scores)) if scores else np.nan)
            lab = labels.get(f"{pr['id']}|{f['id']}")
            y_opus.append(np.nan if lab is None else float(lab == "USEFUL"))
            y_mix.append(LABEL_VALUE[lab] if lab else y_jev[-1])
    return (np.array(X, dtype=float).reshape(-1, len(NAMES)), np.array(groups), pairs, np.array(y_jev),
            np.array(y_opus), np.array(y_mix))


X, groups, pairs, y_jev, y_opus, y_mix = build(prompts, feats, jev, labels, extra)
print(f"pairs {len(X)}, prompts {len(set(groups))}, with Jev {np.isfinite(y_jev).sum()}, "
      f"with an Opus label {np.isfinite(y_opus).sum()}")

# POOL=cand.jsonl:features.jsonl:jev.jsonl adds prompts outside the gold set to every training fold, with the Jev
# probability as their only label (#320); POOL_N takes that many of them, chosen by a fixed shuffle. The gold set alone
# is still what every reader is scored on.
pool = np.empty((0, len(NAMES)))
pool_y = np.empty(0)
if os.environ.get("POOL"):
    if extra:
        sys.exit("POOL and EXTRA together are not supported")
    pc, pf, pj = os.environ["POOL"].split(":")
    pprompts = [r for r in map(json.loads, open(pc)) if len(r["candidates"] or []) >= 2]
    pjev = {}
    for r in map(json.loads, open(pj)):
        if "error" not in r:
            for fid, s in zip(r["facts"], r["scores"]):
                pjev[(r["id"], fid)] = [s]
    pprompts = [r for r in pprompts if all((r["id"], c["id"]) in pjev for c in r["candidates"])]
    np.random.default_rng(SEED).shuffle(pprompts)
    pprompts = pprompts[:int(os.environ.get("POOL_N", len(pprompts)))]
    pfeats = {r["id"]: r for r in map(json.loads, open(pf))}
    pool, _, _, pool_y, _, _ = build(pprompts, pfeats, pjev, {}, {})
    print(f"pool prompts {len(pprompts)}, pairs {len(pool)}")

PARAMS = dict(n_estimators=300, learning_rate=0.03, num_leaves=7, min_child_samples=20, subsample=0.8, subsample_freq=1,
              colsample_bytree=0.8, reg_lambda=1.0, random_state=SEED, deterministic=True, verbose=-1, n_jobs=1)


def model(kind):
    if kind == "tree-opus":
        return lgb.LGBMClassifier(**PARAMS)
    return lgb.LGBMRegressor(**PARAMS)


def raw(m, Xs):
    return m.predict_proba(Xs)[:, 1] if isinstance(m, lgb.LGBMClassifier) else m.predict(Xs)


def fit(kind, idx):
    y = {"tree-jev": y_jev, "tree-opus": y_opus, "tree-mix": y_mix}[kind]
    keep = idx[np.isfinite(y[idx])]
    m = model(kind)
    if kind == "tree-opus" or not len(pool):
        m.fit(X[keep], y[keep])
    else:
        m.fit(np.vstack([X[keep], pool]), np.concatenate([y[keep], pool_y]))
    return m


out_scores = {k: np.full(len(X), np.nan) for k in ("tree-jev", "tree-opus", "tree-mix")}
importance = {k: np.zeros(len(NAMES)) for k in out_scores}
for train, test in GroupKFold(n_splits=FOLDS).split(X, groups=groups):
    for kind in out_scores:
        # Platt scaling on the training folds' own out-of-fold scores, against the Opus label.
        inner = np.full(len(train), np.nan)
        for itr, ite in GroupKFold(n_splits=FOLDS - 1).split(X[train], groups=groups[train]):
            inner[ite] = raw(fit(kind, train[itr]), X[train[ite]])
        lab = np.isfinite(y_opus[train])
        platt = LogisticRegression().fit(inner[lab].reshape(-1, 1), y_opus[train][lab])
        m = fit(kind, train)
        out_scores[kind][test] = platt.predict_proba(raw(m, X[test]).reshape(-1, 1))[:, 1]
        importance[kind] += m.booster_.feature_importance("gain")

Path(outdir).mkdir(parents=True, exist_ok=True)
by_prompt = {}
for i, (pid, fid) in enumerate(pairs):
    by_prompt.setdefault(pid, []).append(i)
for kind, sc in out_scores.items():
    with open(Path(outdir) / f"{kind}.jsonl", "w") as w:
        for pr in prompts:
            idx = by_prompt.get(pr["id"], [])
            w.write(json.dumps({"id": pr["id"], "facts": [pairs[i][1] for i in idx],
                                "provisional": [bool(X[i][NAMES.index("provisional")]) for i in idx],
                                "scores": [round(float(sc[i]), 4) for i in idx]}) + "\n")
    top = np.argsort(-importance[kind])[:12]
    print(f"{kind}: gain " + ", ".join(f"{NAMES[j]} {importance[kind][j] / importance[kind].sum():.2f}" for j in top))
with open(Path(outdir) / "words.jsonl", "w") as w:
    for pr in prompts:
        served = feats[pr["id"]].get("words") or []
        w.write(json.dumps({"id": pr["id"], "facts": served, "provisional": [False] * len(served),
                            "scores": [1.0] * len(served)}) + "\n")
with open(Path(outdir) / "bm25.jsonl", "w") as w:
    for pr in prompts:
        top2 = [c["id"] for c in (pr["candidates"] or [])[:2]]
        w.write(json.dumps({"id": pr["id"], "facts": top2, "provisional": [False] * len(top2),
                            "scores": [1.0, 0.99][:len(top2)]}) + "\n")
