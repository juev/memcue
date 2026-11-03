#!/usr/bin/env python3
"""Grades and the report of the #613 comparison.

    grade613.py grade --out OUT --token FILE --pass N   # Opus, no tools, the arm hidden; OUT/grades-N/<run>.json
    grade613.py report --out OUT                        # the counts of the report, from two passes

A capture record is what #601 graded: the labels of the scenario, the two answers, every fact the run added
(id, kind, scope, source, expiry, text) and the memcue commands the agent ran. A probe record is the probe's prompt,
the labels and the answer. The prompts are those of #601 (preference601/fixtures/grade_*.md). Scope and source are
checked from the facts, not by the grader: the facts the grader names as the preference are global for D1-D4 and
the repository's for P1, and the user's.
"""
import argparse, glob, json, os, subprocess, sys, tempfile
from concurrent.futures import ThreadPoolExecutor

H = os.path.dirname(os.path.abspath(__file__))
F601 = os.path.join(H, "..", "preference601", "fixtures")
CLAUDE = os.path.expanduser("~/.local/bin/claude")


def labels():
    out = json.load(open(os.path.join(F601, "labels.json")))
    out.update(json.load(open(os.path.join(H, "fixtures", "labels613.json"))))
    return out


def stream(path):
    out = []
    for line in open(path):
        try:
            out.append(json.loads(line))
        except ValueError:
            pass
    return out


def answer(path):
    if not os.path.exists(path):
        return ""
    res = [e for e in stream(path) if e.get("type") == "result"]
    return (res[-1].get("result") or "") if res else ""


def memcue_calls(paths):
    calls = []
    for p in paths:
        if not os.path.exists(p):
            continue
        for e in stream(p):
            if e.get("type") != "assistant":
                continue
            for c in e["message"]["content"]:
                if c.get("type") == "tool_use" and c.get("name") == "Bash" and "memcue" in c["input"].get("command", ""):
                    calls.append(c["input"]["command"][:600])
    return calls


def facts(base):
    out = []
    for p in sorted(glob.glob(os.path.join(base, "*", "*.jsonl"))):
        f = json.loads(open(p).read().splitlines()[-1])
        if f.get("status") == "active":
            out.append({"id": f["id"], "kind": f.get("kind"), "scope": f.get("scope"), "source": f.get("source"),
                        "expires": f.get("expires", ""), "text": f["content"]})
    return out


def record(run):
    done = json.load(open(os.path.join(run, "rec", "done.json")))
    lab = labels()[done["scenario"]]
    rec = os.path.join(run, "rec")
    if done["phase"] == "capture":
        r = {"labels": lab, "answers": {"turn1": answer(os.path.join(rec, "turn1.jsonl")),
                                         "turn2": answer(os.path.join(rec, "turn2.jsonl"))},
             "base_changes": facts(os.path.join(rec, "base-after2")),
             "memcue_calls": memcue_calls([os.path.join(rec, "turn1.jsonl"), os.path.join(rec, "turn2.jsonl")])}
        kind = "control" if done["scenario"].startswith("C") else "durable"
    else:
        r = {"labels": lab, "prompt": None, "answer": answer(os.path.join(rec, "probe.jsonl"))}
        kind = "probe"
    return done, kind, r


def grade_one(args):
    run, token, npass, out = args
    name = os.path.basename(run)
    dst = os.path.join(out, f"grades-{npass}", name + ".json")
    if os.path.exists(dst):
        return
    done, kind, rec = record(run)
    if kind == "probe":
        from run613 import scenarios  # noqa: E402  the probe's prompt
        rec["prompt"] = next(s for s in scenarios() if s["id"] == done["scenario"])["probe"]["prompt"]
    prompt = open(os.path.join(F601, f"grade_{kind}.md")).read().replace("{{RECORD}}", json.dumps(rec, ensure_ascii=False))
    env = {"PATH": "/usr/local/bin:/usr/bin:/bin", "CLAUDE_CODE_OAUTH_TOKEN": open(token).read().strip()}
    with tempfile.TemporaryDirectory() as home:
        env |= {"HOME": home, "CLAUDE_CONFIG_DIR": os.path.join(home, ".claude")}
        r = subprocess.run([CLAUDE, "-p", "--model", "opus", "--tools", "", "--setting-sources", "",
                            "--strict-mcp-config", "--no-session-persistence", "--output-format", "json"],
                           input=prompt, capture_output=True, text=True, env=env, cwd=home, timeout=600)
    try:
        envl = json.loads(r.stdout)
        text = envl.get("result") or ""
        parsed = json.loads(text[text.index("{"):text.rindex("}") + 1])
    except (ValueError, KeyError):
        envl, parsed = {}, None
    os.makedirs(os.path.dirname(dst), exist_ok=True)
    json.dump({"run": name, "done": done, "kind": kind, "parsed": parsed, "cost": envl.get("total_cost_usd"),
               "facts": rec.get("base_changes")}, open(dst, "w"), ensure_ascii=False, indent=1)


def cmd_grade(a):
    runs = sorted(d for d in glob.glob(os.path.join(a.out, "runs", "*")) if os.path.exists(os.path.join(d, "rec", "done.json")))
    sys.path.insert(0, H)
    with ThreadPoolExecutor(4) as ex:
        list(ex.map(grade_one, [(r, a.token, a.pass_, a.out) for r in runs]))
    g = glob.glob(os.path.join(a.out, f"grades-{a.pass_}", "*.json"))
    print(f"pass {a.pass_}: {len(g)} grades of {len(runs)} runs, unparsed "
          f"{sum(1 for p in g if json.load(open(p))['parsed'] is None)}, cost "
          f"${sum(json.load(open(p))['cost'] or 0 for p in g):.2f}")


