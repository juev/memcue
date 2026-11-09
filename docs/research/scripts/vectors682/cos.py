"""Cosine of each lexical top-20 fact to its prompt (E5), computed in PostgreSQL."""
import csv, json, subprocess, sys
from pathlib import Path
here = Path(sys.argv[1]); run = json.loads((here / "runs-e5.json").read_text())
with open(here / "pairs.csv", "w", newline="") as f:
    w = csv.writer(f)
    for r in run:
        for fid in r["lex20"]:
            w.writerow([r["qid"], fid])
sql = ("CREATE TEMP TABLE pairs (qid text, id text);\n\\copy pairs FROM STDIN WITH (FORMAT csv)\n" + (here / "pairs.csv").read_text() + "\\.\n"
       "CREATE TEMP TABLE qv AS SELECT p.qid, e.v FROM bench.prompts p, LATERAL bench.embed('http://memcue-682-tei-e5/embed', ARRAY['query: ' || p.prompt]) e;\n"
       "SELECT pairs.qid, pairs.id, 1 - (f.v <=> qv.v) FROM pairs JOIN qv USING (qid) JOIN bench.emb_e5 f ON f.id = pairs.id;\n")
out = subprocess.run(["docker", "exec", "-i", "memcue-682-pg", "psql", "-v", "ON_ERROR_STOP=1", "-U", "bench", "-d", "bench", "-At", "-F", "\t"],
                     input=sql, capture_output=True, text=True, check=True).stdout
cos = {}
for line in out.splitlines():
    if line.count("\t") == 2:
        q, fid, c = line.split("\t"); cos[q + ":" + fid] = float(c)
(here / "lexcos-e5.json").write_text(json.dumps(cos))
print("pairs", sum(len(r["lex20"]) for r in run), "with cosine", len(cos))
