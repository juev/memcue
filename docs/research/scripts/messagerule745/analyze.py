#!/usr/bin/env python3
"""Counts per arm of #745 from the run and the labels; no query or fact text in the output.

    analyze.py RUN.json LABELS.json OUT.json
"""
import json
import sys

ARMS = ["current", "one-term", "search", "wide"]
SHORT = {"USEFUL": "U", "RELATED": "R", "NOISE": "N"}


def main():
    run = json.load(open(sys.argv[1]))
    labels = {k: SHORT[v] for k, v in json.load(open(sys.argv[2])).items()}
    out = {"queries": len(run["results"]), "active_facts": run["active_facts"], "arms": {}, "per_query": []}
    lab = {}
    for r in run["results"]:
        lab[r["qid"]] = {arm: [labels[f"{r['qid']}:{f}"] for f in r["arms"][arm]["facts"]] for arm in ARMS}
    for arm in ARMS:
        ls = [lab[q][arm] for q in lab]
        facts = sum(len(x) for x in ls)
        counts = {k: sum(x.count(k) for x in ls) for k in "URN"}
        out["arms"][arm] = {
            "u_at_5": sum("U" in x for x in ls),
            "u_at_1": sum(bool(x) and x[0] == "U" for x in ls),
            "empty": sum(not x for x in ls),
            "widened": sum(r["arms"][arm]["widened"] for r in run["results"]),
            "facts": facts, **counts,
            "n_share": round(counts["N"] / facts, 3) if facts else None,
        }
    out["pool_ceiling"] = sum(any(labels[k] == "U" for k in labels if k.startswith(q + ":")) for q in lab)
    for q, by_arm in lab.items():
        row = {"qid": q, "labels": {arm: "".join(by_arm[arm]) for arm in ARMS}}
        base_u = "U" in by_arm["current"]
        row["versus_current"] = {arm: ("gain" if "U" in by_arm[arm] and not base_u else
                                       "loss" if base_u and "U" not in by_arm[arm] else "")
                                 for arm in ARMS[1:]}
        row["same_as_current"] = {arm: next(r for r in run["results"] if r["qid"] == q)["arms"][arm]["facts"] ==
                                  next(r for r in run["results"] if r["qid"] == q)["arms"]["current"]["facts"]
                                  for arm in ARMS[1:]}
        out["per_query"].append(row)
    for arm in ARMS[1:]:
        out["arms"][arm]["versus_current"] = {
            "gain": sum(r["versus_current"][arm] == "gain" for r in out["per_query"]),
            "loss": sum(r["versus_current"][arm] == "loss" for r in out["per_query"]),
            "same_list": sum(r["same_as_current"][arm] for r in out["per_query"]),
        }
    json.dump(out, open(sys.argv[3], "w"), indent=1)
    print(json.dumps(out["arms"], indent=1), "\npool ceiling", out["pool_ceiling"])
    for r in out["per_query"]:
        print(r["qid"], r["labels"], {k: v for k, v in r["versus_current"].items() if v})


if __name__ == "__main__":
    main()
