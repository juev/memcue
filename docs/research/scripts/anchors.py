"""Ask the main model (MEMCUE_API_URL, MEMCUE_MODEL, MEMCUE_API_KEY; reasoning off) for the anchors of every active fact,
25 facts a call, and write {id: {paths, commands, errors, hosts}} to OUT.json; a rerun fills what is missing (#318).

  python3 -I anchors.py FACTS.db anchors_prompt.md OUT.json
"""
import json, os, sqlite3, sys, time, urllib.request
from concurrent.futures import ThreadPoolExecutor

KINDS = ("paths", "commands", "errors", "hosts")
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
    err = None
    for attempt in range(3):
        try:
            with urllib.request.urlopen(req, timeout=180) as r:
                ans = json.loads(r.read())
            got = {f["id"]: {k: [a.strip() for a in f.get(k, []) if isinstance(a, str) and a.strip()] for k in KINDS}
                   for f in json.loads(ans["choices"][0]["message"]["content"])["facts"]}
            return {i: got[i] for i, _ in batch if i in got}, ans["usage"]
        except Exception as e:  # retried, then reported
            err = e
            time.sleep(2)
    print("batch failed:", err, file=sys.stderr)
    return {}, {}


t0 = time.time()
with ThreadPoolExecutor(8) as ex:
    for got, use in ex.map(call, batches):
        done.update(got)
        for k in usage:
            usage[k] += use.get(k, 0) or 0
json.dump(done, open(out_path, "w"), ensure_ascii=False, indent=0)
anchored = sum(1 for a in done.values() if any(a[k] for k in KINDS))
print(f"facts {len(rows)}, answered {len(done)}, with an anchor {anchored}, {time.time() - t0:.0f} s, usage {usage}")
