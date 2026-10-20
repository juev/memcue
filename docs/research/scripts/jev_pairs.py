#!/usr/bin/env python3
"""Compare Jev's class decision with bge-m3 cosine on the same hand-picked pairs.

The pairs file is a JSON list of {"name", "a", "b", "gold"}, where `gold` is one of `same`,
`related` (same subject, different or stale claim) or `unrelated`. Jev is asked twice per pair,
the documented way: one `score` over those three classes, and one `noul` "same claim". The two
texts are also embedded with bge-m3 and their cosine is printed next to both.

    python3 -I jev_pairs.py --key-file KEY --pairs PAIRS.json [--json OUT]

The pairs file is built from the base and stays out of the repository.
"""

import argparse
import json
import math
import sys
import time
import urllib.error
import urllib.request

DECISIONS = "https://openrouter.ai/api/alpha/decisions"
EMBEDDINGS = "https://openrouter.ai/api/v1/embeddings"
MODEL = "typesafe/jev-1.14"
EMBED_MODEL = "baai/bge-m3"
CLASSES = ["same claim, just reworded", "same subject, different or stale claim", "unrelated statements"]


def post(url, body, key):
    req = urllib.request.Request(url, data=json.dumps(body).encode(),
                                headers={"Authorization": "Bearer " + key,
                                         "Content-Type": "application/json"})
    started = time.perf_counter()
    try:
        with urllib.request.urlopen(req, timeout=300) as resp:
            return json.loads(resp.read()), time.perf_counter() - started
    except urllib.error.HTTPError as err:
        sys.exit("HTTP %s at %s: %s" % (err.code, url, err.read()[:400].decode(errors="replace")))


def cosine(u, v):
    dot = sum(a * b for a, b in zip(u, v))
    return dot / (math.sqrt(sum(a * a for a in u)) * math.sqrt(sum(b * b for b in v)))


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--key-file", required=True)
    ap.add_argument("--pairs", required=True)
    ap.add_argument("--json", metavar="OUT")
    args = ap.parse_args()
    key = open(args.key_file, encoding="utf-8").read().strip()
    pairs = json.load(open(args.pairs, encoding="utf-8"))

    state, questions = [], {}
    for k, pair in enumerate(pairs, 1):
        state += ["Fact %d: %s" % (2 * k - 1, pair["a"]), "Fact %d: %s" % (2 * k, pair["b"])]
        questions[pair["name"]] = {
            "type": "score",
            "instructions": ("Is Fact %d the same claim as Fact %d, a different claim on the same subject, "
                             "or an unrelated statement? Judge meaning only, ignore wording." % (2 * k, 2 * k - 1)),
            "criteria": CLASSES,
        }
        questions[pair["name"] + "_dup"] = {
            "type": "noul",
            "instructions": "Do Fact %d and Fact %d assert the same thing?" % (2 * k - 1, 2 * k),
        }

    answer, seconds = post(DECISIONS, {"model": MODEL, "state": state, "questions": questions}, key)
    usage = answer["usage"]

    emb, emb_seconds = post(EMBEDDINGS, {"model": EMBED_MODEL, "input": state}, key)
    vectors = [d["embedding"] for d in emb["data"]]

    print("%-22s %-9s %6s %6s %6s %6s  %s"
          % ("pair", "gold", "p_same", "p_rel", "p_unrel", "cos", "p_dup"), file=sys.stderr)
    rows = []
    for k, pair in enumerate(pairs, 1):
        probs = answer["answers"][pair["name"]].get("probabilities") or {}
        row = {"name": pair["name"], "gold": pair["gold"],
               "p_same": probs.get("0"), "p_related": probs.get("1"), "p_unrelated": probs.get("2"),
               "p_dup": answer["answers"][pair["name"] + "_dup"]["noul"],
               "cosine": cosine(vectors[2 * k - 2], vectors[2 * k - 1])}
        rows.append(row)
        print("%-22s %-9s %6.2f %6.2f %6.2f %6.3f  %6.2f"
              % (row["name"], row["gold"], row["p_same"] or 0, row["p_related"] or 0,
                 row["p_unrelated"] or 0, row["cosine"], row["p_dup"]), file=sys.stderr)
    print("jev: %.2fs, %d input tokens, $%.6f (%s); embeddings: %.2fs"
          % (seconds, usage["input_tokens"], usage["cost"], answer["model"], emb_seconds), file=sys.stderr)
    if args.json:
        with open(args.json, "w", encoding="utf-8") as f:
            json.dump({"rows": rows, "usage": usage, "seconds": seconds}, f, ensure_ascii=False, indent=1)


if __name__ == "__main__":
    main()
