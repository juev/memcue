#!/usr/bin/env python3
"""Blind grades of the e2e runs and the report.

    grade.py run     -> grades.json (one Opus call per prompt: the reference and every run's answer under a letter)
    grade.py report  -> per-mode scores, flags, turns, tools, cost, time; pairwise wins; rep-to-rep spread

E2E_ARMS picks the modes graded together, E2E_GRADES the prefix of the grade files; the pull grading of #361 ran with
E2E_ARMS=none,memcue,push,pull,both E2E_GRADES=grades7, the pullf grading with
E2E_ARMS=none,push,pull,both,pullf E2E_GRADES=grades8.
"""
import json, os, random, re, statistics as st, subprocess, sys
from concurrent.futures import ThreadPoolExecutor

E = os.path.dirname(os.path.abspath(__file__))
PASS = int(sys.argv[2]) if len(sys.argv) > 2 else 1  # grading pass: two passes measure the grader's own noise
ARMS = tuple(os.environ.get("E2E_ARMS", "none,files,memcue,judge,stubs,local").split(","))
GRADES = os.environ.get("E2E_GRADES", "grades")

PROMPT = """Ты оцениваешь ответы агента на реплику пользователя. Агент работал в режиме планирования: только читал
репозиторий, без сети и без записи. Дан эталон — начало реального ответа, который пользователь получил в настоящей
сессии (там у агента были все инструменты; эталон может быть неполон). Ответы прогонов помечены буквами, источник скрыт.

Для каждого ответа:
- score: 2 — по существу верно и полезно, совпадает с эталоном в главном (те же хосты, решения, причины, шаги);
  1 — частично: верное направление, но упущено или искажено важное; 0 — неверно, мимо или пусто.
- wrong: true, если ответ утверждает неверное о среде, решениях или истории пользователя (сверяй с эталоном).
- asks: true, если ответ спрашивает пользователя или откладывает до выяснения то, что эталон знал сразу.
- past: true, если ответ опирается на знание о прошлых сессиях, которого нет в самом репозитории.
Оценивай каждый ответ отдельно, не сравнивая с соседними. Ответ — только JSON:
{"A": {"score": 2, "wrong": false, "asks": false, "past": true}, ...}
"""


def answers(pid):
    out = {}
    for arm in ARMS:
        for rep in (1, 2):
            path = os.path.join(E, "runs", str(pid), f"{arm}-{rep}.jsonl")
            if not os.path.exists(path):
                continue
            ev = [json.loads(l) for l in open(path) if l.strip()]
            res = [e for e in ev if e.get("type") == "result"]
            tools = sum(1 for e in ev if e.get("type") == "assistant"
                        for c in e["message"]["content"] if c.get("type") == "tool_use")
            meta = json.load(open(path.replace(".jsonl", ".meta.json")))
            r = res[-1] if res else {}
            out[(arm, rep)] = {"text": (r.get("result") or "")[:4000], "turns": r.get("num_turns"),
                               "cost": r.get("total_cost_usd"), "tools": tools, "seconds": meta["seconds"],
                               "served": sum(len(x.get("facts") or []) for x in meta["recall"]),
                               "error": r.get("subtype") != "success"}
    return out


def grade_one(p):
    ans = answers(p["id"])
    keys = list(ans)
    random.Random(p["id"] * 7 + PASS).shuffle(keys)  # another letter order per pass
    letters = {chr(65 + n): k for n, k in enumerate(keys)}
    body = {"prompt": p["prompt"][:3000], "reference": p["reply"][:2000],
            "answers": {l: ans[k]["text"] for l, k in letters.items()}}
    env = {k: v for k, v in os.environ.items() if not k.startswith("MEMCUE_")} | {"MEMCUE_EXTRACT": "0",
                                                                                   "MEMCUE_BIN": "/usr/bin/true"}
    r = subprocess.run(["claude", "-p", "--model", "opus", "--tools", "", "--strict-mcp-config",
                        "--output-format", "json"],
                       input=PROMPT + json.dumps(body, ensure_ascii=False), capture_output=True, text=True,
                       env=env, timeout=900)
    try:
        envelope = json.loads(r.stdout)
    except json.JSONDecodeError:
        envelope = {}
    m = re.search(r"\{.*\}", envelope.get("result") or "", re.S)
    got = json.loads(m.group(0)) if m else {}
    return (str(p["id"]), {f"{letters[l][0]}-{letters[l][1]}": v for l, v in got.items() if l in letters},
            envelope.get("total_cost_usd") or 0)


def cmd_run():
    ids = json.load(open(os.path.join(E, "ids-v2.json")))
    ps = {p["id"]: p for p in json.load(open(os.path.join(E, "prompts-all.json")))}
    path = os.path.join(E, f"{GRADES}-{PASS}.json")
    done = json.load(open(path)) if os.path.exists(path) else {}
    todo = [ps[i] for i in ids if len(done.get(str(i), {})) < 2 * len(ARMS)]
    cost = 0
    with ThreadPoolExecutor(4) as ex:
        for pid, g, c in ex.map(grade_one, todo):
            done[pid] = g
            cost += c
            json.dump(done, open(path, "w"), ensure_ascii=False, indent=1)
    print(f"graded {sum(1 for v in done.values() if len(v) == 2 * len(ARMS))} of {len(ids)} prompts, "
          f"cost_usd {cost:.2f}")


