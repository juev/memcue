#!/usr/bin/env python3
"""Build a gold set from several readers of the same fact base, label it blind, and score the readers on it.

The four stages are separate on purpose: the pool is what the readers propose, the labels are what a judge decides
about those pairs without knowing who proposed them, and the report counts both. Re-running one stage never
invalidates another as long as the pool file stays the same.

  pool    three readers -> one candidate pool per prompt (lexical, vector, whole-base judge)
  label   one request per prompt to an OpenAI-compatible judge that never sees the reader
  rerank  Jev over the lexical candidates alone, for the ranker comparison
  report  useful prompts and the mixture of facts, per reader, on the same pool

Keys come from the environment; the fact base and the prompts come from outside the repository.

  python3 -I gold_set.py pool   --facts FACTS.db --prompts eval.json --jev jev.jsonl --out pool.json \
                                --binary /path/to/memcue --vector-tag http://localhost:11434/v1/bge-m3#keywords
  python3 -I gold_set.py label  --pool pool.json --facts FACTS.db --out labels.jsonl
  python3 -I gold_set.py rerank --pool pool.json --facts FACTS.db --out rerank.jsonl
  python3 -I gold_set.py report --pool pool.json --labels labels.jsonl --rerank rerank.jsonl
"""

import argparse
import json
import math
import os
import random
import sqlite3
import struct
import subprocess
import sys
import time
import urllib.error
import urllib.request

LEXICAL, VECTOR, JUDGE = "lexical", "vector", "jev"

JUDGE_PROMPT = """You label retrieved facts for a memory system. For each numbered fact decide:
USEFUL - it helps answer the prompt: it holds the specific knowledge the prompt asks about (a decision, a rule, a
pitfall, an exact path/host/port/version, a preference), such that the answer would change or improve with it.
RELATED - same subject or area, but the prompt does not need it to be answered.
NOISE - unrelated, or a trap: it shares words with the prompt but would mislead the answer.
Judge only usefulness for this prompt. Being on the same topic is not USEFUL.
Answer with JSON only: {"1": "USEFUL", "2": "RELATED", "3": "NOISE"} for every number."""


def connect(path):
    return sqlite3.connect("file:%s?mode=ro" % path, uri=True)


def active_facts(path):
    db = connect(path)
    rows = db.execute("select id, content, keywords from facts f where status='active' and review=''"
                      " and version=(select max(version) from facts where id=f.id)").fetchall()
    db.close()
    return rows


