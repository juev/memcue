"""#682 bench on loffe: lexical (memcue CLI), vector (PostgreSQL + TEI) and RRF hybrid top lists for each prompt."""
import csv, json, os, subprocess, sys, time
from pathlib import Path
here = Path(sys.argv[1]); model = sys.argv[2]
pw = (Path.home() / ".memcue-682-pg.pw").read_text().strip()
env = {"HOME": str(Path.home()), "PATH": "/usr/bin:/bin", "MEMCUE_STATE_DIR": str(here / "state"),
       "MEMCUE_POSTGRES_DSN": f"postgres://bench:{pw}@172.28.32.2:5432/bench?sslmode=disable"}
prompts = [json.loads(l) for l in (here / "prompts.jsonl").open()]
today = time.strftime("%Y-%m-%d")

def psql(sql_file):
    return subprocess.run(["docker", "exec", "-i", "memcue-682-pg", "psql", "-v", "ON_ERROR_STOP=1", "-U", "bench", "-d", "bench", "-At", "-F", "\t"],
                          stdin=open(sql_file), capture_output=True, text=True, check=True).stdout

# Load the prompts once, as CSV, so their text needs no SQL quoting.
with open(here / "prompts.csv", "w", newline="") as f:
    w = csv.writer(f)
    for p in prompts:
        w.writerow([p["qid"], p["prompt"]])
(here / "load.sql").write_text("TRUNCATE bench.prompts;\n\\copy bench.prompts FROM STDIN WITH (FORMAT csv)\n" + (here / "prompts.csv").read_text() + "\\.\n")
psql(here / "load.sql")
(here / "vq.sql").write_text(f"SELECT p.qid, r.rank, r.id, r.cos, r.embed_ms, r.search_ms FROM bench.prompts p, "
                             f"LATERAL bench.vsearch('{model}', p.prompt, 20, '{today}') r ORDER BY p.qid, r.rank;\n")
vec = {}
for line in psql(here / "vq.sql").splitlines():
    qid, rank, fid, cos, ems, sms = line.split("\t")
    vec.setdefault(qid, {"ids": [], "cos": [], "embed_ms": float(ems), "search_ms": float(sms)})
    vec[qid]["ids"].append(fid); vec[qid]["cos"].append(float(cos))

def lexical(prompt, extra):
    t = time.perf_counter()
    r = subprocess.run([str(here / "memcue"), "search", "-json", "-in", "all", *extra, "--", prompt],
                       env=env, capture_output=True, text=True, stdin=subprocess.DEVNULL, cwd=str(here), timeout=30)
    ms = (time.perf_counter() - t) * 1000
    if r.returncode != 0:
        return [], ms, r.stderr.strip()[:200]
    return [f["id"] for f in json.loads(r.stdout)["facts"]], ms, ""

out = []
for p in prompts:
    l5, l5ms, err = lexical(p["prompt"], ["-n", "5"])
    l20, _, _ = lexical(p["prompt"], ["-n", "20", "-min-score", "0"])
    v = vec.get(p["qid"], {"ids": [], "cos": [], "embed_ms": None, "search_ms": None})
    rrf = {}
    for lst in (l20, v["ids"]):
        for i, fid in enumerate(lst):
            rrf[fid] = rrf.get(fid, 0) + 1 / (60 + i + 1)
    h5 = [fid for fid, _ in sorted(rrf.items(), key=lambda x: (-x[1], x[0]))[:5]]
    out.append({"qid": p["qid"], "lex5": l5, "lex20": l20, "vec20": v["ids"], "cos20": v["cos"], "hyb5": h5,
                "lex_ms": l5ms, "embed_ms": v["embed_ms"], "vsearch_ms": v["search_ms"], "lex_err": err})
(here / f"runs-{model}.json").write_text(json.dumps(out, ensure_ascii=False))
print("prompts", len(out), "lex empty", sum(1 for o in out if not o["lex5"]), "lex errors", sum(1 for o in out if o["lex_err"]))
