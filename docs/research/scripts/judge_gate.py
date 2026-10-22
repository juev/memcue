"""Skip the judge when the words found a strong hit: what it costs in useful prompts and saves in judge calls.

  python3 -I judge_gate.py label  candidates.jsonl labels.json JEV.jsonl...
  python3 -I judge_gate.py report candidates.jsonl labels.json JEV.jsonl...

candidates.jsonl is the output of judgecand (with the words' own answer in "lexical"), JEV.jsonl the Jev runs of
judge_ab.py, labels.json the Opus labels of judge_ab_report.py. A gate is a rule on the words' answer; where it holds,
the prompt gets the words' answer and the judge is not called, elsewhere it gets Jev's at its default threshold.
`label` hands the words' answer to `judge_ab_report.py label` as one more reader, so Opus labels the pairs it lacks
blind and with the same instructions.
"""
import json
import subprocess
import sys
import tempfile
from pathlib import Path

MIN, MAX_FACTS = 0.5, 2


def gates():
    yield "never (Jev on every prompt)", lambda lex: False
    for cov in (0.5, 0.6, 0.7, 0.8):
        yield f"top coverage >= {cov}", lambda lex, c=cov: bool(lex) and lex[0]["coverage"] >= c
        yield f"top coverage >= {cov}, two facts", lambda lex, c=cov: len(lex) >= 2 and lex[0]["coverage"] >= c
    yield "always (words only)", lambda lex: True


def jev_served(row):
    order = sorted(range(len(row["facts"])), key=lambda i: (row["provisional"][i], -row["scores"][i]))
    return [row["facts"][i] for i in order if row["scores"][i] >= MIN][:MAX_FACTS]


mode, cand_path, labels_path, runs = sys.argv[1], sys.argv[2], Path(sys.argv[3]), sys.argv[4:]
prompts = {r["id"]: r for r in map(json.loads, open(cand_path))}
labels = json.loads(labels_path.read_text())
jev = {Path(p).stem: {r["id"]: r for r in map(json.loads, open(p)) if "error" not in r} for p in runs}

if mode == "label":
    with tempfile.TemporaryDirectory() as d:
        cands, words = Path(d, "candidates.jsonl"), Path(d, "words.jsonl")
        with cands.open("w") as c, words.open("w") as w:
            for pid, r in prompts.items():
                lex = r["lexical"] or []
                extra = [{"id": x["id"], "content": x["content"], "provisional": False} for x in lex]
                c.write(json.dumps(dict(r, candidates=(r["candidates"] or []) + extra)) + "\n")
                w.write(json.dumps({"id": pid, "facts": [x["id"] for x in lex], "provisional": [False] * len(lex),
                                    "scores": [1.0 - i / 10 for i in range(len(lex))]}) + "\n")
        sys.exit(subprocess.run([sys.executable, "-I", str(Path(__file__).with_name("judge_ab_report.py")), "label",
                                 str(cands), str(labels_path), str(words)]).returncode)

ids = set.intersection(*(set(r) for r in jev.values()))
print(f"prompts {len(ids)} (those every Jev run answered)")
print(f"{'gate':<34}{'run':<8}{'skip':>6}{'top-1':>7}{'any':>6}{'USEFUL':>8}{'RELATED':>9}{'NOISE':>7}{'unlab':>7}")
for name, gate in gates():
    for run_name, run in jev.items():
        skip = top1 = anyu = 0
        dist = {"USEFUL": 0, "RELATED": 0, "NOISE": 0, "UNLABELLED": 0}
        for pid in ids:
            lex = prompts[pid]["lexical"] or []
            if gate(lex):
                skip += 1
                served = [x["id"] for x in lex][:MAX_FACTS]
            else:
                served = jev_served(run[pid])
            ls = [labels.get(f"{pid}|{f}", "UNLABELLED") for f in served]
            for l in ls:
                dist[l] += 1
            top1 += bool(ls) and ls[0] == "USEFUL"
            anyu += "USEFUL" in ls
        print(f"{name:<34}{run_name:<8}{skip:>6}{top1:>7}{anyu:>6}{dist['USEFUL']:>8}{dist['RELATED']:>9}"
              f"{dist['NOISE']:>7}{dist['UNLABELLED']:>7}")
