#!/usr/bin/env python3
"""The keep list of the #623 select arm: the card of main with its global part limited to behaviour facts.

    keep623.py --data DATA

DATA holds labels.json (the #606 labels), snap/ (run606.py snap) and cards.json of the current arm
(E2E_ARMS=current run606.py cards). Global facts on the current cards that #606 did not label get a label from
label.py's Opus call. keep.txt is every fact id of the snapshots except the global facts not labelled behaviour:
the repository part of the card stays whole.
"""
import argparse, glob, json, os

import label


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--data", required=True)
    data = ap.parse_args().data
    labels = json.load(open(os.path.join(data, "labels.json")))
    cards = json.load(open(os.path.join(data, "cards.json")))
    # A fact relabelled between two prompts is global in some snapshots and not in others; one keep list serves every
    # run, so a fact global in any snapshot counts as global. None of them sits on a card with its other scope.
    ids, glob_ids = set(), set()
    for path in glob.glob(os.path.join(data, "snap", "*", "facts", "*", "*.jsonl")):
        vs = [json.loads(l) for l in open(path) if l.strip()]
        ids.add(vs[0]["id"])
        if vs[-1].get("scope") == "global":
            glob_ids.add(vs[0]["id"])
    on_cards = {i for c in cards.values() for i in c["current"]["ids"]}
    todo = sorted(i for i in on_cards & glob_ids if i not in labels)
    cost = 0
    if todo:
        got, cost = label.ask([label.latest(i) for i in todo])
        labels.update({l["id"]: l["class"] for l in got if l["id"] in todo})
        json.dump(labels, open(os.path.join(data, "labels.json"), "w"), indent=1)
    drop = {i for i in glob_ids if labels.get(i) != "behaviour"}
    keep = sorted(ids - drop)
    open(os.path.join(data, "keep.txt"), "w").write("\n".join(keep) + "\n")
    print(f"labelled {len(todo)} new global facts (${cost:.2f}); facts {len(ids)}, dropped {len(drop)}, "
          f"keep {len(keep)}")


if __name__ == "__main__":
    main()
