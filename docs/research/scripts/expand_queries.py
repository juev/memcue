"""Ask the main model (MEMCUE_API_URL, MEMCUE_MODEL, MEMCUE_API_KEY; reasoning off) for search queries of every active
fact, 25 facts a call, and write {id: [queries]} to OUT.json; a rerun fills what is missing.

  python3 -I expand_queries.py FACTS.db expand_queries_prompt.md OUT.json
"""
import json, os, sqlite3, sys, time, urllib.request
from concurrent.futures import ThreadPoolExecutor
db_path, prompt_path, out_path = sys.argv[1:4]
system = open(prompt_path, encoding="utf-8").read()
db = sqlite3.connect(f"file:{db_path}?mode=ro", uri=True)
rows = db.execute("""select id, content from facts f where version = (select max(version) from facts where id = f.id)
                     and status = 'active' and review = '' order by id""").fetchall()
done = json.load(open(out_path)) if os.path.exists(out_path) else {}
todo = [r for r in rows if r[0] not in done]
batches = [todo[i:i + 25] for i in range(0, len(todo), 25)]
usage = {"prompt_tokens": 0, "completion_tokens": 0, "prompt_cache_hit_tokens": 0}

def call(batch):
    body = {"model": os.environ["MEMCUE_MODEL"], "thinking": {"type": "disabled"},
            "response_format": {"type": "json_object"},
            "messages": [{"role": "system", "content": system},
                         {"role": "user", "content": json.dumps({"facts": [{"id": i, "text": t} for i, t in batch]},
                                                                ensure_ascii=False)}]}
    req = urllib.request.Request(os.environ["MEMCUE_API_URL"].rstrip("/") + "/chat/completions",
                                 data=json.dumps(body).encode(),
                                 headers={"Authorization": "Bearer " + os.environ["MEMCUE_API_KEY"],
                                          "Content-Type": "application/json"})
    for attempt in range(3):
        try:
            with urllib.request.urlopen(req, timeout=180) as r:
                ans = json.loads(r.read())
            got = {f["id"]: [q.strip() for q in f.get("queries", []) if q.strip()]
                   for f in json.loads(ans["choices"][0]["message"]["content"])["facts"]}
            return got, ans["usage"]
        except Exception as e:  # retried, then reported
            err = e
            time.sleep(2)
    print("batch failed:", err, file=sys.stderr)
    return {}, {}

with ThreadPoolExecutor(6) as pool:
    for got, u in pool.map(call, batches):
        done.update({k: v for k, v in got.items() if k in dict(rows)})
        for k in usage:
            usage[k] += int(u.get(k) or 0)
json.dump(done, open(out_path, "w"), ensure_ascii=False, indent=0)
print(f"facts {len(rows)}, expanded {len(done)}, usage {usage}")
