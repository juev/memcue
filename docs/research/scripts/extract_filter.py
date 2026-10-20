"""Drop derivable and event facts with Jev noul; compare with blind labels. Args: KEY_FILE SIDE PAIR_DIR..."""
import json
import re
import sys
import urllib.request
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path

key = Path(sys.argv[1]).read_text().strip()
SIDE = sys.argv[2]
sets = sys.argv[3:]

Q = {
    "derivable": ("Can an agent working in this repository learn the fact with one command, by reading the repository's "
                  "files, its git history or its instruction files (CLAUDE.md, skills)?",
                  "Yes: it retells the repository's structure, paths, build, CI, configuration, release steps, or a fix "
                  "that the code and its commit show.",
                  "No: it is about a host, a server, a container, an external service or account, another project, a "
                  "tool's or library's behaviour, a measurement, or a rule or decision of the user."),
    "event": ("Is the fact a log entry of something that happened, which is true or matters for a day or two only?",
              "Yes: it records that something was updated to a version, started, deployed, passed, took some time, "
              "or the state of work in progress.",
              "No: it stays true and matters for later work, such as where something lives, how it behaves, a rule, "
              "a decision, a measurement behind a decision, or a move of a service."),
}

items = []
for d in map(Path, sets):
    for r in json.loads((d / "labels.json").read_text()):
        side = "A" if r["a"] == SIDE else "B"
        run = json.loads((d / SIDE / f"{r['session']}.json").read_text())
        facts = run.get("facts") or []
        repo = run.get("origin", "").split(" ")[0]
        for lab in r["result"]["labels"]:
            m = re.match(rf"\s*{side}(\d+)", lab["id"])
            if m and int(m.group(1)) <= len(facts):
                items.append({"set": d.name, "repo": repo, "text": facts[int(m.group(1)) - 1]["content"],
                              "label": lab["label"]})


def ask(it):
    qs = {k: {"type": "noul", "instructions": q, "criteria": {"true": t, "false": f}} for k, (q, t, f) in Q.items()}
    body = {"model": "typesafe/jev-1.14", "state": {"repository": it["repo"], "fact": it["text"]}, "questions": qs}
    req = urllib.request.Request("https://routerai.ru/api/v1/decisions", data=json.dumps(body).encode(),
                                 headers={"Authorization": f"Bearer {key}", "Content-Type": "application/json"})
    for _ in range(3):
        try:
            with urllib.request.urlopen(req, timeout=90) as r:
                a = json.load(r)
            return {**it, **{k: a["answers"][k]["noul"] for k in Q}, "tokens": a["usage"]["input_tokens"]}
        except Exception:  # retry transient errors
            pass
    return {**it, "error": True}


with ThreadPoolExecutor(8) as ex:
    res = list(ex.map(ask, items))
Path(sets[0]).parent.joinpath(f"filter-{SIDE}.json").write_text(json.dumps(res, ensure_ascii=False))
ok = [r for r in res if not r.get("error")]
print("facts", len(res), "errors", len(res) - len(ok), "tokens", sum(r["tokens"] for r in ok))
for name in sorted({r["set"] for r in ok}) + ["all"]:
    s = [r for r in ok if name in ("all", r["set"])]
    keep = [r for r in s if r["label"] == "KEEP"]
    junk = [r for r in s if r["label"] in ("DERIVABLE", "EVENT", "PROCEDURE")]
    print(f"{name}: {len(s)} facts, KEEP {len(keep)}, junk {len(junk)}")
    for t in (0.5, 0.6, 0.7, 0.8, 0.9):
        drop = lambda r: max(r["derivable"], r["event"]) >= t
        print(f"  t={t}: junk dropped {sum(map(drop, junk))}/{len(junk)}, KEEP lost {sum(map(drop, keep))}/{len(keep)}")
