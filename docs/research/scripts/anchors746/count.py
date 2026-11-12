#!/usr/bin/env python3
"""Stage 1 counts of #746: anchors in agent search queries, from the calls, the hand read and #745's labels.

    count.py CALLS.jsonl SAMPLE.json HAND_READ.txt RESULTS_745.json QUERIES_745.jsonl OUT.json

HAND_READ.txt has one line per sampled call: `CID S I G`, S and I Y/N, G Y/N or - (no task to judge). The sample is
the 120 calls drawn by random.Random(746).sample over the distinct calls in file order (SAMPLE.json lists their IDs).

`identifier` is a deterministic rule for I over every call, checked against the hand read: a token that is a path
(`/`, `~`), a flag (`-x`), an environment variable (FOO_BAR), camelCase, snake_case, a file name with an extension,
a ticket (ABC-123), an issue (#123) or a number of three or more digits, or a dotted version.
The output holds counts and call IDs only.
"""
import json
import math
import re
import sys

TOKEN_RULES = [
    re.compile(r"[/~]"),
    re.compile(r"^-{1,2}[a-z]"),
    re.compile(r"^[A-Z][A-Z0-9]*_[A-Z0-9_]+(=.*)?$"),
    re.compile(r"[a-z][A-Z]"),
    re.compile(r"^[a-z0-9]+_[a-z0-9_]+$"),
    re.compile(r"\.(go|py|sh|ts|js|mjs|json|toml|md|ya?ml|jks|db|kts|txt|log)$"),
    re.compile(r"^[A-Z]{2,}-\d+"),
    re.compile(r"^#\d+"),
    re.compile(r"^\d{3,}$"),
    re.compile(r"\d+\.\d+"),
]


def identifier(text):
    return any(rule.search(tok) for tok in text.split() for rule in TOKEN_RULES)


def wilson(k, n, z=1.96):
    if n == 0:
        return None
    p = k / n
    d = 1 + z * z / n
    c = p + z * z / (2 * n)
    h = z * math.sqrt(p * (1 - p) / n + z * z / (4 * n * n))
    return [round((c - h) / d, 3), round((c + h) / d, 3)]


def share(k, n):
    return {"count": k, "of": n, "wilson95": wilson(k, n)}


def main():
    calls_path, sample_path, hand_path, res745_path, q745_path, out_path = sys.argv[1:7]
    calls = [json.loads(line) for line in open(calls_path)]
    by_id = {c["cid"]: c for c in calls}
    sample = json.load(open(sample_path))
    hand = {}
    for line in open(hand_path):
        cid, s, i, g = line.split()
        hand[cid] = {"S": s == "Y", "I": i == "Y", "G": None if g == "-" else g == "Y"}
    if list(hand) != sample:
        raise SystemExit("the hand read does not follow the sample")
    out = {"calls": len(calls)}

    h = list(hand.values())
    judged = [x for x in h if x["G"] is not None]
    out["hand_read"] = {
        "sample": len(h),
        "system": share(sum(x["S"] for x in h), len(h)),
        "identifier": share(sum(x["I"] for x in h), len(h)),
        "system_and_identifier": share(sum(x["S"] and x["I"] for x in h), len(h)),
        "system_or_identifier": share(sum(x["S"] or x["I"] for x in h), len(h)),
        "neither": share(sum(not x["S"] and not x["I"] for x in h), len(h)),
        "with_task": len(judged),
        "with_task_no_identifier": sum(not x["I"] for x in judged),
        "task_had_identifier_query_lacked": share(sum(bool(x["G"]) for x in judged), len(judged)),
        "gap_calls": [cid for cid, x in hand.items() if x["G"]],
    }

    rule = {c["cid"]: identifier(c["text"]) for c in calls}
    tp = sum(rule[c] and hand[c]["I"] for c in hand)
    fp = sum(rule[c] and not hand[c]["I"] for c in hand)
    fn = sum(not rule[c] and hand[c]["I"] for c in hand)
    out["identifier_rule"] = {
        "all_calls": share(sum(rule.values()), len(calls)),
        "against_hand_read": {"agree": sum(rule[c] == hand[c]["I"] for c in hand), "of": len(hand),
                              "true_pos": tp, "false_pos": fp, "false_neg": fn},
    }

    answers = {}
    for c in calls:
        answers.setdefault(c["answer"], []).append(c)
    out["answers_at_the_time"] = {k: len(v) for k, v in sorted(answers.items())}
    out["empty_answers"] = [{"cid": c["cid"], "identifier_rule": rule[c["cid"]], "attempt": c["attempt"],
                             "retry": c["retry"]} for c in answers.get("none", [])]

    # #745's labels: the long queries whose current answer held no useful fact.
    res = json.load(open(res745_path))
    q745 = {json.loads(line)["qid"]: json.loads(line)["text"] for line in open(q745_path)}
    by_text = {}
    for c in calls:
        by_text.setdefault(c["text"], c["cid"])
    misses = [r["qid"] for r in res["per_query"] if "U" not in r["labels"]["current"]]
    out["misses_745"] = [{"qid": q, "cid": by_text.get(q745[q]), "identifier_rule": identifier(q745[q]),
                          "hand_read": hand.get(by_text.get(q745[q]))} for q in misses]

    words = sorted(len(c["text"].split()) for c in calls)
    out["words_median"] = words[len(words) // 2]
    json.dump(out, open(out_path, "w"), indent=1)
    print(json.dumps(out, indent=1))


if __name__ == "__main__":
    main()
