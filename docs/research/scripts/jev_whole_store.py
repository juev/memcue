#!/usr/bin/env python3
"""Ask Jev one question per stored fact, in a single request, instead of searching an index.

Two modes:

  dedup  - "does stored fact [k] state the same claim as the new fact", one `noul` per fact
  recall - "would stored fact [k] help answer the prompt", one `noul` per fact
  choice - the same dedup question as one `choice` over every fact, for comparison; a `choice`
           dictionary holds at most 255 options, so this fails on any larger base

The whole active base goes into `state`, so no candidate generation happens on our side: this is
the cost and the ceiling of using Jev as the retriever rather than as a judge over candidates.

    python3 -I jev_whole_store.py --facts FACTS.db --key-file KEY --dedup "the new fact"
    python3 -I jev_whole_store.py --facts FACTS.db --key-file KEY --recall "the prompt"

Prints one line per fact above 0.5, a summary line on stderr, and with --json the full answers.
"""

import argparse
import json
import sqlite3
import sys
import time
import urllib.error
import urllib.request

URL = "https://openrouter.ai/api/alpha/decisions"
DEFAULT_MODEL = "typesafe/jev-1.14"
PRICE_PER_TOKEN = 0.042 / 1e6  # Jev input, output is free


def read_key(path):
    with open(path, encoding="utf-8") as f:
        key = f.read().strip()
    if not key:
        sys.exit("empty key file")
    return key


def active_facts(path):
    db = sqlite3.connect("file:%s?mode=ro" % path, uri=True)
    rows = db.execute(
        """
        select id, content from facts
         where status = 'active'
           and version = (select max(version) from facts f where f.id = facts.id)
         order by created desc
        """
    ).fetchall()
    db.close()
    return rows


def ask(key, body, long_context):
    headers = {"Authorization": "Bearer " + key, "Content-Type": "application/json"}
    if long_context:
        headers["TypeSafe-Danger-Zone-Long-Context"] = "true"
    req = urllib.request.Request(URL, data=json.dumps(body).encode(), headers=headers)
    started = time.perf_counter()
    try:
        with urllib.request.urlopen(req, timeout=300) as resp:
            answer = json.loads(resp.read())
    except urllib.error.HTTPError as err:
        sys.exit("HTTP %s: %s" % (err.code, err.read()[:600].decode(errors="replace")))
    return answer, time.perf_counter() - started


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--facts", required=True, help="path to facts.db")
    ap.add_argument("--key-file", required=True, help="file holding the OpenRouter API key")
    ap.add_argument("--model", default=DEFAULT_MODEL)
    ap.add_argument("--dedup", metavar="TEXT", help="new fact to check against the whole base")
    ap.add_argument("--recall", metavar="TEXT", help="prompt to check every fact against")
    ap.add_argument("--choice", action="store_true",
                    help="ask the dedup question as one choice over all facts instead of one noul per fact")
    ap.add_argument("--long-context", action="store_true")
    ap.add_argument("--limit", type=int, help="use only the newest N facts")
    ap.add_argument("--top", type=int, default=10)
    ap.add_argument("--json", metavar="OUT")
    args = ap.parse_args()
    if bool(args.dedup) == bool(args.recall):
        sys.exit("give exactly one of --dedup or --recall")
    if args.choice and not args.dedup:
        sys.exit("--choice only applies to --dedup")

    if args.dedup:
        head = "New fact: " + args.dedup
        question = ("Does stored fact [%d] state the same claim as the new fact, so that storing the "
                    "new fact would add nothing?")
    else:
        head = "Prompt: " + args.recall
        question = "Would stored fact [%d] help answer the prompt?"

    facts = active_facts(args.facts)
    if args.limit:
        facts = facts[: args.limit]
    state = [head, "Stored facts:"] + ["[%d] %s" % (k, text) for k, (_, text) in enumerate(facts, 1)]
    if args.choice:
        questions = {"match": {
            "type": "choice",
            "instructions": ("Which stored fact already states the same claim as the new fact? Judge "
                             "meaning only; the new fact may be worded differently or in another language."),
            "criteria": {"[%d]" % k: "stored entry %d" % k for k in range(1, len(facts) + 1)},
        }}
    else:
        questions = {"f%d" % k: {"type": "noul", "instructions": question % k}
                     for k in range(1, len(facts) + 1)}
    answer, seconds = ask(read_key(args.key_file),
                          {"model": args.model, "state": state, "questions": questions},
                          args.long_context)

    if args.choice:
        picked = answer["answers"]["match"]
        probs = picked.get("probabilities") or {}
        scores = [(prob, facts[int(k.strip("[]")) - 1][0], facts[int(k.strip("[]")) - 1][1])
                  for k, prob in probs.items()]
    else:
        picked = None
        scores = [(answer["answers"]["f%d" % k]["noul"], fid, text)
                  for k, (fid, text) in enumerate(facts, 1)]
    scores.sort(key=lambda row: -row[0])
    usage = answer["usage"]
    stderr = ("%d facts, %.2fs, %d input tokens, $%.6f (%s)"
              % (len(facts), seconds, usage["input_tokens"],
                 usage.get("cost", usage["input_tokens"] * PRICE_PER_TOKEN), answer["model"]))
    if picked:
        stderr += ", picked %s, confidence %s" % (picked.get("choice"), picked.get("confidence"))
    print(stderr, file=sys.stderr)
    for score, fid, text in scores[: args.top]:
        print("%.3f %s %s" % (score, fid, text.replace("\n", " ")[:100]))
    if args.json:
        with open(args.json, "w", encoding="utf-8") as f:
            json.dump({"usage": usage, "seconds": seconds, "answers": answer["answers"]},
                      f, ensure_ascii=False, indent=1)


if __name__ == "__main__":
    main()
