#!/usr/bin/env python3
"""Collect every default `memcue search` call of this machine's Claude Code sessions with its context (memcue #746).

    extract.py PROJECTS_DIR OUT.jsonl

The calls are those of #745 (messagerule745/extract.py parses them the same way): Bash tool calls with a literal
query, outside temporary directories, with the default floor (no -min-score, no -all-words), here of any length.
For each call it keeps what the agent had and what it got:

- task: the last message the user typed before the call in the same transcript (slash commands, hook and command
  output and tool results left out); in a subagent's transcript, its task prompt when no later user message came.
- answer: what the search printed at the time, as `found` (fact lines), `none` ("No stored facts match.") or
  `other` (-json output, an error, or several searches in one command whose joint output cannot be split).
- retry: whether the agent ran another search within the same turn, before the next user message.

The output holds query and task text, so it stays out of the repository.
"""
import json
import os
import re
import sys

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "messagerule745"))
from extract import calls_in, literal, temporary  # noqa: E402

FACT_LINE = re.compile(r"^[0-9A-HJKMNP-TV-Z]{26} ", re.M)
NOT_TYPED = ("<command-", "<local-command", "<system-reminder>", "Caveat:", "<bash-", "<task-notification>",
             "[Request interrupted", "<user-prompt-submit-hook>")


def typed(e):
    """The text the user typed in a user entry, or None for tool results, meta and machine text."""
    if e.get("type") != "user" or e.get("isMeta"):
        return None
    c = (e.get("message") or {}).get("content")
    if isinstance(c, list):
        if any(x.get("type") == "tool_result" for x in c):
            return None
        c = "\n".join(x.get("text", "") for x in c if x.get("type") == "text")
    if not isinstance(c, str):
        return None
    t = c.strip()
    if not t or t.startswith(NOT_TYPED):
        return None
    return t


def result_text(x):
    c = x.get("content")
    if isinstance(c, list):
        c = "\n".join(y.get("text", "") for y in c if isinstance(y, dict))
    return c or ""


def answer_of(text, calls):
    """found / none / other for one of `calls` searches whose output is text. Several searches in one command print
    into one output; it splits only when every one of them found nothing."""
    if calls > 1:
        if not FACT_LINE.search(text) and text.count("No stored facts match.") == calls:
            return "none", 0
        return "other", 0
    n = len(FACT_LINE.findall(text))
    if n:
        return "found", n
    if "No stored facts match." in text:
        return "none", 0
    return "other", 0


def scan(path, stats, rows):
    entries = []
    with open(path, errors="replace") as f:
        for line in f:
            try:
                entries.append(json.loads(line))
            except ValueError:
                pass
    results = {}
    for e in entries:
        c = (e.get("message") or {}).get("content")
        if e.get("type") == "user" and isinstance(c, list):
            for x in c:
                if x.get("type") == "tool_result":
                    results[x.get("tool_use_id")] = result_text(x)
    task, turn, pending = None, 0, []
    subagent = "/subagents/" in path
    for e in entries:
        t = typed(e)
        if t is not None:
            task = t[:4000]
            turn += 1
            continue
        c = (e.get("message") or {}).get("content")
        if e.get("type") != "assistant" or not isinstance(c, list):
            continue
        for x in c:
            if x.get("type") != "tool_use" or x.get("name") != "Bash":
                continue
            cmd = (x.get("input") or {}).get("command", "")
            if "memcue" not in cmd or "search" not in cmd:
                continue
            calls = calls_in(cmd)
            for (flags, words), cd, seg in calls:
                stats["calls"] += 1
                if not literal(words, seg) or not words:
                    stats["not_literal"] += 1
                    continue
                if temporary(e.get("cwd") or ""):
                    stats["temp_session"] += 1
                    continue
                if "min-score" in flags or flags.get("all-words"):
                    stats["explicit_floor"] += 1
                    continue
                stats["default"] += 1
                answer, n = answer_of(results.get(x.get("id"), ""), len(calls))
                rows.append({"text": " ".join(words), "in": flags.get("in", ""), "kind": flags.get("kind", ""),
                             "cwd": flags.get("cwd") or cd or e.get("cwd") or "", "time": e.get("timestamp"),
                             "session": e.get("sessionId"), "subagent": subagent, "turn": (path, turn),
                             "task": task, "answer": answer, "facts": n})
    # A retry is another default search later in the same turn of the same transcript.
    by_turn = {}
    for r in rows:
        if isinstance(r["turn"], tuple) and r["turn"][0] == path:
            by_turn.setdefault(r["turn"], []).append(r)
    for group in by_turn.values():
        for i, r in enumerate(group):
            r["retry"] = i + 1 < len(group)
            r["attempt"] = i + 1


def main():
    projects, out_path = sys.argv[1:3]
    stats = {"calls": 0, "not_literal": 0, "temp_session": 0, "explicit_floor": 0, "default": 0}
    rows = []
    for root, _, files in os.walk(projects):
        for name in sorted(files):
            if name.endswith(".jsonl"):
                scan(os.path.join(root, name), stats, rows)
    rows.sort(key=lambda r: (r["time"] or "", r["text"]))
    seen, out = {}, []
    for r in rows:
        r["turn"] = f"{os.path.basename(r['turn'][0])}#{r['turn'][1]}"
        key = (r["text"], r["in"], r["kind"], r["cwd"])
        if key in seen:
            seen[key]["calls"] += 1
            continue
        r["calls"] = 1
        r["cid"] = "C%04d" % (len(out) + 1)
        seen[key] = r
        out.append(r)
    stats["distinct"] = len(out)
    stats["no_task"] = sum(r["task"] is None for r in out)
    with open(out_path, "w") as f:
        for r in out:
            f.write(json.dumps(r, ensure_ascii=False) + "\n")
    json.dump(stats, sys.stdout, indent=1)
    print()


if __name__ == "__main__":
    main()
