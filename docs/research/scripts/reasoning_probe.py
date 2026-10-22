"""One split call as memcue sends it, on the first 20 approved facts of ~/.claude/memory/facts.db (read-only); prints
the usage and how much of the answer is reasoning. THINKING=disabled turns reasoning off.

  THINKING=enabled python3 -I reasoning_probe.py REPO_DIR
"""
import json, os, re, sqlite3, sys, time, urllib.request
repo = sys.argv[1]
prompt = open(f"{repo}/internal/extract/split_prompt.md", encoding="utf-8").read()
shape = re.search(r'const splitShape = "(.*?)" \+\n\t`(.*?)` \+ "\\n"', open(f"{repo}/internal/extract/split.go").read(), re.S)
system = prompt + shape.group(1).replace("\\n", "\n") + shape.group(2) + "\n"
db = sqlite3.connect(f"file:{os.path.expanduser('~/.claude/memory/facts.db')}?mode=ro", uri=True)
rows = db.execute("""select id, content from facts f where version = (select max(version) from facts where id = f.id)
                     and status = 'active' and review = '' order by id limit 20""").fetchall()
body = {"model": os.environ["MEMCUE_MODEL"], "messages": [{"role": "system", "content": system},
        {"role": "user", "content": json.dumps({"facts": [{"id": i, "text": t} for i, t in rows]}, ensure_ascii=False)}],
        "response_format": {"type": "json_object"}, "thinking": {"type": os.environ.get("THINKING", "enabled")}}
req = urllib.request.Request(os.environ["MEMCUE_API_URL"].rstrip("/") + "/chat/completions", data=json.dumps(body).encode(),
                             headers={"Authorization": "Bearer " + os.environ["MEMCUE_API_KEY"], "Content-Type": "application/json"})
t = time.time()
with urllib.request.urlopen(req, timeout=300) as r:
    ans = json.loads(r.read())
msg = ans["choices"][0]["message"]
print("model", ans.get("model"), "seconds %.1f" % (time.time() - t))
print("usage", json.dumps(ans["usage"]))
print("content chars", len(msg.get("content") or ""), "reasoning_content chars", len(msg.get("reasoning_content") or ""))
print("splits", json.dumps([[s["id"], len(s["parts"])] for s in json.loads(msg["content"]).get("splits", [])]))
