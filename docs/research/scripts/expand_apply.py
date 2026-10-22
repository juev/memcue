"""Write the queries into the keywords of a COPY of the base through `memcue correct`, keeping the text and old keywords.
Run it with MEMCUE_FACTS pointing at the copy and without MEMCUE_EMBED_*.

  python3 -I expand_apply.py MEMCUE_BINARY COPY.db queries.json
"""
import json, sqlite3, subprocess, sys
binary, db_path, queries_path = sys.argv[1:4]
queries = json.load(open(queries_path))
db = sqlite3.connect(f"file:{db_path}?mode=ro", uri=True)
rows = db.execute("""select id, content, keywords from facts f where version = (select max(version) from facts where id = f.id)
                     and status = 'active' and review = ''""").fetchall()
db.close()
failed = 0
for fid, content, keywords in rows:
    old = [k.strip() for k in keywords.split(",") if k.strip()]
    new = [q.replace(",", " ").strip() for q in queries.get(fid, [])]
    merged = list(dict.fromkeys(old + [q for q in new if q]))
    r = subprocess.run([binary, "correct", "-force", "-keywords", ",".join(merged), fid, content], capture_output=True, text=True)
    if r.returncode != 0:
        failed += 1
        if failed <= 3:
            print(fid, r.stderr.strip()[:200], file=sys.stderr)
print(f"facts {len(rows)}, failed {failed}")
