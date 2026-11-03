"""Blind labels of the #602 windows by Opus through `claude -p`, in the form aggregate.py binds (iid, seen_sha).

Run: python3 -I label602.py --queries Q.jsonl --results ARM_DIR/results.jsonl --out LABELS.jsonl [--memcue BIN]

One call a query: the query, its time, its windows newest first with their date and role, and the facts the base holds
now for the query's words (`memcue search -in all -n 5 -min-score 0`, so a window an existing fact contradicts can be
marked). For each window the model gives A (answers the query), R (related, does not answer) or N (noise), and for A
whether a newer window of the list or a fact outdates it. Only the text arm is labelled: tool output carries secrets
and fetched pages and does not leave the machine.
"""

import argparse
import hashlib
import json
import subprocess
import sys
import tempfile
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path

PROMPT = """You label search results from an AI coding agent's past session history. Everything is private history;
judge only from what is below. You have no tools.

The agent searched its long-term memory with `query` at `time` and found nothing. These `windows` are passages of
earlier sessions (newest first) that a plain word search over the transcripts returned. `facts` are what the memory
holds now for the same words; they may be newer than the query.

For each window decide:
- label: "A" when the passage answers what the query was looking for (the agent could act on it), "R" when it is on
  the same subject but does not answer, "N" when it only shares words.
- outdated (only matters for "A"): "newer" when a newer window of the list states something else about the same thing,
  "fact" when one of the facts states something else, otherwise "none".

Answer with one JSON object and nothing else: {"labels": [{"rank": 1, "label": "A|R|N", "outdated": "none|newer|fact"}]}
with one entry per window.

"""


def sha(text: str, n: int) -> str:
    return hashlib.sha256(text.encode()).hexdigest()[:n]


def base_facts(memcue: str, query: str) -> list[str]:
    r = subprocess.run([memcue, "search", "-in", "all", "-n", "5", "-min-score", "0", "-json", query],
                       capture_output=True, text=True, stdin=subprocess.DEVNULL)
    try:
        return [f.get("content") or f.get("text") or "" for f in json.loads(r.stdout).get("facts", [])]
    except (ValueError, AttributeError):
        return []


def ask(body: str) -> tuple[list, float]:
    with tempfile.TemporaryDirectory() as d:
        r = subprocess.run(["claude", "-p", "--model", "opus", "--tools", "", "--no-session-persistence",
                            "--settings", '{"disableAllHooks": true}', "--output-format", "json"],
                           input=PROMPT + body, capture_output=True, text=True, cwd=d, timeout=600)
    env = json.loads(r.stdout)
    text = env.get("result") or ""
    return json.loads(text[text.index("{"):text.rindex("}") + 1])["labels"], env.get("total_cost_usd") or 0


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--queries", required=True, type=Path)
    ap.add_argument("--results", required=True, type=Path)
    ap.add_argument("--out", required=True, type=Path)
    ap.add_argument("--memcue", default="memcue")
    a = ap.parse_args()
    queries = {q["qid"]: q for q in map(json.loads, a.queries.read_text().splitlines()) if q}
    rows = [json.loads(l) for l in a.results.read_text().splitlines() if l.strip()]
    todo = [r for r in rows if r["windows"]]

    def one(r):
        q = queries[r["qid"]]
        body = json.dumps({"query": q["query"], "time": r["query_time"], "facts": base_facts(a.memcue, q["query"]),
                           "windows": [{"rank": w["rank"], "date": w["ts"][:10], "role": w["role"], "text": w["text"]}
                                       for w in r["windows"]]}, ensure_ascii=False)
        labels, cost = ask(body)
        by = {x["rank"]: x for x in labels}
        out = []
        for w in r["windows"]:
            x = by.get(w["rank"])
            if not x or x.get("label") not in ("A", "R", "N"):
                continue
            out.append({"iid": sha(f"{r['qid']}\x00{w['uid']}\x00{w['text']}", 12), "label": x["label"],
                        "outdated": x.get("outdated") if x.get("outdated") in ("none", "newer", "fact") else "none",
                        "seen_sha": sha("  " + w["text"], 16)})
        return out, cost

    with ThreadPoolExecutor(4) as ex:
        res = list(ex.map(one, todo))
    with a.out.open("w") as f:
        for labels, _ in res:
            for lab in labels:
                f.write(json.dumps(lab) + "\n")
    print(f"labelled {sum(len(l) for l, _ in res)} windows of {sum(len(r['windows']) for r in todo)} in "
          f"{len(todo)} queries, cost ${sum(c for _, c in res):.2f}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