def cmd_report():
    ids = json.load(open(os.path.join(E, "ids-v2.json")))
    grades = json.load(open(os.path.join(E, f"{GRADES}-1.json")))
    second = json.load(open(os.path.join(E, f"{GRADES}-2.json")))
    rows = {a: {"score": [], "wrong": 0, "asks": 0, "past": 0, "turns": [], "tools": [], "cost": [], "sec": [],
                "n": 0, "errors": 0, "served": []} for a in ARMS}
    per = {}  # pid -> arm -> run scores (each the mean of the two grading passes)
    same = flips = pairs = wrong_both = wrong_any = 0
    for pid in ids:
        ans = answers(pid)
        g, g2 = grades.get(str(pid), {}), second.get(str(pid), {})
        for (arm, rep), a in ans.items():
            v, v2 = g.get(f"{arm}-{rep}"), g2.get(f"{arm}-{rep}")
            if not v or not v2:
                continue
            pairs += 1; same += v["score"] == v2["score"]; flips += abs(v["score"] - v2["score"]) == 2
            wrong_both += v["wrong"] and v2["wrong"]; wrong_any += v["wrong"] or v2["wrong"]
            score = (v["score"] + v2["score"]) / 2
            r = rows[arm]
            # a flag counts only when both passes raise it
            r["n"] += 1; r["score"].append(score); r["wrong"] += v["wrong"] and v2["wrong"]
            r["asks"] += v["asks"] and v2["asks"]
            r["past"] += v["past"] and v2["past"]; r["turns"].append(a["turns"] or 0); r["tools"].append(a["tools"])
            r["cost"].append(a["cost"] or 0); r["sec"].append(a["seconds"]); r["errors"] += a["error"]
            r["served"].append(a["served"])
            per.setdefault(pid, {}).setdefault(arm, []).append(score)
    print(f"grader vs itself: same score {same}/{pairs}, 0<->2 flips {flips}, 'wrong' raised by both {wrong_both}"
          f" of {wrong_any} raised by either\n")
    print("mode    runs  score  2s   0s  wrong asks past  turns tools  cost$   sec  errors served")
    for arm, r in rows.items():
        s = r["score"]
        print(f"{arm:7s} {r['n']:4d}  {st.mean(s):.2f}  {s.count(2):3d} {s.count(0):3d}  {r['wrong']:4d} {r['asks']:4d}"
              f" {r['past']:4d}  {st.mean(r['turns']):5.1f} {st.mean(r['tools']):5.1f} {sum(r['cost']):6.2f}"
              f" {st.median(r['sec']):5.0f}  {r['errors']:4d}  {st.mean(r['served']):.1f}")
    print("\npairwise by prompt (mean of two reps): wins / ties / losses")
    for a, b in (("memcue", "files"), ("memcue", "none"), ("files", "none"), ("judge", "memcue"), ("judge", "files"),
                 ("judge", "none"), ("stubs", "memcue"), ("stubs", "judge"), ("stubs", "none"),
                 ("local", "memcue"), ("local", "judge"), ("local", "none"), ("pull", "push"), ("both", "push"),
                 ("both", "pull"), ("push", "none"), ("pull", "none"), ("both", "none"), ("push", "memcue"),
                 ("pullf", "pull"), ("pullf", "push"), ("pullf", "none")):
        if a not in ARMS or b not in ARMS:
            continue
        w = t = l = 0
        for pid, d in per.items():
            if a in d and b in d:
                x, y = st.mean(d[a]), st.mean(d[b])
                w += x > y; t += x == y; l += x < y
        diffs = [st.mean(d[a]) - st.mean(d[b]) for d in per.values() if a in d and b in d]
        rnd = random.Random(1)
        boot = sorted(st.mean(rnd.choices(diffs, k=len(diffs))) for _ in range(5000))
        print(f"{a} vs {b}: {w} / {t} / {l}; mean diff {st.mean(diffs):+.2f},"
              f" 95% bootstrap [{boot[125]:+.2f}, {boot[4874]:+.2f}]")
    print("\nrep-to-rep spread (prompts where the two reps of one mode differ):")
    for arm in ARMS:
        diff = sum(1 for d in per.values() if len(d.get(arm, [])) == 2 and d[arm][0] != d[arm][1])
        print(f"  {arm}: {diff} of {sum(1 for d in per.values() if len(d.get(arm, [])) == 2)}")

    # The pull and both modes: how often the agent searched the base itself.
    for arm in ("pull", "both", "pullf"):
        runs = searched = calls = 0
        for pid in ids:
            for rep in (1, 2):
                path = os.path.join(E, "runs", str(pid), f"{arm}-{rep}.meta.json")
                if os.path.exists(path):
                    n = len(json.load(open(path)).get("search", []))
                    runs += 1; searched += n > 0; calls += n
        if runs:
            print(f"\n{arm}: {runs} runs, searched in {searched}, {calls / runs:.1f} searches per run")

    # The stubs mode: how many stubs were offered and how many of them the agent read with memcue-show.
    offered = read = runs = with_read = 0
    for pid in ids:
        for rep in (1, 2):
            path = os.path.join(E, "runs", str(pid), f"stubs-{rep}.meta.json")
            if not os.path.exists(path):
                continue
            meta = json.load(open(path))
            runs += 1
            stubs = {i for x in meta.get("stubs", []) for i in x["stubs"]}
            got = {i for x in meta.get("show", []) for i in x} & stubs
            offered += len(stubs); read += len(got); with_read += bool(got)
    if runs and "stubs" in ARMS:
        print(f"\nstubs: {runs} runs, {offered / runs:.1f} stubs offered and {read / runs:.1f} read per run, "
              f"runs that read any {with_read}")


{"run": cmd_run, "report": cmd_report}[sys.argv[1]]()
