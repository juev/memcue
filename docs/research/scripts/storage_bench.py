#!/usr/bin/env python3
"""Compare SQLite FTS5 and DuckDB FTS on the shape of work memcue does.

Builds a synthetic fact base from the vocabulary of a live base (so term frequencies look like the real thing), then
measures, per engine and per size: bulk load, index build, one insert plus commit, and the recall query itself —
MATCH over the terms of a real prompt, joined to the latest version of each fact, the first 80 in BM25 order.

DuckDB is optional: without it the script measures SQLite alone. DuckDB 2.0 is what the numbers in
docs/research/storage-2026-10.md come from; `incremental=1` needs the document id column to be NOT NULL.

    python3 storage_bench.py --base ~/.claude/memory/facts.db --prompts ~/.local/state/memcue/recall.log
"""

import argparse
import json
import os
import random
import re
import sqlite3
import statistics
import sys
import tempfile
import time

SCHEMA = """
CREATE TABLE facts (id TEXT NOT NULL, version INTEGER NOT NULL, content TEXT NOT NULL,
  keywords TEXT NOT NULL DEFAULT '', origin TEXT NOT NULL DEFAULT '', created TEXT NOT NULL,
  status TEXT NOT NULL, PRIMARY KEY (id, version)) WITHOUT ROWID;
CREATE TABLE fact_index (rid INTEGER PRIMARY KEY, id TEXT NOT NULL UNIQUE, stems TEXT NOT NULL);
CREATE VIRTUAL TABLE fact_fts USING fts5(stems, content='fact_index', content_rowid='rid',
  tokenize='unicode61 remove_diacritics 0');
CREATE INDEX facts_created ON facts(created);
"""

# The query Recall makes: terms OR-ed, bm25 order, joined to the latest version of each fact.
SQLITE_Q = """SELECT i.stems, f.id, f.content FROM fact_fts JOIN fact_index i ON i.rid = fact_fts.rowid
JOIN facts f ON f.id = i.id AND f.version = (SELECT max(version) FROM facts WHERE id = i.id)
WHERE fact_fts MATCH ? ORDER BY bm25(fact_fts) LIMIT ?"""

# match_bm25 is a scalar function, so the filter has to sit outside the projection that computes it.
DUCK_Q = """SELECT * FROM (SELECT id, fts_main_facts.match_bm25(id, ?) AS s FROM facts) t
WHERE s IS NOT NULL ORDER BY s DESC LIMIT ?"""


def terms(prompt, k=10):
    """The distinct words of a prompt a query would carry, in order."""
    out, seen = [], set()
    for w in re.findall(r"[\w\-]+", prompt.lower()):
        if len(w) > 2 and w not in seen:
            seen.add(w)
            out.append(w)
    return out[:k]


def corpus(base, size, seed=7):
    """size facts of 22-40 words drawn from the vocabulary of a real base, so that df looks like a real base."""
    con = sqlite3.connect("file:%s?mode=ro" % base, uri=True)
    text = "\n".join(" ".join(x for x in row if x) for row in con.execute("SELECT content, keywords, queries FROM facts"))
    con.close()
    freq = {}
    for w in re.findall(r"[\w\-]+", text.lower()):
        if len(w) > 2:
            freq[w] = freq.get(w, 0) + 1
    vocab, weights = list(freq), list(freq.values())
    rnd = random.Random(seed)
    return [" ".join(rnd.choices(vocab, weights=weights, k=rnd.randint(22, 40))) for _ in range(size)]


