"""Train the tree model memcue ships as its local reader (#321) and write the test fixture.

  python3 -I local_train.py POOL_CAND POOL_FEAT POOL_JEV GOLD_FEAT OUTDIR

The model learns Jev's probability from the prompt pool of #320 alone (features from rankfeat, one Jev run), so the
gold set of local_ranker.py stays unseen and its report on it is not in-sample. memcue reads the model at Jev's own
threshold, rerank.DefaultMin: on the gold set 0.5 served as many useful prompts as the words with half their noise,
and 0.34, the threshold that agreed best with Jev on out-of-fold pool predictions, served more useful prompts with more
noise than the words (docs/research/local-readers-2026-10.md).

OUTDIR gets:
  model.txt           the LightGBM text model, header rewritten to version=v3 for github.com/dmitryikh/leaves
  gold.jsonl.gz       every gold prompt with two candidates or more: its rankfeat row and the model's raw scores,
                      which internal/localrank's test reproduces in Go
  local.jsonl         the reader on the gold set in the judge_ab.py result format, for judge_ab_report.py

The vector is the one of local_ranker.py without EXTRA; internal/localrank builds the same one in Go.
"""
import gzip
import json
import sys
from pathlib import Path

import lightgbm as lgb
import numpy as np

SEED = 321
KINDS = ("rule", "decision", "pitfall", "environment")
SCOPES = ("global", "host", "repo")
SOURCES = ("user", "user_confirmed", "tool", "agent", "note", "memory")
PARAMS = dict(n_estimators=300, learning_rate=0.03, num_leaves=7, min_child_samples=20, subsample=0.8, subsample_freq=1,
              colsample_bytree=0.8, reg_lambda=1.0, random_state=SEED, deterministic=True, verbose=-1, n_jobs=1)


def vectors(row):
    """The feature vectors of one rankfeat row, in candidate order: row_features of local_ranker.py."""
    fs = row["features"]
    n = len(fs)
    covs = sorted((f["coverage"] for f in fs), reverse=True)
    best, second = covs[0], covs[1] if n > 1 else 0.0
    cov_rank = {f["id"]: i for i, f in enumerate(sorted(fs, key=lambda f: -f["coverage"]))}
    passed = sum(f["passed"] for f in fs)
    out = []
    for f in fs:
        out.append([
            f["rank"], f["rank"] / max(1, n),
            float(f["passed"]), f["coverage"], f["matched"], f["matched_max"],
            f["content_cov"], f["keyword_cov"], f["query_cov"], float(f["ticket"]),
            f["runes"], f["keywords"], f["queries"], float(f["provisional"]), f["age_days"], float(f["expires"]),
            f["versions"],
            *[float(f["kind"] == k) for k in KINDS], *[float(f["scope"] == s) for s in SCOPES],
            *[float(f["source"] == s) for s in SOURCES],
            f["coverage"] - best, f["coverage"] / best if best else 0.0, cov_rank[f["id"]],
            best, best - second, passed, n,
            f["matched"] / max(1, row["known"]),
            row["terms"], row["known"], row["runes"], row["tickets"], row["weights"],
        ])
    return out


def main():
    pool_cand, pool_feat, pool_jev, gold_feat, outdir = sys.argv[1:6]
    out = Path(outdir)
    out.mkdir(parents=True, exist_ok=True)

    jev = {}
    for r in map(json.loads, open(pool_jev)):
        if "error" not in r:
            for fid, s in zip(r["facts"], r["scores"]):
                jev[(r["id"], fid)] = s
    keep = {r["id"] for r in map(json.loads, open(pool_cand))
            if len(r["candidates"] or []) >= 2 and all((r["id"], c["id"]) in jev for c in r["candidates"])}
    X, y, groups = [], [], []
    for row in map(json.loads, open(pool_feat)):
        if row["id"] not in keep or len(row["features"] or []) < 2:
            continue
        for f, v in zip(row["features"], vectors(row)):
            X.append(v)
            y.append(jev[(row["id"], f["id"])])
            groups.append(row["id"])
    X, y, groups = np.array(X, dtype=float), np.array(y), np.array(groups)
    print(f"pool: {len(set(groups))} prompts, {len(X)} pairs, {X.shape[1]} features")

    booster = lgb.LGBMRegressor(**PARAMS).fit(X, y).booster_
    text = booster.model_to_string()
    # leaves reads version=v3 only; a v4 file of plain (not linear) trees has the same layout.
    if "\nis_linear=1" in text:
        sys.exit("linear trees are not supported by leaves")
    text = text.replace("version=v4\n", "version=v3\n", 1)
    (out / "model.txt").write_text(text)

    with gzip.open(out / "gold.jsonl.gz", "wt") as fixture, open(out / "local.jsonl", "w") as reader:
        n = 0
        for row in map(json.loads, open(gold_feat)):
            fs = row["features"] or []
            if len(fs) < 2:
                continue
            scores = booster.predict(np.array(vectors(row), dtype=float)).tolist()
            fixture.write(json.dumps({**row, "scores": scores}, separators=(",", ":")) + "\n")
            reader.write(json.dumps({"id": row["id"], "facts": [f["id"] for f in fs],
                                     "provisional": [f["provisional"] for f in fs],
                                     "scores": [min(1.0, max(0.0, s)) for s in scores]}) + "\n")
            n += 1
    print(f"gold: {n} prompts written")


if __name__ == "__main__":
    main()
