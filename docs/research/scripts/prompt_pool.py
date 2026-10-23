"""Collect real user prompts for training a local ranker (#320): every typed prompt of the Claude Code transcripts and of
recall.log, deduplicated, without the prompts of a gold set, as a judgecand eval.json.

  python3 -I prompt_pool.py GOLD_candidates.jsonl OUT_eval.json TRANSCRIPT_DIR... [--recall-log recall.log]

A prompt is a user message the person typed: not meta, not a tool result, not a command or hook wrapper (text starting
with "<"), not a compaction summary, at least 20 characters. Texts are cut to 4000 characters as the hook cuts them. A prompt whose first 200
characters, lowercased and with whitespace collapsed, match a gold prompt's is left out, so training never sees the
evaluation prompts.
"""
import json
import re
import sys
from pathlib import Path

MAX = 4000
MIN = 20


def key(text):
    return re.sub(r"\s+", " ", text.lower()).strip()[:200]


def typed(msg):
    if msg.get("type") != "user" or msg.get("isMeta") or msg.get("isSidechain"):
        return None
    content = (msg.get("message") or {}).get("content")
    if isinstance(content, list):
        if any(isinstance(c, dict) and c.get("type") == "tool_result" for c in content):
            return None
        content = "\n".join(c.get("text", "") for c in content if isinstance(c, dict) and c.get("type") == "text")
    if not isinstance(content, str):
        return None
    return clean(content)


def clean(text):
    text = text.strip()
    # Compaction summaries come in as user messages; they are not prompts.
    if len(text) < MIN or text.startswith("<") or text.startswith("This session is being continued"):
        return None
    return text[:MAX]


args = sys.argv[1:]
recall_log = None
if "--recall-log" in args:
    i = args.index("--recall-log")
    recall_log = args[i + 1]
    del args[i:i + 2]
gold_path, out_path, dirs = args[0], args[1], args[2:]
gold = {key(json.loads(line)["text"]) for line in open(gold_path)}

seen, prompts = set(), []


def add(text, source):
    k = key(text)
    if k in gold or k in seen:
        return
    seen.add(k)
    prompts.append({"id": f"n{len(prompts) + 1:05d}", "tier": "", "text": text, "source": source})


for d in dirs:
    for path in sorted(Path(d).rglob("*.jsonl")):
        if "subagents" in path.parts:
            continue
        for line in open(path, errors="replace"):
            try:
                text = typed(json.loads(line))
            except (json.JSONDecodeError, AttributeError):
                continue
            if text:
                add(text, "transcript")
if recall_log:
    for line in open(recall_log):
        text = clean(json.loads(line).get("prompt") or "")
        if text:
            add(text, "recall.log")
Path(out_path).write_text(json.dumps({"prompts": prompts}, ensure_ascii=False))
print(f"prompts {len(prompts)}, gold left out by text {len(gold)}")
