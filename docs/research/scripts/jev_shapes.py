#!/usr/bin/env python3
"""Compare two question shapes for duplicate judging on the same candidates, against labels someone else gave.

Labels come from a `memcue vet -dry-run -json` run, which is the review model's opinion, not the truth:

    memcue vet -dry-run -json > vet.json
    python3 -I -c 'import json;print(json.dumps([{"id":d["id"],"verdict":d["verdict"]}
        for d in json.load(open("vet.json"))["decisions"]]))' > labels.json

    python3 -I jev_shapes.py --facts FACTS.db --key-file KEY --labels labels.json [--json OUT]

Each candidate is asked about every approved fact twice, as a `noul` ("same claim?") and as a `score` over three
levels; the strongest answer of each shape is kept. Prints AUC and a threshold sweep, so a threshold is picked from
the trade-off rather than from one example.
"""

import argparse
import json
import sqlite3
import sys
import urllib.error
import urllib.request
from concurrent.futures import ThreadPoolExecutor

URL = "https://openrouter.ai/api/alpha/decisions"
MODEL = "typesafe/jev-1.14"
POLICY = ("A knowledge base stores atomic facts for a coding agent. A candidate is a fact just extracted from a "
          "session; the approved facts are what the base already holds. Two facts state the same claim when keeping "
          "both would tell a reader nothing new: the same rule, path, version, command or decision, possibly in other "
          "words, in another language, or with a detail that does not change the claim. They do not state the same "
          "claim when they share a subject but differ in claim, scope, host or time.")
LEVELS = ["Same claim, just reworded: keeping both would tell a reader nothing new.",
          "Same subject, but a different, narrower or stale claim.",
          "Unrelated statements."]


def ask(key, head, questions):
    body = {"model": MODEL, "state": head, "questions": questions}
    req = urllib.request.Request(URL, data=json.dumps(body).encode(),
                                 headers={"Authorization": "Bearer " + key, "Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=300) as resp:
            return json.loads(resp.read())
    except urllib.error.HTTPError as err:
        sys.exit("HTTP %s: %s" % (err.code, err.read()[:400].decode(errors="replace")))


def auc(rows, key, positive="DROP"):
    pos = [r[key] for r in rows if r["verdict"] == positive]
    neg = [r[key] for r in rows if r["verdict"] != positive]
    if not pos or not neg:
        return float("nan")
    wins = sum((p > q) + 0.5 * (p == q) for p in pos for q in neg)
    return wins / (len(pos) * len(neg))


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--facts", required=True)
    ap.add_argument("--key-file", required=True)
    ap.add_argument("--labels", required=True)
    ap.add_argument("--json", metavar="OUT")
    args = ap.parse_args()
    key = open(args.key_file, encoding="utf-8").read().strip()
    labels = json.load(open(args.labels, encoding="utf-8"))
    db = sqlite3.connect("file:%s?mode=ro" % args.facts, uri=True)
    approved = db.execute("""select id, content from facts where status='active' and review=''
        and version=(select max(version) from facts f2 where f2.id=facts.id) order by id""").fetchall()
    texts = dict(db.execute("select id, content from facts").fetchall())
    db.close()
    stored = "\n".join("[%d] %s" % (k, t) for k, (_, t) in enumerate(approved, 1))

    def one(label):
        questions = {}
        for k in range(1, len(approved) + 1):
            questions["n%d" % k] = {"type": "noul",
                                    "instructions": "Does approved fact [%d] state the same claim as the candidate, "
                                                    "so that storing the candidate would add nothing?" % k}
            questions["s%d" % k] = {"type": "score",
                                    "instructions": "How close is approved fact [%d] to the candidate?" % k,
                                    "criteria": LEVELS}
        answer = ask(key, {"policy": POLICY, "candidate": texts[label["id"]], "approved": stored}, questions)
        noul = max((a.get("noul", 0) for k, a in answer["answers"].items() if k.startswith("n")), default=0)
        same = max(((a.get("probabilities") or {}).get("0", 0) for k, a in answer["answers"].items()
                    if k.startswith("s")), default=0)
        return {"id": label["id"], "verdict": label["verdict"], "p_noul": noul, "p_score": same,
                "cost": answer["usage"].get("cost", 0)}

    with ThreadPoolExecutor(max_workers=4) as pool:
        rows = list(pool.map(one, labels))
    for k in ("p_noul", "p_score"):
        print("%-8s AUC %.3f" % (k, auc(rows, k)))
    print("\nthreshold  noul caught/false   score caught/false")
    for t in (0.2, 0.3, 0.35, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9):
        line = "%9.2f" % t
        for k in ("p_noul", "p_score"):
            caught = sum(1 for r in rows if r["verdict"] == "DROP" and r[k] >= t)
            false = sum(1 for r in rows if r["verdict"] != "DROP" and r[k] >= t)
            line += "   %2d/%2d" % (caught, false)
        print(line)
    print("\ncost $%.4f over %d candidates" % (sum(r["cost"] for r in rows), len(rows)))
    if args.json:
        json.dump(rows, open(args.json, "w"), indent=1)


if __name__ == "__main__":
    main()
