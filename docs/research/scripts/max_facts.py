#!/usr/bin/env python3
"""How many facts one prompt should get: the MaxFacts cap, measured on live prompts (#307).

The cap (`recall.Default.MaxFacts`, 2) is applied after the ranking, so the arms — 1, 2, 3, 4 facts — are prefixes of
one run, not four re-rankings. `memcue recall -explain` prints every fact above the judge's threshold in its own order
(`served`, then `cut by max facts`), so one run gives every arm at once.

Stages:

    prompts  — build the sample from a Hermes state.db (real user messages, harness notices dropped);
    arms     — run `recall -explain` per prompt and keep the `final:` block (one file per pass: the difference between
               two passes is the noise floor);
    label-tasks — emit the blind labeling tasks (prompt + first reply + the facts of every arm);
    merge-labels — read the labeler's answers back into one labels.json;
    report   — the arms table, the noise floor and the third-fact breakdown.

The labeler itself is an agent or a model: it sees the tasks with no arm and no score, and answers
`{"<id>": {"needed": true, "labels": {"F1": "U", "F2": "R"}}}` (U useful, R related, N noise).

Needs a copy of the base, never the live one: `recall` opens the base it is given and may migrate it. The judge comes
from MEMCUE_JUDGE_URL / _MODEL / _KEY; without it only the words decide.

    python3 max_facts.py prompts --db ~/.hermes/state.db --out work/prompts.json
    python3 max_facts.py arms --bin ./memcue --base /tmp/facts.db --prompts work/prompts.json \\
        --out work/pass1.json --state work/state1
    python3 max_facts.py report --prompts work/prompts.json --arms work/pass1.json work/pass2.json \\
        --labels work/labels.json
"""
import argparse
import json
import os
import re
import sqlite3
import subprocess
import sys

HARNESS = ("[ASYNC DELEGATION", "[Cron delivery", "Gateway message origin", "[System note", "<task-notification>",
           "Background process", "[IMPORTANT")


def norm(text):
    return re.sub(r"\s+", " ", text).strip().lower()


def is_harness(prompt):
    return prompt.lstrip().startswith(HARNESS)


def cmd_prompts(args):
    con = sqlite3.connect(f"file:{args.db}?mode=ro", uri=True)
    con.row_factory = sqlite3.Row
    rows = con.execute(
        "select m.content, m.timestamp, s.id as sid, s.cwd from messages m join sessions s on s.id = m.session_id"
        " where m.role = 'user' and length(m.content) > ? and s.source not in ('cron', 'subagent')"
        " order by m.timestamp asc", (args.min_chars,)).fetchall()
    seen, out = set(), []
    for row in rows:
        text = row["content"]
        if is_harness(text) or norm(text) in seen:
            continue
        seen.add(norm(text))
        reply = con.execute(
            "select content from messages where session_id = ? and role = 'assistant' and timestamp > ?"
            " and content != '' order by timestamp asc limit 1", (row["sid"], row["timestamp"])).fetchone()
        out.append({"id": len(out), "prompt": text, "cwd": row["cwd"] or ".", "reply": reply[0][:1500] if reply else ""})
    json.dump(out, open(args.out, "w"), ensure_ascii=False, indent=1)
    harness = sum(1 for r in rows if is_harness(r["content"]))
    print(f"prompts {len(out)} (dropped {harness} harness notices and {len(rows) - harness - len(out)} repeats)")


FINAL = re.compile(r"^\s+(served|cut by max facts)\s+(\S+)\s+\|\s*(.*)$")


