#!/usr/bin/env python3
"""Collect the `memcue search` calls agents made in this machine's Claude Code sessions (memcue #745).

    extract.py PROJECTS_DIR SEARCH_LOG OUT.jsonl

Reads every transcript under PROJECTS_DIR (~/.claude/projects, subagents included) and takes each Bash tool call that
runs `memcue search` with a literal query: a call whose words hold a shell variable, a command substitution or a glob
is skipped. Flags are parsed as the CLI parses them, anywhere among the words. A call is kept when it uses the default
floor (no -min-score, no -all-words) and its query has more than eight words by recall.IsMessage's rule
(whitespace-separated). Sessions whose working directory is a temporary one (research stands, e2e fixtures) are left
out: they searched throwaway bases.

The search cwd is the call's -cwd, else a leading `cd DIR &&`, else the transcript's cwd. When that directory no
longer exists, the tier search.log recorded for the same query within two minutes names it, mapped to the checkout
~/src/<tier>; a call that resolves to no existing directory is counted and dropped. Duplicates (same text, flags and
directory) are kept once. The output holds query text, so it stays out of the repository.
"""
import json
import os
import shlex
import sys
from datetime import datetime, timedelta

VALUE_FLAGS = {"n", "min-score", "in", "kind", "since", "cwd", "session"}
BOOL_FLAGS = {"expired", "all-words", "json"}
OPERATORS = {";", "&&", "||", "|", "&", "(", ")", "\n", ";;", "|&"}
TEMP_ROOTS = ("/private/tmp", "/tmp", "/private/var/folders", "/var/folders", "/var/tmp")


def words_of(text):
    return len(text.split())


def parse_call(tokens):
    """tokens after `memcue search`, up to the end of the simple command: (flags, query words) or None."""
    flags, words, i, rest_words = {}, [], 0, False
    while i < len(tokens):
        t = tokens[i]
        if rest_words:
            words.append(t)
        elif t == "--":
            rest_words = True
        elif t.startswith("-") and len(t) > 1 and not t[1:2].isdigit():
            name = t.lstrip("-")
            value = None
            if "=" in name:
                name, value = name.split("=", 1)
            if name in BOOL_FLAGS:
                flags[name] = True if value is None else value
            elif name in VALUE_FLAGS:
                if value is None:
                    i += 1
                    if i >= len(tokens):
                        return None
                    value = tokens[i]
                flags[name] = value
            else:
                return None  # unknown flag: the CLI would fail
        else:
            words.append(t)
        i += 1
    return flags, words


def calls_in(command):
    """Every literal `memcue search` call in a shell command, with the directory of a leading cd."""
    try:
        lex = shlex.shlex(command, posix=True, punctuation_chars=";&|()")
        lex.whitespace_split = True
        lex.commenters = ""
        tokens = list(lex)
    except ValueError:
        return []
    # A redirection token ends the call's words.
    out, cd = [], None
    i = 0
    while i < len(tokens):
        t = tokens[i]
        if t == "cd" and i + 1 < len(tokens):
            cd = tokens[i + 1]
        if (t == "memcue" or t.endswith("/memcue")) and i + 1 < len(tokens) and tokens[i + 1] == "search":
            j = i + 2
            seg = []
            while j < len(tokens) and tokens[j] not in OPERATORS and not tokens[j].startswith((">", "2>", "<")) \
                    and tokens[j] not in ("2>&1", "</dev/null", ">/dev/null"):
                seg.append(tokens[j])
                j += 1
            parsed = parse_call(seg)
            if parsed is not None:
                out.append((parsed, cd, seg))
            i = j
            continue
        i += 1
    return out


def literal(words, command_seg):
    return not any(("$" in w or "`" in w or "*" in w) for w in command_seg)


def temporary(d):
    d = os.path.realpath(d) if os.path.exists(d) else d
    return d.startswith(TEMP_ROOTS)


def load_log(path):
    by_query = {}
    with open(path) as f:
        for line in f:
            try:
                r = json.loads(line)
            except ValueError:
                continue
            if r.get("caller"):
                continue
            by_query.setdefault(r.get("query", ""), []).append(r)
    return by_query


def parse_time(s):
    return datetime.fromisoformat(s.replace("Z", "+00:00"))


def main():
    projects, log_path, out_path = sys.argv[1:4]
    log = load_log(log_path)
    stats = {"calls": 0, "not_literal": 0, "temp_session": 0, "explicit_floor": 0, "short": 0, "long_default": 0,
             "no_dir": 0, "resolved_by_log": 0, "kept": 0}
    seen, rows = set(), []
    home = os.path.expanduser("~")
    for root, _, files in os.walk(projects):
        for name in files:
            if not name.endswith(".jsonl"):
                continue
            path = os.path.join(root, name)
            with open(path, errors="replace") as f:
                for line in f:
                    if "memcue" not in line or "search" not in line:
                        continue
                    try:
                        e = json.loads(line)
                    except ValueError:
                        continue
                    if e.get("type") != "assistant":
                        continue
                    content = (e.get("message") or {}).get("content")
                    if not isinstance(content, list):
                        continue
                    for c in content:
                        if c.get("type") != "tool_use" or c.get("name") != "Bash":
                            continue
                        cmd = (c.get("input") or {}).get("command", "")
                        if "memcue" not in cmd or "search" not in cmd:
                            continue
                        for (flags, words), cd, seg in calls_in(cmd):
                            stats["calls"] += 1
                            if not literal(words, seg) or not words:
                                stats["not_literal"] += 1
                                continue
                            session_cwd = e.get("cwd") or ""
                            if temporary(session_cwd):
                                stats["temp_session"] += 1
                                continue
                            if "min-score" in flags or flags.get("all-words"):
                                stats["explicit_floor"] += 1
                                continue
                            text = " ".join(words)
                            if words_of(text) <= 8:
                                stats["short"] += 1
                                continue
                            stats["long_default"] += 1
                            d = flags.get("cwd") or cd or session_cwd
                            if d and not os.path.isabs(d):
                                d = os.path.normpath(os.path.join(session_cwd, os.path.expanduser(d)))
                            d = os.path.expanduser(d)
                            if not os.path.isdir(d) or temporary(d):
                                tier = None
                                t = parse_time(e["timestamp"])
                                for r in log.get(text, []):
                                    if abs(parse_time(r["time"]) - t) <= timedelta(minutes=2):
                                        tier = r.get("tier")
                                        break
                                cand = os.path.join(home, "src", tier) if tier and "/" in tier else None
                                if cand and os.path.isdir(cand):
                                    d = cand
                                    stats["resolved_by_log"] += 1
                                else:
                                    stats["no_dir"] += 1
                                    continue
                            key = (text, flags.get("in", ""), flags.get("kind", ""), flags.get("since", ""),
                                   bool(flags.get("expired")), d)
                            if key in seen:
                                continue
                            seen.add(key)
                            rows.append({"text": text, "in": flags.get("in", ""), "kind": flags.get("kind", ""),
                                         "since": flags.get("since", ""), "expired": bool(flags.get("expired")),
                                         "cwd": d, "time": e.get("timestamp"), "session": e.get("sessionId")})
    rows.sort(key=lambda r: (r["time"] or "", r["text"]))
    for i, r in enumerate(rows):
        r["qid"] = "Q%04d" % (i + 1)
    stats["kept"] = len(rows)
    with open(out_path, "w") as f:
        for r in rows:
            f.write(json.dumps(r, ensure_ascii=False) + "\n")
    json.dump(stats, sys.stdout, indent=1)
    print()


if __name__ == "__main__":
    main()