def bench_sqlite(docs, prompts, journal, tmp):
    path = os.path.join(tmp, "facts-%s.db" % journal)
    if os.path.exists(path):
        os.remove(path)
    con = sqlite3.connect(path, isolation_level=None)
    con.execute("PRAGMA journal_mode=%s" % journal)
    con.execute("PRAGMA synchronous=FULL")
    con.executescript(SCHEMA)
    t = time.perf_counter()
    con.execute("BEGIN")
    for i, d in enumerate(docs):
        fid = "01J%010d" % i
        con.execute("INSERT INTO facts VALUES (?,?,?,?,?,?,?)",
                    (fid, 1, d, "kw", "repo x 2026-10-23", "2026-10-23T00:00:00Z", "active"))
        rid = con.execute("INSERT INTO fact_index(id, stems) VALUES (?,?)", (fid, d)).lastrowid
        con.execute("INSERT INTO fact_fts(rowid, stems) VALUES (?,?)", (rid, d))
    con.execute("COMMIT")
    bulk = time.perf_counter() - t
    adds = []
    for i in range(20):
        t = time.perf_counter()
        fid = "02J%010d" % i
        con.execute("BEGIN")
        con.execute("INSERT INTO facts VALUES (?,?,?,?,?,?,?)",
                    (fid, 1, docs[i], "kw", "repo x 2026-10-23", "2026-10-23T00:00:00Z", "active"))
        rid = con.execute("INSERT INTO fact_index(id, stems) VALUES (?,?)", (fid, docs[i])).lastrowid
        con.execute("INSERT INTO fact_fts(rowid, stems) VALUES (?,?)", (rid, docs[i]))
        con.execute("COMMIT")
        adds.append((time.perf_counter() - t) * 1000)
    qs = []
    for p in prompts:
        q = " OR ".join('"%s"' % w.replace('"', "") for w in terms(p))
        t = time.perf_counter()
        con.execute(SQLITE_Q, (q, 80)).fetchall()
        qs.append((time.perf_counter() - t) * 1000)
    con.execute("PRAGMA journal_mode=DELETE")
    con.close()
    return dict(bulk=bulk, adds=adds, queries=qs, size=os.path.getsize(path))


def bench_duckdb(docs, prompts, tmp):
    import duckdb

    path = os.path.join(tmp, "facts.duckdb")
    if os.path.exists(path):
        os.remove(path)
    con = duckdb.connect(path)
    con.execute("INSTALL fts; LOAD fts;")
    con.execute("CREATE TABLE facts(id VARCHAR NOT NULL, content VARCHAR)")
    t = time.perf_counter()
    con.execute("BEGIN")
    con.executemany("INSERT INTO facts VALUES (?,?)", [("%010d" % i, d) for i, d in enumerate(docs)])
    con.execute("COMMIT")
    bulk = time.perf_counter() - t
    t = time.perf_counter()
    con.execute("PRAGMA create_fts_index('facts','id','content', stemmer='russian', stopwords='none', overwrite=1, incremental=1)")
    build = time.perf_counter() - t
    qs = []
    for p in prompts:
        t = time.perf_counter()
        con.execute(DUCK_Q, (" ".join(terms(p)), 80)).fetchall()
        qs.append((time.perf_counter() - t) * 1000)
    adds = []
    for i in range(10):
        t = time.perf_counter()
        con.execute("INSERT INTO facts VALUES (?,?)", ("b%09d" % i, docs[i]))
        adds.append((time.perf_counter() - t) * 1000)
    con.execute("CHECKPOINT")
    con.close()
    return dict(bulk=bulk, build=build, adds=adds, queries=qs, size=os.path.getsize(path))


def line(name, engine, r, build=None):
    qs, adds = sorted(r["queries"]), sorted(r["adds"])
    print("%-6s %-7s n=%6d bulk=%5.2fs%s add+commit p50=%5.2fms query p50=%5.2fms p95=%6.2fms size=%5.1fMB" % (
        name, engine, r.get("n", 0), r["bulk"],
        "" if build is None else " index=%4.2fs" % build,
        statistics.median(adds), statistics.median(qs), qs[int(len(qs) * 0.95)], r["size"] / 1e6))


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--base", default=os.path.expanduser("~/.claude/memory/facts.db"))
    ap.add_argument("--prompts", default=os.path.expanduser("~/.local/state/memcue/recall.log"))
    ap.add_argument("--sizes", default="300,3000,20000")
    ap.add_argument("--no-duckdb", action="store_true")
    a = ap.parse_args()

    prompts = [json.loads(l)["prompt"] for l in open(a.prompts) if l.strip()]
    prompts = [p for p in prompts if len(p) > 15][:120]
    print("%d prompts, %d facts max" % (len(prompts), max(int(s) for s in a.sizes.split(","))))

    with tempfile.TemporaryDirectory() as tmp:
        for size in (int(s) for s in a.sizes.split(",")):
            docs = corpus(a.base, size)
            for journal in ("DELETE", "WAL"):
                r = bench_sqlite(docs, prompts, journal, tmp)
                r["n"] = size
                line(size, "sqlite/" + journal, r)
            if not a.no_duckdb:
                try:
                    r = bench_duckdb(docs, prompts, tmp)
                except ImportError:
                    print("duckdb is not installed: pip install duckdb to compare")
                    return 0
                r["n"] = size
                line(size, "duckdb", r, build=r["build"])
    return 0


if __name__ == "__main__":
    sys.exit(main())
