#!/usr/bin/env python3
"""Candidate prompts for the #606 card replay: what the person typed in an interactive Claude Code session, with the
whole assistant text of that turn as the reference.

    candidates.py --since 2026-10-26 --out DATA/candidates.json [--skip-session ID]...

A candidate is a prompt the person typed (not a tool result, a harness insert or a slash command) in a session started
from the terminal or the desktop app, not by `claude -p`; its working directory still exists and is a git repository
or the Claude config directory; the prompt holds 20-2000 characters and the turn's assistant text at least 300. The
reference is the turn's assistant text: head and tail when it is long, as e2e/refs.py cut it.
"""
import argparse, glob, json, os, subprocess


# text_of and is_human are those of e2e/refs.py, which the October set was built with; refs.py runs on import.
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
    return bool(t) and not t.startswith(("<task-notification>", "<system-reminder>", "<local-command",
                                         "<command-name>", "Caveat:", "[IMPORTANT"))

CLAUDE = os.path.expanduser("~/.claude")
INTERACTIVE = ("cli", "claude-desktop")


def repo_root(cwd):
    if os.path.realpath(cwd) == os.path.realpath(CLAUDE):
        return CLAUDE
    r = subprocess.run(["git", "-C", cwd, "rev-parse", "--path-format=absolute", "--git-common-dir"],
                       capture_output=True, text=True)
    return os.path.dirname(r.stdout.strip()) if r.returncode == 0 else ""


def cut(t):
    return t if len(t) <= 3500 else t[:1000] + "\n…\n" + t[-2500:]


def session_prompts(path, since):
    lines = []
    for l in open(path):
        try:
            lines.append(json.loads(l))
        except json.JSONDecodeError:
            continue
    entry = next((e.get("entrypoint") for e in lines if e.get("entrypoint")), None)
    if entry not in INTERACTIVE:
        return
    for i, e in enumerate(lines):
        if not is_human(e) or (e.get("timestamp") or "") < since:
            continue
        prompt = text_of(e["message"]).strip()
        if not 20 <= len(prompt) <= 2000 or prompt.startswith(("/", "<")):
            continue
        reply = []
        for f in lines[i + 1:]:
            if is_human(f):
                break
            if f.get("type") == "assistant":
                t = text_of(f.get("message", {})).strip()
                if t:
                    reply.append(t)
        reply = "\n".join(reply)
        if len(reply) < 300:
            continue
        yield {"session": e.get("sessionId"), "time": e["timestamp"], "cwd": e.get("cwd", ""), "prompt": prompt,
               "reply": cut(reply), "reply_chars": len(reply)}


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--since", required=True)
    ap.add_argument("--out", required=True)
    ap.add_argument("--skip-session", action="append", default=[])
    a = ap.parse_args()
    out, roots = [], {}
    for path in sorted(glob.glob(os.path.join(CLAUDE, "projects", "*", "*.jsonl"))):
        if os.path.basename(path)[:-6] in a.skip_session:
            continue
        for p in session_prompts(path, a.since):
            cwd = p["cwd"]
            if not cwd or not os.path.isdir(cwd) or "/tmp/" in cwd or cwd.startswith("/private/"):
                continue
            if cwd not in roots:
                roots[cwd] = repo_root(cwd)
            if not roots[cwd]:
                continue
            p["root"] = roots[cwd]
            out.append(p)
    for i, p in enumerate(sorted(out, key=lambda p: p["time"]), 1):
        p["id"] = i
    json.dump(out, open(a.out, "w"), ensure_ascii=False, indent=1)
    by = {}
    for p in out:
        by[p["root"]] = by.get(p["root"], 0) + 1
    print(f"{len(out)} candidates in {len(by)} repositories")


if __name__ == "__main__":
    main()
