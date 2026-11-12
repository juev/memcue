"""memcue #742: put every checked candidate in its measured stratum and write the query set to freeze, plus the
sample for the hand read.

Strata, by the measured overlap with the gold fact (measure_overlap, memcue's own tokenization), never by the
writer's intent:
  literal  same language, 2 or more shared content terms
  low      same language, exactly 1 shared content term
  zero     same language, no shared content term
  cross    the other language than the gold fact (overlap kept and reported)
  negative no gold fact, and the checker found no fact that answers it
A candidate the unique-answer check rejected is dropped. At most two queries per target and stratum stay, the first
by candidate ID, so no target dominates a stratum.

Usage: select_queries.py CHECKED.jsonl QUERIES.json SAMPLE.jsonl
"""
import json
import random
import re
import sys

in_path, out_path, sample_path = sys.argv[1:4]
rows = [json.loads(line) for line in open(in_path)]
CYR = re.compile(r"[а-яё]", re.I)
LAT = re.compile(r"[a-z]", re.I)


def language(t):
    # The script most letters are in: an English query may carry a Russian name, a Russian one a Latin identifier.
    return "ru" if len(CYR.findall(t)) > len(LAT.findall(t)) else "en"

PER_TARGET = 2

kept, dropped, per = [], {}, {}
for r in sorted(rows, key=lambda r: r["cid"]):
    lang = language(r["text"])
    if not r["unique_ok"]:
        dropped["unique"] = dropped.get("unique", 0) + 1
        continue
    if r["intent"] == "negative":
        stratum = "negative"
    elif lang != r["fact_lang"]:
        stratum = "cross"
    elif r["overlap"] >= 2:
        stratum = "literal"
    elif r["overlap"] == 1:
        stratum = "low"
    else:
        stratum = "zero"
    if stratum != "negative" and r["intent"] != "cross" and lang != r["fact_lang"]:
        dropped["language"] = dropped.get("language", 0) + 1
        continue
    if r["intent"] == "cross" and stratum != "cross":
        dropped["language"] = dropped.get("language", 0) + 1
        continue
    key = (r["gold"][0] if r["gold"] else r["group"], stratum)
    if per.get(key, 0) >= PER_TARGET:
        dropped["cap"] = dropped.get("cap", 0) + 1
        continue
    per[key] = per.get(key, 0) + 1
    kept.append({"cid": r["cid"], "group": r["group"], "stratum": stratum, "intent": r["intent"], "lang": lang,
                 "fact_lang": r["fact_lang"], "text": r["text"], "gold": r["gold"], "query_terms": r["query_terms"],
                 "shared_terms": r["shared_terms"], "overlap": r["overlap"],
                 "checker_answers": r["checker_answers"]})

order = ["literal", "low", "zero", "cross", "negative"]
kept.sort(key=lambda q: (order.index(q["stratum"]), q["cid"]))
for i, q in enumerate(kept, 1):
    q["id"] = f"Q{i:03d}"
counts = {s: sum(q["stratum"] == s for q in kept) for s in order}
targets = {s: len({q["gold"][0] for q in kept if q["stratum"] == s and q["gold"]}) for s in order}
with open(out_path, "w") as w:
    json.dump({"purpose": "memcue-742-paraphrase-queries-v1", "counts": counts, "targets": targets,
               "dropped": dropped, "candidates": len(rows), "queries": kept}, w, ensure_ascii=False, indent=1)
    w.write("\n")
sample = random.Random(742).sample(kept, round(len(kept) / 10))
with open(sample_path, "w") as w:
    for q in sorted(sample, key=lambda q: q["id"]):
        w.write(json.dumps(q, ensure_ascii=False) + "\n")
print("kept", len(kept), counts, "targets", targets, "dropped", dropped, "sample", len(sample))
