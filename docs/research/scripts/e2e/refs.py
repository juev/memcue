#!/usr/bin/env python3
"""Full references: the whole assistant text of the prompt's turn in the real session, head and tail.

Run 1 took the first 1500 characters of the turn, which left 13 of 41 references nearly empty (the turn went through
tools and said little first). Writes prompts-all.json with the new `reply` and `reply_chars`, keeps the old one as
`reply_v1`, and ids-v2.json with the prompts whose turn has at least MIN characters of text.
"""
import glob, json, os, re

E = os.path.dirname(os.path.abspath(__file__))
MIN = 300


def norm(t):
    return re.sub(r"\s+", " ", t).strip().lower()


def text_of(msg):
    c = msg.get("content")
    if isinstance(c, str):
        return c
    if isinstance(c, list):
        return "\n".join(b.get("text", "") for b in c if isinstance(b, dict) and b.get("type") == "text")
    return ""


def is_human(e):
    """A user entry the person typed, not a tool result."""
    if e.get("type") != "user" or e.get("toolUseResult") is not None or e.get("isMeta"):
        return False
    t = text_of(e.get("message", {})).lstrip()
    # background-task notices and harness inserts arrive as user entries but belong to the same turn
    return bool(t) and not t.startswith(("<task-notification>", "<system-reminder>", "<local-command",
                                         "<command-name>", "Caveat:", "[IMPORTANT"))


def turn_text(path, prompt):
    lines = [json.loads(l) for l in open(path) if l.strip()]
    key = norm(prompt)[:200]
    for i, e in enumerate(lines):
        if is_human(e) and norm(text_of(e["message"]))[:200] == key:
            out = []
            for f in lines[i + 1:]:
                if is_human(f):
                    break
                if f.get("type") == "assistant":
                    t = text_of(f.get("message", {})).strip()
                    if t:
                        out.append(t)
            return "\n".join(out)
    return None


def main():
    log = [json.loads(l) for l in open(os.path.expanduser("~/.local/state/memcue/recall.log")) if l.strip()]
    session = {}
    for r in log:
        session.setdefault(norm(r["prompt"]), r["session"])
    ps = json.load(open(os.path.join(E, "prompts-all.json")))
    ids = set(json.load(open(os.path.join(E, "ids.json"))))
    keep = []
    for p in ps:
        if p["id"] not in ids:
            continue
        sid = session.get(norm(p["prompt"]))
        hits = glob.glob(os.path.expanduser(f"~/.claude/projects/*/{sid}.jsonl")) if sid else []
        full = turn_text(hits[0], p["prompt"]) if hits else None
        if full is None:
            print("no turn", p["id"])
            continue
        p.setdefault("reply_v1", p["reply"])
        p["reply_chars"] = len(full)
        p["reply"] = full if len(full) <= 3500 else full[:1000] + "\n[…]\n" + full[-2500:]
        if len(full) >= MIN:
            keep.append(p["id"])
    json.dump(ps, open(os.path.join(E, "prompts-all.json"), "w"), ensure_ascii=False)
    json.dump(sorted(keep), open(os.path.join(E, "ids-v2.json"), "w"))
    short = sorted(ids - set(keep))
    print(f"prompts {len(ids)}, with a reference of {MIN}+ chars {len(keep)}, dropped {short}")


main()
