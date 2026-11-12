"""memcue #742: turn the authored corpus into the fixture, with fact IDs in a seeded random order.

The tie order of every arm is ID ascending, so the IDs must not follow the authoring order, where the target is the
first fact of its group. Usage: build_fixture.py corpus.json fixture.json
"""
import json
import random
import sys

corpus_path, out_path = sys.argv[1:3]
corpus = json.load(open(corpus_path))
facts = []
for g in corpus["groups"]:
    for i, content in enumerate(g["facts"]):
        facts.append({"group": g["group"], "lang": g["lang"], "role": "target" if i == 0 else "neighbour",
                      "content": content})
order = list(range(len(facts)))
random.Random(742).shuffle(order)
for n, i in enumerate(order, 1):
    facts[i]["id"] = f"F{n:03d}"
facts.sort(key=lambda f: f["id"])
out = {"purpose": "memcue-742-paraphrase-fixture-v1", "corpus": corpus["purpose"],
       "facts": [{"id": f["id"], "group": f["group"], "lang": f["lang"], "role": f["role"], "content": f["content"]}
                 for f in facts]}
with open(out_path, "w") as w:
    json.dump(out, w, ensure_ascii=False, indent=1)
    w.write("\n")
print(len(facts), "facts,", sum(f["role"] == "target" for f in facts), "targets")
