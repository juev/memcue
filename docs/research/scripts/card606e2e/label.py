#!/usr/bin/env python3
"""Label the facts the #606 cards carry by the channel that should carry them, and write the trimmed card's keep list.

    label.py --data DATA [--batch 50] [--jobs 4]

Reads DATA/cards.json (the ids each prompt's current card carries) and the live text history for each fact's latest
text and labels; Opus through `claude -p` gives each fact one class of the #606 table. The trimmed card keeps the
behaviour facts, and the rules and decisions of a repository that the user stated (source user or user_confirmed):
#606 question 1. Writes DATA/labels.json and DATA/keep.txt.
"""
import argparse, json, os, subprocess, tempfile
from concurrent.futures import ThreadPoolExecutor

LIVE = os.path.expanduser("~/.claude/memory/facts")
CLASSES = ["behaviour", "repository", "file", "command", "knowledge", "outdated"]
SYSTEM = ("You sort the facts of a coding agent's memory by how they should reach the agent. Answer only with the JSON "
          "the schema asks for.")
TASK = """The agent's memory shows some facts on a card at the start of every session; the rest it finds by searching
when a task calls for them, and some reach it when it runs a program or opens a file. Give each fact below the one
channel that should carry it:

- behaviour: how to work or answer, with no moment at which the agent would think to search for it ("write the
  commit messages in English", "measure before filing a task", "do not wait for confirmation to merge").
- repository: a rule or decision that matters only while working in one repository ("in repository A, release only
  after the parallel task is merged").
- file: matters when editing a particular file ("never change the server name in that config file").
- command: matters when running a particular program or command ("copy into that directory only as its owner").
- knowledge: a fact to look up when a task needs it: how something is set up, where it is, what a value is, what was
  measured or decided as background ("the proxy listens on port N").
- outdated: a one-off or temporary state that no longer guides work ("the first update has not happened yet").

Facts (JSON; scope and source are where the fact holds and who said it):
"""
SCHEMA = json.dumps({"type": "object", "additionalProperties": False, "required": ["labels"], "properties": {
    "labels": {"type": "array", "items": {"type": "object", "additionalProperties": False,
                                          "required": ["id", "class"],
                                          "properties": {"id": {"type": "string"},
                                                         "class": {"type": "string", "enum": CLASSES}}}}}})


def latest(fid):
    lines = [l for l in open(os.path.join(LIVE, fid[-1], fid + ".jsonl")) if l.strip()]
    return json.loads(lines[-1])


def ask(batch):
    items = [{"id": f["id"], "kind": f.get("kind"), "scope": f.get("scope") or "", "source": f.get("source") or "",
              "text": f["content"]} for f in batch]
    with tempfile.TemporaryDirectory() as d:
        r = subprocess.run(["claude", "-p", "--model", "opus", "--tools", "", "--no-session-persistence",
                            "--settings", '{"disableAllHooks": true}', "--system-prompt", SYSTEM,
                            "--json-schema", SCHEMA, "--output-format", "json"],
                           input=TASK + json.dumps(items, ensure_ascii=False), capture_output=True, text=True, cwd=d,
                           check=True)
    env = json.loads(r.stdout)
    if env.get("is_error"):
        raise SystemExit("claude: " + env.get("result", ""))
    return env["structured_output"]["labels"], env.get("total_cost_usd", 0)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--data", required=True)
    ap.add_argument("--batch", type=int, default=50)
    ap.add_argument("--jobs", type=int, default=4)
    a = ap.parse_args()
    cards = json.load(open(os.path.join(a.data, "cards.json")))
    ids = sorted({i for c in cards.values() for i in c["current"]["ids"]})
    facts = [latest(i) for i in ids]
    batches = [facts[i:i + a.batch] for i in range(0, len(facts), a.batch)]
    with ThreadPoolExecutor(a.jobs) as ex:
        res = list(ex.map(ask, batches))
    labels = {l["id"]: l["class"] for ls, _ in res for l in ls if l["id"] in ids}
    missing = [i for i in ids if i not in labels]
    keep = []
    for f in facts:
        stated = f.get("source") in ("user", "user_confirmed")
        project = (f.get("scope") or "").lower().startswith(("repo:", "dir:"))
        if labels.get(f["id"]) == "behaviour" or (stated and project and f.get("kind") in ("rule", "decision")):
            keep.append(f["id"])
    json.dump(labels, open(os.path.join(a.data, "labels.json"), "w"), indent=1)
    open(os.path.join(a.data, "keep.txt"), "w").write("\n".join(keep) + "\n")
    counts = {c: sum(1 for v in labels.values() if v == c) for c in CLASSES}
    print(f"facts {len(ids)}, labelled {len(labels)}, missing {len(missing)}, classes {counts}, keep {len(keep)}, "
          f"cost ${sum(c for _, c in res):.2f}")


if __name__ == "__main__":
    main()
