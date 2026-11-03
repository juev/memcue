#!/usr/bin/env python3
"""Blind filter of the #606 candidates: Opus through `claude -p` reads each prompt alone, without its session, and says
whether a fresh session could act on it, whether it is a task rather than a go-ahead, and whether the right answer may
depend on what the person decided or asked for in earlier sessions.

    filter.py --in DATA/candidates.json --out DATA/filter.json [--batch 60] [--jobs 4]

Only the prompt text and its repository's base name are sent; the reference answer is not.
"""
import argparse, json, os, subprocess, tempfile
from concurrent.futures import ThreadPoolExecutor

SYSTEM = ("You label prompts a person typed to a coding agent. Each prompt is read alone, without the conversation "
          "it came from. Answer only with the JSON the schema asks for.")
TASK = """For each prompt below decide three things:

- standalone: true when an agent starting a fresh session in the named repository could act on the prompt as it is.
  False when it refers to something only the earlier conversation shows ("do it", "the second option", "that error",
  "continue", a reply to the agent's question, a pasted fragment without the question).
- task: true when it asks for work or an answer of substance; false for a go-ahead, thanks, a greeting, a status
  question about a running job, or a bare yes/no.
- memory: true when a good answer may depend on what this person decided, asked for or set up in earlier sessions
  (their rules of work, conventions, decisions about the project, how their hosts or tools are set up); false when the
  repository and general knowledge are enough.

Prompts (JSON):
"""
SCHEMA = json.dumps({"type": "object", "additionalProperties": False, "required": ["labels"], "properties": {
    "labels": {"type": "array", "items": {"type": "object", "additionalProperties": False,
                                          "required": ["id", "standalone", "task", "memory"],
                                          "properties": {"id": {"type": "integer"}, "standalone": {"type": "boolean"},
                                                         "task": {"type": "boolean"}, "memory": {"type": "boolean"}}}}}})


def ask(batch):
    items = [{"id": p["id"], "repository": os.path.basename(p["root"]), "prompt": p["prompt"]} for p in batch]
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
    ap.add_argument("--in", dest="inp", required=True)
    ap.add_argument("--out", required=True)
    ap.add_argument("--batch", type=int, default=60)
    ap.add_argument("--jobs", type=int, default=4)
    a = ap.parse_args()
    ps = json.load(open(a.inp))
    batches = [ps[i:i + a.batch] for i in range(0, len(ps), a.batch)]
    with ThreadPoolExecutor(a.jobs) as ex:
        res = list(ex.map(ask, batches))
    labels = {l["id"]: l for ls, _ in res for l in ls}
    missing = [p["id"] for p in ps if p["id"] not in labels]
    json.dump(labels, open(a.out, "w"), indent=1)
    keep = [i for i, l in labels.items() if l["standalone"] and l["task"]]
    print(f"labelled {len(labels)} of {len(ps)} (missing {missing}), standalone tasks {len(keep)}, "
          f"of them memory {sum(labels[i]['memory'] for i in keep)}, cost ${sum(c for _, c in res):.2f}")


if __name__ == "__main__":
    main()
