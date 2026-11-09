"""Sample real user prompts from Claude Code transcripts: one line per prompt, at most 3 per session."""
import json, random, sys
from pathlib import Path
root, out, n = Path(sys.argv[1]), Path(sys.argv[2]), int(sys.argv[3])
by_session = {}
for f in root.glob("*/*.jsonl"):
    for line in f.open(errors="replace"):
        try:
            d = json.loads(line)
        except ValueError:
            continue
        if d.get("type") != "user" or d.get("isSidechain") or d.get("isMeta"):
            continue
        c = d.get("message", {}).get("content")
        if not isinstance(c, str):
            continue
        t = c.strip()
        if not (25 <= len(t) <= 1500) or t.startswith(("<", "[", "Caveat:", "This session is being continued")):
            continue
        if "<command-" in t or "<system-reminder>" in t or "hook" in t[:40].lower():
            continue
        by_session.setdefault(f.stem, []).append({"prompt": t, "cwd": d.get("cwd", ""), "ts": d.get("timestamp", "")})
rng = random.Random(682)
pool, seen = [], set()
for sid in sorted(by_session):
    items = by_session[sid]
    rng.shuffle(items)
    for it in items[:3]:
        if it["prompt"] not in seen:
            seen.add(it["prompt"]); pool.append(it | {"session": sid})
rng.shuffle(pool)
sample = pool[:n]
with out.open("w") as w:
    for i, it in enumerate(sample):
        w.write(json.dumps({"qid": f"q{i:03d}"} | it, ensure_ascii=False) + "\n")
print(f"sessions {len(by_session)}, candidate prompts {len(pool)}, sampled {len(sample)}")
