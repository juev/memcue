"""Check the facts of a scope replay (#345) against what the agent did, with no model: a fact counts as used when one of
its distinctive tokens shows up in what the agent wrote or ran after the prompt, up to the next typed prompt of the
session. A distinctive token is an identifier, path, host, number or command word of the fact: at least 4
characters, with a digit, a slash, a dot, an underscore, a dash or an inner capital, held by few facts of the base,
absent from the prompt itself, and not part of the agent's common vocabulary (written after many of the prompts, as
origin/main or a date are). The agent's own words and tool calls are read; tool results are not, since they
show what the system printed rather than what the agent knew.

  python3 -I scope_outcome.py scope.jsonl FACTS.db labels.json TRANSCRIPT_DIR... > outcome.json

A use is evidence, not proof: the agent may have found the token without the fact, and the archived sessions were
served facts by the recall of their time. No use is no evidence either way. To stderr: per variant, the prompts
where a served fact was used, paired against the baseline, and how the judge's labels line up with the evidence.
"""
import json
import math
import re
import sqlite3
import sys
from collections import Counter
from pathlib import Path

scope_path, db_path, labels_path, dirs = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4:]
MAX_DF = 10  # a token held by more facts than this says little about one of them
MAX_OUT = 0.02  # a token the agent writes after more than this share of the prompts is its own vocabulary

rows = [json.loads(line) for line in open(scope_path)]
rows = [r for r in rows if any(v != r["picks"]["base"] for v in r["picks"].values())]
labels = json.load(open(labels_path))
db = sqlite3.connect(f"file:{db_path}?mode=ro", uri=True)
content = dict(db.execute("SELECT id, content FROM facts f WHERE version = (SELECT max(version) FROM facts WHERE id = f.id)"))

reToken = re.compile(r"[A-Za-z0-9_./:@~-]+")


def shaped(t):
    return len(t) >= 4 and (re.search(r"[0-9/._-]", t) or re.search(r"[a-z][A-Z]", t))


def tokens(text):
    out = set()
    for t in reToken.findall(text):
        t = t.strip(".:,-")
        if shaped(t):
            out.add(t.lower())
    return out


df = Counter()
for c in content.values():
    df.update(tokens(c))


def key(text):
    return re.sub(r"\s+", " ", text.lower()).strip()[:200]


def typed(msg):
    if msg.get("type") != "user" or msg.get("isMeta") or msg.get("isSidechain"):
        return None
    c = (msg.get("message") or {}).get("content")
    if isinstance(c, list):
        if any(isinstance(x, dict) and x.get("type") == "tool_result" for x in c):
            return None
        c = "\n".join(x.get("text", "") for x in c if isinstance(x, dict) and x.get("type") == "text")
    if not isinstance(c, str):
        return None
    c = c.strip()
    if len(c) < 20 or c.startswith("<") or c.startswith("This session is being continued"):
        return None
    return c[:4000]


want = {key(r["text"]): r["id"] for r in rows}
after = {}  # prompt id -> what the agent wrote and ran until the next typed prompt
for d in dirs:
    for path in sorted(Path(d).rglob("*.jsonl")):
        if "subagents" in path.parts:
            continue
        cur = None
        for line in open(path, errors="replace"):
            try:
                m = json.loads(line)
            except json.JSONDecodeError:
                continue
            t = typed(m) if m.get("type") == "user" else None
            if t is not None:
                k = key(t)
                cur = want[k] if k in want and want[k] not in after else None
                if cur:
                    after[cur] = []
                continue
            if cur and m.get("type") == "assistant":
                for x in (m.get("message") or {}).get("content") or []:
                    if not isinstance(x, dict):
                        continue
                    if x.get("type") == "text":
                        after[cur].append(x.get("text", ""))
                    elif x.get("type") == "tool_use":
                        after[cur].append(json.dumps(x.get("input"), ensure_ascii=False))

seen_by = {r["id"]: tokens("\n".join(after.get(r["id"], []))) for r in rows}
out_df = Counter(t for ts in seen_by.values() for t in ts)
common = {t for t, n in out_df.items() if n > MAX_OUT * len(rows)}
used = {}  # (prompt, fact) -> the tokens found
for r in rows:
    seen = seen_by[r["id"]] - common
    in_prompt = tokens(r["text"])
    for f in {i for v in r["picks"].values() for i in v}:
        hit = sorted(t for t in tokens(content.get(f, "")) if df[t] <= MAX_DF and t not in in_prompt and t in seen)
        used[(r["id"], f)] = hit


def sign_p(a, b):
    n, k = a + b, min(a, b)
    return 1.0 if n == 0 else min(1.0, 2 * sum(math.comb(n, i) for i in range(k + 1)) / 2 ** n)


def has_use(r, v):
    return any(used[(r["id"], f)] for f in r["picks"][v])


log = sys.stderr
print(f"prompts {len(rows)}, found in transcripts {sum(1 for r in rows if r['id'] in after)}, "
      f"with agent output {sum(1 for r in rows if after.get(r['id']))}", file=log)
print(f"{'variant':<12} {'used':>5} {'+':>4} {'-':>4} {'p':>6}", file=log)
for v in sorted(rows[0]["picks"]):
    n = sum(1 for r in rows if has_use(r, v))
    gain = sum(1 for r in rows if has_use(r, v) and not has_use(r, "base"))
    loss = sum(1 for r in rows if has_use(r, "base") and not has_use(r, v))
    print(f"{v:<12} {n:>5} {gain:>4} {loss:>4} {sign_p(gain, loss):>6.3f}", file=log)
agree = Counter()
for (p, f), hit in used.items():
    agree[(labels.get(p, {}).get("labels", {}).get(f, ""), bool(hit))] += 1
print("judge label x used:", {f"{lab or '-'}/{'used' if u else 'no'}": n for (lab, u), n in sorted(agree.items())},
      file=log)
json.dump({f"{p} {f}": hit for (p, f), hit in used.items() if hit}, sys.stdout, ensure_ascii=False, indent=1)