def both(out, name):
    a = json.load(open(os.path.join(out, "grades-1", name)))
    b = json.load(open(os.path.join(out, "grades-2", name)))
    return a, b


def cmd_report(a):
    lab = labels()
    rows = {}
    for p in sorted(glob.glob(os.path.join(a.out, "grades-1", "*.json"))):
        name = os.path.basename(p)
        g1, g2 = both(a.out, name)
        d = g1["done"]
        if g1["parsed"] is None or g2["parsed"] is None:
            print("unparsed", name)
            continue
        r = rows.setdefault(d["arm"], {"stored": 0, "partial": 0, "durable_n": 0, "scope_ok": 0, "source_user": 0,
                                       "probe_yes": 0, "probe_partial": 0, "probe_n": 0, "p1_stored": 0, "p1_scope": 0,
                                       "p1_n": 0, "p1_overreach": 0, "p1_probe_n": 0, "ctl_durable": 0, "ctl_n": 0,
                                       "noise": 0, "facts": 0, "ack": 0, "cost": 0.0, "agree": 0, "graded": 0})
        sc = d["scenario"]
        x, y = g1["parsed"], g2["parsed"]
        if g1["kind"] == "durable":
            stored = x["preference_stored"] == "yes" and y["preference_stored"] == "yes"
            ids = set(x.get("preference_fact_ids") or []) & set(y.get("preference_fact_ids") or [])
            pf = [f for f in g1["facts"] or [] if f["id"] in ids]
            want = lab[sc]["expected_scope"]
            scope_ok = bool(pf) and all((f["scope"] or "").lower() == "global" if want == "global"
                                        else (f["scope"] or "").startswith("repo:") for f in pf)
            src = bool(pf) and all(f["source"] in ("user", "user_confirmed") for f in pf)
            key = "p1" if sc == "P1" else None
            if key:
                r["p1_n"] += 1; r["p1_stored"] += stored; r["p1_scope"] += stored and scope_ok
            else:
                r["durable_n"] += 1; r["stored"] += stored; r["scope_ok"] += stored and scope_ok
                r["partial"] += (not stored) and "partial" in (x["preference_stored"], y["preference_stored"])
                r["source_user"] += stored and src
            r["ack"] += x["acknowledged"] == "yes" and y["acknowledged"] == "yes"
            r["noise"] += min(x.get("noise", 0), y.get("noise", 0))
            r["facts"] += len(g1["facts"] or [])
            r["agree"] += x["preference_stored"] == y["preference_stored"]
        elif g1["kind"] == "control":
            r["ctl_n"] += 1
            r["ctl_durable"] += x["durable_capture"] == "yes" and y["durable_capture"] == "yes"
            r["noise"] += min(x.get("noise", 0), y.get("noise", 0))
            r["facts"] += len(g1["facts"] or [])
            r["agree"] += x["durable_capture"] == y["durable_capture"]
        else:
            if sc == "P1":
                r["p1_probe_n"] += 1
                r["p1_overreach"] += x["applied"] in ("yes", "partial") and y["applied"] in ("yes", "partial")
            else:
                r["probe_n"] += 1
                r["probe_yes"] += x["applied"] == "yes" and y["applied"] == "yes"
                r["probe_partial"] += x["applied"] in ("yes", "partial") and y["applied"] in ("yes", "partial")
            r["agree"] += x["applied"] == y["applied"]
        r["graded"] += 1
    for arm, r in rows.items():
        print(f"{arm}: durable stored {r['stored']}/{r['durable_n']} (partial {r['partial']}), of them global "
              f"{r['scope_ok']}, source user {r['source_user']}; fresh-session applied {r['probe_yes']}/{r['probe_n']} "
              f"(with partial {r['probe_partial']}); P1 stored {r['p1_stored']}/{r['p1_n']}, repository scope "
              f"{r['p1_scope']}, applied in another repository {r['p1_overreach']}/{r['p1_probe_n']}; controls stored "
              f"as lasting {r['ctl_durable']}/{r['ctl_n']}; acknowledged {r['ack']}; facts {r['facts']}, noise "
              f"{r['noise']}; passes agree {r['agree']}/{r['graded']}")
    cost = {}
    for p in glob.glob(os.path.join(a.out, "runs", "*", "rec", "*.jsonl")):
        arm = os.path.basename(os.path.dirname(os.path.dirname(p))).split("-")[0]
        res = [e for e in stream(p) if e.get("type") == "result"]
        if res:
            cost.setdefault(arm, [0.0, 0, 0])
            cost[arm][0] += res[-1].get("total_cost_usd") or 0
            u = res[-1].get("usage") or {}
            cost[arm][1] += (u.get("input_tokens") or 0) + (u.get("cache_read_input_tokens") or 0) + \
                (u.get("cache_creation_input_tokens") or 0)
            cost[arm][2] += res[-1].get("duration_ms") or 0
    for arm, (c, tok, ms) in cost.items():
        print(f"{arm}: API-equivalent cost ${c:.2f}, input tokens {tok}, model time {ms / 1000:.0f} s")


def main():
    ap = argparse.ArgumentParser()
    sub = ap.add_subparsers(dest="cmd", required=True)
    g = sub.add_parser("grade")
    g.add_argument("--out", required=True)
    g.add_argument("--token", required=True)
    g.add_argument("--pass", dest="pass_", type=int, required=True)
    r = sub.add_parser("report")
    r.add_argument("--out", required=True)
    a = ap.parse_args()
    {"grade": cmd_grade, "report": cmd_report}[a.cmd](a)


if __name__ == "__main__":
    main()