def cmd_arms(args):
    prompts = json.load(open(args.prompts))
    env = {k: v for k, v in os.environ.items() if not k.startswith("MEMCUE_") or k in
           ("MEMCUE_JUDGE_URL", "MEMCUE_JUDGE_MODEL", "MEMCUE_JUDGE_KEY")}
    env.update(MEMCUE_FACTS=args.base, MEMCUE_STATE_DIR=args.state)
    os.makedirs(args.state, exist_ok=True)
    out = []
    for p in prompts:
        r = subprocess.run([args.bin, "recall", "-explain", "-cwd", p["cwd"], p["prompt"]],
                           capture_output=True, text=True, env=env, timeout=120)
        final, fallback = [], ""
        for line in r.stdout.splitlines():
            m = FINAL.match(line)
            if m:
                final.append({"id": m.group(2), "content": m.group(3), "verdict": m.group(1)})
            elif "fallback" in line or "no judge configured" in line:
                fallback = line.strip()
        out.append({"id": p["id"], "fallback": fallback, "final": final})
    json.dump(out, open(args.out, "w"), ensure_ascii=False)
    print(f"arms {len(out)}, with facts {sum(1 for r in out if r['final'])},"
          f" three or more {sum(1 for r in out if len(r['final']) >= 3)}, fallbacks {sum(1 for r in out if r['fallback'])}")


def facts_by_prompt(prompts, arms, cap):
    served = {r["id"]: r for r in arms}
    out = {}
    for p in prompts:
        r = served[p["id"]]
        if r["fallback"] or not r["final"]:
            continue
        out[p["id"]] = [{"key": f"F{n + 1}", "text": f["content"], "id": f["id"]}
                        for n, f in enumerate(r["final"][:cap])]
    return out


def cmd_label_tasks(args):
    prompts = json.load(open(args.prompts))
    arms = json.load(open(args.arms))
    tasks = []
    for p in prompts:
        facts = facts_by_prompt([p], arms, args.cap).get(p["id"])
        if facts:
            tasks.append({"id": p["id"], "prompt": p["prompt"][:2500], "reply": p["reply"][:1200],
                          "facts": [{"key": f["key"], "text": f["text"]} for f in facts]})
    json.dump(tasks, open(args.out, "w"), ensure_ascii=False, indent=1)
    print(f"tasks {len(tasks)}, pairs {sum(len(t['facts']) for t in tasks)}")


def cmd_merge_labels(args):
    tasks = json.load(open(args.tasks))
    labeled = json.load(open(args.labeled))
    labels, bad = {}, []
    for t in tasks:
        answer = labeled.get(str(t["id"])) or {}
        got = answer.get("labels") or {}
        if sorted(got) != sorted(f["key"] for f in t["facts"]) or any(v not in "URN" for v in got.values()):
            bad.append(t["id"])
            continue
        labels[str(t["id"])] = {"needed": bool(answer.get("needed")), "labels": got}
    if bad:
        sys.exit(f"labeler lost or added pairs for prompts {bad[:8]}: {len(bad)} of {len(tasks)}")
    json.dump(labels, open(args.out, "w"), ensure_ascii=False, indent=1)
    print(f"labels {len(labels)}")


def load(prompts_path, arms_path, labels_path):
    prompts = {p["id"]: p for p in json.load(open(prompts_path))}
    arms = {r["id"]: r for r in json.load(open(arms_path))}
    labels = {int(k): v for k, v in json.load(open(labels_path)).items()}
    return prompts, arms, labels


