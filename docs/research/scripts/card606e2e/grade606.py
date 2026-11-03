#!/usr/bin/env python3
"""Blind grades of the #606 runs and the report, as e2e/grade.py graded #361.

    grade606.py run DATA PASS   -> DATA/grades-PASS.json (one Opus call a prompt: the reference and the four answers
                                   of the two arms and two repetitions under letters in a per-pass order)
    grade606.py report DATA     -> per-arm scores and flags, card size, searches, cost; pairwise by prompt with a
                                   bootstrap interval and the prompts each arm lost; rep-to-rep and grader noise
"""
import json, os, random, re, statistics as st, subprocess, sys
from concurrent.futures import ThreadPoolExecutor

ARMS = tuple(os.environ.get("E2E_ARMS", "current,trim").split(","))  # the second arm is compared with current
REPS = tuple(int(r) for r in os.environ.get("E2E_REPS", "1,2").split(","))
CAND = ARMS[1]
PROMPT = """Ты оцениваешь ответы агента на реплику пользователя. Агент работал в режиме планирования: только читал
репозиторий, без сети и без записи. Дан эталон — ответ, который пользователь получил в настоящей сессии (там у агента
были все инструменты; эталон может быть неполон). Ответы прогонов помечены буквами, источник скрыт.

Для каждого ответа:
- score: 2 — по существу верно и полезно, совпадает с эталоном в главном (те же хосты, решения, причины, шаги);
  1 — частично: верное направление, но упущено или искажено важное; 0 — неверно, мимо или пусто.
- wrong: true, если ответ утверждает неверное о среде, решениях или истории пользователя (сверяй с эталоном).
- asks: true, если ответ спрашивает пользователя или откладывает до выяснения то, что эталон знал сразу.
- past: true, если ответ опирается на знание о прошлых сессиях, которого нет в самом репозитории.
Оценивай каждый ответ отдельно, не сравнивая с соседними. Ответ — только JSON:
{"A": {"score": 2, "wrong": false, "asks": false, "past": true}, ...}
"""


def answers(data, pid):
    out = {}
    for arm in ARMS:
        for rep in REPS:
            path = os.path.join(data, "runs", str(pid), f"{arm}-{rep}.jsonl")
            if not os.path.exists(path):
                continue
            ev = [json.loads(l) for l in open(path) if l.strip()]
            res = [e for e in ev if e.get("type") == "result"]
            tools = sum(1 for e in ev if e.get("type") == "assistant"
                        for c in e["message"]["content"] if c.get("type") == "tool_use")
            meta = json.load(open(path.replace(".jsonl", ".meta.json")))
            r = res[-1] if res else {}
            card = set()
            for line in meta.get("card", []):
                card.update(json.loads(line).get("facts") or [])
            out[(arm, rep)] = {"card": len(card), "text": (r.get("result") or "")[:4000], "turns": r.get("num_turns"),
                               "cost": r.get("total_cost_usd"), "tools": tools, "seconds": meta["seconds"],
                               # the agent's own searches; the memory-recall hook's are -json -kind pitfall,environment
                               "searches": sum(1 for s in meta.get("search", [])
                                               if s.startswith("search") and "-kind pitfall,environment" not in s),
                               "error": r.get("subtype") != "success"}
    return out


def grade_one(args):
    data, p, npass = args
    ans = answers(data, p["id"])
    keys = list(ans)
    random.Random(p["id"] * 7 + npass).shuffle(keys)
    letters = {chr(65 + n): k for n, k in enumerate(keys)}
    body = {"prompt": p["prompt"][:3000], "reference": p["reply"][:3500],
            "answers": {l: ans[k]["text"] for l, k in letters.items()}}
    env = {k: v for k, v in os.environ.items() if not k.startswith(("MEMCUE_", "CLAUDECODE"))}
    r = subprocess.run(["claude", "-p", "--model", "opus", "--tools", "", "--strict-mcp-config",
                        "--no-session-persistence", "--settings", '{"disableAllHooks": true}',
                        "--output-format", "json"],
                       input=PROMPT + json.dumps(body, ensure_ascii=False), capture_output=True, text=True,
                       env=env, timeout=900, cwd="/tmp")
    try:
        envelope = json.loads(r.stdout)
    except json.JSONDecodeError:
        envelope = {}
    m = re.search(r"\{.*\}", envelope.get("result") or "", re.S)
    got = json.loads(m.group(0)) if m else {}
    return (str(p["id"]), {f"{letters[l][0]}-{letters[l][1]}": v for l, v in got.items() if l in letters},
            envelope.get("total_cost_usd") or 0)