def vectors(path, tag):
    db = connect(path)
    rows = db.execute("select id, vec from fact_vectors where tag=?", (tag,)).fetchall()
    db.close()
    out = {}
    for fid, blob in rows:
        out[fid] = unit(list(struct.unpack("<%df" % (len(blob) // 4), blob)))
    return out


def unit(values):
    norm = math.sqrt(sum(v * v for v in values)) or 1.0
    return [v / norm for v in values]


def embed(url, model, text):
    body = json.dumps({"model": model, "input": [text]}).encode()
    req = urllib.request.Request(url, data=body, headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=120) as resp:
        return unit(json.loads(resp.read())["data"][0]["embedding"])


def lexical(binary, facts, prompt, n):
    env = {k: v for k, v in os.environ.items() if not k.startswith("MEMCUE_EMBED") and not k.startswith("MEMCUE_RERANK")}
    env["MEMCUE_FACTS"] = facts
    done = subprocess.run([binary, "search", "-n", str(n), "-json", prompt], env=env, capture_output=True, text=True)
    if done.returncode != 0:
        return []
    try:
        return [row["id"] for row in json.loads(done.stdout)]
    except (ValueError, KeyError, TypeError):
        return []


def stage_pool(args):
    prompts = json.load(open(args.prompts, encoding="utf-8"))["prompts"]
    jev = {row["id"]: row for row in (json.loads(line) for line in open(args.jev, encoding="utf-8"))}
    vecs = vectors(args.facts, args.vector_tag)
    made = []
    for n, prompt in enumerate(prompts, 1):
        query = embed(args.embed_url, args.embed_model, prompt["text"])
        by_vector = sorted(((sum(a * b for a, b in zip(query, v)), fid) for fid, v in vecs.items()), reverse=True)
        seen, cands = set(), []
        for rank, fid in enumerate(lexical(args.binary, args.facts, prompt["text"], args.n), 1):
            if fid not in seen:
                seen.add(fid)
                cands.append({"id": fid, LEXICAL: rank})
        for rank, (sim, fid) in enumerate(by_vector[: args.n], 1):
            if fid not in seen:
                seen.add(fid)
                cands.append({"id": fid, VECTOR: rank, "sim": round(sim, 3)})
        scored = sorted(jev.get(prompt["id"], {}).get("scores", {}).items(), key=lambda kv: -kv[1])
        for fid, score in [(fid, s) for fid, s in scored[: args.n] if s >= args.jev_min]:
            if fid in seen:
                continue
            seen.add(fid)
            cands.append({"id": fid, JUDGE: round(score, 3)})
        made.append({"id": prompt["id"], "text": prompt["text"], "needs_memory": prompt.get("needs_memory"),
                     "candidates": cands})
        if n % 20 == 0:
            print("%d/%d prompts" % (n, len(prompts)), file=sys.stderr)
    json.dump(made, open(args.out, "w", encoding="utf-8"), ensure_ascii=False)
    sizes = [len(m["candidates"]) for m in made]
    print("pool: %d prompts, %d pairs, %.1f per prompt" % (len(made), sum(sizes), sum(sizes) / len(sizes)),
          file=sys.stderr)


def key_of(name):
    value = os.environ.get(name, "").strip()
    if not value:
        sys.exit("environment variable %s is empty" % name)
    return value


def stage_label(args):
    pool = json.load(open(args.pool, encoding="utf-8"))
    text = {fid: content for fid, content, _ in active_facts(args.facts)}
    key = key_of(args.key_env)
    done = {json.loads(line)["id"] for line in open(args.out, encoding="utf-8")} if os.path.exists(args.out) else set()
    tokens = [0, 0]
    with open(args.out, "a", encoding="utf-8") as out:
        for prompt in pool:
            if prompt["id"] in done:
                continue
            order = list(range(len(prompt["candidates"])))
            random.Random(hash(prompt["id"]) & 0xFFFF).shuffle(order)
            facts = [(prompt["candidates"][i]["id"], text[prompt["candidates"][i]["id"]]) for i in order]
            numbered = "\n".join("[%d] %s" % (i, t) for i, (_, t) in enumerate(facts, 1))
            body = {"model": args.model, "temperature": 0,
                    "messages": [{"role": "system", "content": JUDGE_PROMPT},
                                 {"role": "user", "content": "Prompt:\n%s\n\nFacts:\n%s" % (prompt["text"], numbered)}]}
            req = urllib.request.Request(args.url, data=json.dumps(body).encode(),
                                         headers={"Authorization": "Bearer " + key, "Content-Type": "application/json"})
            try:
                with urllib.request.urlopen(req, timeout=180) as resp:
                    answer = json.loads(resp.read())
            except (urllib.error.HTTPError, ValueError) as err:
                print("%s: judge failed: %s" % (prompt["id"], err), file=sys.stderr)
                continue
            raw = answer["choices"][0]["message"]["content"].strip()
            if raw.startswith("```"):
                raw = raw.strip("`").split("\n", 1)[1]
            verdict = json.loads(raw)
            tokens[0] += answer["usage"]["prompt_tokens"]
            tokens[1] += answer["usage"]["completion_tokens"]
            labels = {fid: verdict.get(str(i), "UNLABELED") for i, (fid, _) in enumerate(facts, 1)}
            out.write(json.dumps({"id": prompt["id"], "labels": labels}, ensure_ascii=False) + "\n")
            out.flush()
    print("labelled, %d input / %d output tokens" % tuple(tokens), file=sys.stderr)


def decisions(url, model, key, prompt, facts):
    """Ask one noul per fact. facts is a list of (id, text) pairs, in order."""
    state = ["Prompt: " + prompt, "Candidate facts:"] + ["[%d] %s" % (i, t) for i, t in enumerate(facts, 1)]
    questions = {"f%d" % i: {"type": "noul", "instructions": "Would fact [%d] help answer the prompt?" % i}
                 for i in range(1, len(facts) + 1)}
    body = json.dumps({"model": model, "state": state, "questions": questions}).encode()
    req = urllib.request.Request(url, data=body,
                                 headers={"Authorization": "Bearer " + key, "Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=300) as resp:
        answer = json.loads(resp.read())
    return {fid: answer["answers"]["f%d" % i]["noul"] for i, (fid, _) in enumerate(facts, 1)}, answer["usage"]


def stage_rerank(args):
    pool = json.load(open(args.pool, encoding="utf-8"))
    text = {fid: content for fid, content, _ in active_facts(args.facts)}
    key = key_of(args.key_env)
    spent = 0.0
    tokens = 0
    with open(args.out, "w", encoding="utf-8") as out:
        for prompt in pool:
            cands = sorted([c for c in prompt["candidates"] if LEXICAL in c], key=lambda c: c[LEXICAL])[: args.n]
            if len(cands) < 2:
                continue
            facts = [(c["id"], text[c["id"]]) for c in cands]
            started = time.perf_counter()
            try:
                scores, usage = decisions(args.url, args.model, key, prompt["text"], facts)
            except (urllib.error.HTTPError, ValueError, KeyError) as err:
                print("%s: rerank failed: %s" % (prompt["id"], err), file=sys.stderr)
                continue
            spent += usage.get("cost", 0)
            tokens += usage["input_tokens"]
            out.write(json.dumps({"id": prompt["id"], "scores": scores, "seconds": time.perf_counter() - started},
                                 ensure_ascii=False) + "\n")
            out.flush()
    print("reranked, %d input tokens, $%.4f" % (tokens, spent), file=sys.stderr)


def stage_report(args):
    pool = {m["id"]: m for m in json.load(open(args.pool, encoding="utf-8"))}
    labels = {r["id"]: r["labels"] for r in (json.loads(line) for line in open(args.labels, encoding="utf-8"))}
    rerank = {r["id"]: r["scores"] for r in (json.loads(line) for line in open(args.rerank, encoding="utf-8"))} \
        if args.rerank else {}

    def ranked(prompt, reader, k):
        items = [c for c in pool[prompt]["candidates"] if reader in c]
        items.sort(key=lambda c: c[reader])
        return [c["id"] for c in items[:k]]

    def reranked(prompt, k):
        scores = rerank[prompt]
        ids = [c["id"] for c in pool[prompt]["candidates"] if LEXICAL in c and c["id"] in scores]
        return sorted(ids, key=lambda fid: -scores[fid])[:k]

    labelled = [pid for pid in pool if pid in labels]
    for k in (1, 2):
        print("\n=== top-%d (%d prompts) ===" % (k, len(labelled)))
        for reader in (LEXICAL, VECTOR, JUDGE):
            useful = sum(1 for pid in labelled if any(labels[pid].get(f) == "USEFUL" for f in ranked(pid, reader, k)))
            dist = {l: sum(1 for pid in labelled for f in ranked(pid, reader, k) if labels[pid].get(f) == l)
                    for l in ("USEFUL", "RELATED", "NOISE")}
            print("%-8s useful prompts %3d   USEFUL %3d / RELATED %3d / NOISE %3d"
                  % (reader, useful, dist["USEFUL"], dist["RELATED"], dist["NOISE"]))
        if rerank:
            ids = [pid for pid in labelled if pid in rerank]
            useful = sum(1 for pid in ids if any(labels[pid].get(f) == "USEFUL" for f in reranked(pid, k)))
            dist = {l: sum(1 for pid in ids for f in reranked(pid, k) if labels[pid].get(f) == l)
                    for l in ("USEFUL", "RELATED", "NOISE")}
            print("%-8s useful prompts %3d   USEFUL %3d / RELATED %3d / NOISE %3d"
                  % ("rerank", useful, dist["USEFUL"], dist["RELATED"], dist["NOISE"]))
    ceiling = sum(1 for pid in labelled if any(labels[pid].get(c["id"]) == "USEFUL" for c in pool[pid]["candidates"]))
    print("\npool ceiling: %d of %d prompts have a USEFUL fact among the candidates" % (ceiling, len(labelled)))


def main():
    ap = argparse.ArgumentParser()
    sub = ap.add_subparsers(dest="stage", required=True)

    pool = sub.add_parser("pool")
    pool.add_argument("--facts", required=True)
    pool.add_argument("--prompts", required=True)
    pool.add_argument("--jev", required=True, help="whole-base judgement, one JSON line per prompt")
    pool.add_argument("--out", required=True)
    pool.add_argument("--binary", default="memcue", help="memcue binary for the lexical candidates")
    pool.add_argument("--vector-tag", required=True, help="fact_vectors tag, e.g. http://host:11434/v1/bge-m3#keywords")
    pool.add_argument("--embed-url", default="http://127.0.0.1:11434/v1/embeddings")
    pool.add_argument("--embed-model", default="bge-m3")
    pool.add_argument("--n", type=int, default=10)
    pool.add_argument("--jev-min", type=float, default=0.3)
    pool.set_defaults(func=stage_pool)

    label = sub.add_parser("label")
    label.add_argument("--pool", required=True)
    label.add_argument("--facts", required=True)
    label.add_argument("--out", required=True)
    label.add_argument("--url", default="https://api.deepseek.com/chat/completions")
    label.add_argument("--model", default="deepseek-flash")
    label.add_argument("--key-env", default="DEEPSEEK_API_KEY")
    label.set_defaults(func=stage_label)

    rerank = sub.add_parser("rerank")
    rerank.add_argument("--pool", required=True)
    rerank.add_argument("--facts", required=True)
    rerank.add_argument("--out", required=True)
    rerank.add_argument("--url", default="https://openrouter.ai/api/alpha/decisions")
    rerank.add_argument("--model", default="typesafe/jev-1.14")
    rerank.add_argument("--key-env", default="OPENROUTER_API_KEY")
    rerank.add_argument("--n", type=int, default=10)
    rerank.set_defaults(func=stage_rerank)

    report = sub.add_parser("report")
    report.add_argument("--pool", required=True)
    report.add_argument("--labels", required=True)
    report.add_argument("--rerank", help="the same pool ranked by the judge, for the ranker line")
    report.set_defaults(func=stage_report)

    args = ap.parse_args()
    args.func(args)


if __name__ == "__main__":
    main()