def cmd_report(args):
    prompts, arms, labels = load(args.prompts, args.arms[0], args.labels)
    clean = [i for i in prompts if not is_harness(prompts[i]["prompt"])]
    with_facts = [i for i in clean if arms[i]["final"]]
    print(f"prompts {len(prompts)}, clean {len(clean)}, with facts {len(with_facts)},"
          f" two or more {sum(1 for i in clean if len(arms[i]['final']) >= 2)},"
          f" three or more {sum(1 for i in clean if len(arms[i]['final']) >= 3)},"
          f" fallbacks {sum(1 for i in clean if arms[i]['fallback'])}, labelled {len([i for i in clean if i in labels])}")
    if len(args.arms) > 1:
        second = {r["id"]: r for r in json.load(open(args.arms[1]))}
        same2 = sum(1 for i in clean
                    if [f["id"] for f in arms[i]["final"][:2]] == [f["id"] for f in second[i]["final"][:2]])
        same = sum(1 for i in clean
                   if {f["id"] for f in arms[i]["final"]} == {f["id"] for f in second[i]["final"]})
        print(f"noise floor: same top-2 {same2}/{len(clean)}, same eligible set {same}/{len(clean)}")

    judged = [i for i in clean if i in labels]  # a prompt without facts has nothing to label
    print("\ncap  facts  useful  related  noise  prompts-with-useful  chars")
    for cap in range(1, args.caps + 1):
        facts = useful = related = noise = with_useful = chars = 0
        for i in judged:
            got = [labels[i]["labels"][f"F{n + 1}"] for n in range(cap) if f"F{n + 1}" in labels[i]["labels"]]
            facts += len(got)
            useful += got.count("U")
            related += got.count("R")
            noise += got.count("N")
            with_useful += 1 if "U" in got else 0
            chars += sum(len(arms[i]["final"][n]["content"]) for n in range(len(got)))
        print(f"{cap:3d}  {facts:5d}  {useful:6d}  {related:7d}  {noise:5d}  {with_useful:19d}  {chars:5d}")

    thirds = [i for i in judged if len(labels[i]["labels"]) >= 3]
    third = [labels[i]["labels"]["F3"] for i in thirds]
    print(f"\nthird fact: prompts {len(thirds)}, useful {third.count('U')}, related {third.count('R')},"
          f" noise {third.count('N')}, of the useful ones next to a useful pair"
          f" {sum(1 for i in thirds if labels[i]['labels']['F3'] == 'U' and 'U' in (labels[i]['labels']['F1'], labels[i]['labels']['F2']))}")
    for needed in (True, False):
        keys = [i for i in judged if labels[i]["needed"] == needed]
        for cap in (2, 3):
            got = [[labels[i]["labels"][f"F{n + 1}"] for n in range(cap) if f"F{n + 1}" in labels[i]["labels"]]
                   for i in keys]
            print(f"needed={str(needed):5s} cap {cap}: prompts {len(keys)}, useful {sum(g.count('U') for g in got)},"
                  f" prompts with a useful fact {sum(1 for g in got if 'U' in g)}")


def main():
    ap = argparse.ArgumentParser(description=(__doc__ or "").splitlines()[0])
    sub = ap.add_subparsers(dest="cmd", required=True)

    p = sub.add_parser("prompts")
    p.add_argument("--db", required=True, help="Hermes state.db (opened read-only)")
    p.add_argument("--out", required=True)
    p.add_argument("--min-chars", type=int, default=45)
    p.set_defaults(func=cmd_prompts)

    a = sub.add_parser("arms")
    a.add_argument("--bin", required=True)
    a.add_argument("--base", required=True, help="a copy of the fact base")
    a.add_argument("--prompts", required=True)
    a.add_argument("--out", required=True)
    a.add_argument("--state", required=True)
    a.set_defaults(func=cmd_arms)

    t = sub.add_parser("label-tasks")
    t.add_argument("--prompts", required=True)
    t.add_argument("--arms", required=True)
    t.add_argument("--cap", type=int, default=4)
    t.add_argument("--out", required=True)
    t.set_defaults(func=cmd_label_tasks)

    m = sub.add_parser("merge-labels")
    m.add_argument("--tasks", required=True)
    m.add_argument("--labeled", required=True)
    m.add_argument("--out", required=True)
    m.set_defaults(func=cmd_merge_labels)

    r = sub.add_parser("report")
    r.add_argument("--prompts", required=True)
    r.add_argument("--arms", nargs="+", required=True, help="one pass, or two for the noise floor")
    r.add_argument("--labels", required=True)
    r.add_argument("--caps", type=int, default=4)
    r.set_defaults(func=cmd_report)

    args = ap.parse_args()
    args.func(args)


if __name__ == "__main__":
    main()