def cmd_run(data, npass):
    ps = json.load(open(os.path.join(data, "prompts.json")))
    path = os.path.join(data, f"grades-{npass}.json")
    done = json.load(open(path)) if os.path.exists(path) else {}
    want = len(ARMS) * len(REPS)
    todo = [(data, p, npass) for p in ps if len(done.get(str(p["id"]), {})) < want]
    cost = 0
    with ThreadPoolExecutor(4) as ex:
        for pid, g, c in ex.map(grade_one, todo):
            done[pid] = g
            cost += c
            json.dump(done, open(path, "w"), ensure_ascii=False, indent=1)
    print(f"pass {npass}: graded {sum(1 for v in done.values() if len(v) == want)} of {len(ps)} prompts, "
          f"cost_usd {cost:.2f}")


def boot(diffs):
    rnd = random.Random(1)
    b = sorted(st.mean(rnd.choices(diffs, k=len(diffs))) for _ in range(5000))
    return b[125], b[4874]


def cmd_report(data):
    ps = json.load(open(os.path.join(data, "prompts.json")))
    g1 = json.load(open(os.path.join(data, "grades-1.json")))
    g2 = json.load(open(os.path.join(data, "grades-2.json")))
    rows = {a: {"score": [], "wrong": 0, "asks": 0, "past": 0, "turns": [], "tools": [], "cost": [], "sec": [],
                "searches": [], "errors": 0, "card": []} for a in ARMS}
    per, same, flips, pairs = {}, 0, 0, 0
    for p in ps:
        pid = p["id"]
        for (arm, rep), a in answers(data, pid).items():
            v, v2 = g1.get(str(pid), {}).get(f"{arm}-{rep}"), g2.get(str(pid), {}).get(f"{arm}-{rep}")
            if not v or not v2:
                continue
            pairs += 1
            same += v["score"] == v2["score"]
            flips += abs(v["score"] - v2["score"]) == 2
            r = rows[arm]
            score = (v["score"] + v2["score"]) / 2  # a flag counts only when both passes raise it
            r["score"].append(score)
            r["wrong"] += v["wrong"] and v2["wrong"]
            r["asks"] += v["asks"] and v2["asks"]
            r["past"] += v["past"] and v2["past"]
            r["turns"].append(a["turns"] or 0)
            r["tools"].append(a["tools"])
            r["cost"].append(a["cost"] or 0)
            r["sec"].append(a["seconds"])
            r["searches"].append(a["searches"])
            r["errors"] += a["error"]
            r["card"].append(a["card"])
            per.setdefault(pid, {}).setdefault(arm, []).append(score)
    print(f"grader vs itself: same score {same}/{pairs}, 0<->2 flips {flips}\n")
    print("arm      runs score  2s  0s wrong asks past turns tools  cost$  sec searched searches/run errors card")
    for arm, r in rows.items():
        s = r["score"]
        print(f"{arm:8s} {len(s):4d} {st.mean(s):.2f} {s.count(2):3d} {s.count(0):3d} {r['wrong']:5d} {r['asks']:4d}"
              f" {r['past']:4d} {st.mean(r['turns']):5.1f} {st.mean(r['tools']):5.1f} {sum(r['cost']):6.2f}"
              f" {st.median(r['sec']):4.0f} {sum(x > 0 for x in r['searches']):8d} {st.mean(r['searches']):12.1f}"
              f" {r['errors']:6d} {st.mean(r['card']):.0f} facts, runs without a card {r['card'].count(0)}")
    diffs, lost, won = [], [], []
    for pid, d in sorted(per.items()):
        if "current" in d and CAND in d:
            x = st.mean(d[CAND]) - st.mean(d["current"])
            diffs.append(x)
            (won if x > 0 else lost if x < 0 else []).append((pid, x))
    lo, hi = boot(diffs)
    print(f"\n{CAND} vs current by prompt (mean of the reps): {len(won)} / {len(diffs) - len(won) - len(lost)} / "
          f"{len(lost)}; mean diff {st.mean(diffs):+.2f}, 95% bootstrap [{lo:+.2f}, {hi:+.2f}]")
    print(f"prompts {CAND} lost: {[(pid, round(x, 2)) for pid, x in lost]}")
    print(f"prompts {CAND} won:  {[(pid, round(x, 2)) for pid, x in won]}")
    print("\nrep-to-rep spread (prompts where the two reps of one arm differ):")
    for arm in ARMS:
        n = sum(1 for d in per.values() if len(d.get(arm, [])) == 2 and d[arm][0] != d[arm][1])
        print(f"  {arm}: {n} of {sum(1 for d in per.values() if len(d.get(arm, [])) == 2)}")


if __name__ == "__main__":
    cmd, data = sys.argv[1], os.path.abspath(sys.argv[2])
    if cmd == "run":
        cmd_run(data, int(sys.argv[3]))
    else:
        cmd_report(data)
