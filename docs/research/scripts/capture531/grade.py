#!/usr/bin/env python3
"""Pre-labels and tables of #531, read-only over the kept run records. Nothing here runs an agent or opens a base.

    python3 -I -B grade.py selftest   the answer rules against crafted negation, paraphrase, invalid and no-verdict
                                      cases; the fail-closed answer and capture bindings against tampered copies of a
                                      kept record; the privacy shapes; and that every kept label still verifies.
                                      Exits 1 on a wrong outcome
    python3 -I -B grade.py privacy    counts a few shapes of local identifier (see PRIVATE) in results/, fixtures/,
                                      testdata/ and the report; prints file, shape and count, never the match; exits
                                      1 on a match. A narrow check: zero matches does not mean nothing private is left
    python3 -I -B grade.py table      one row per run as JSON on stdout: mechanics, capture, labels
    python3 -I -B grade.py summary    every count the report quotes from the records, by section, and what the kept
                                      files cannot reproduce
    python3 -I -B grade.py claims     the expected values hard-coded in CLAIMS (copied from the report by hand) against
                                      the computed metrics; exits 1 on a mismatch. It does not read the report, so a
                                      number edited there, or one missing from CLAIMS, is not caught

Labels, in three layers: the rule pre-label (`auto`); the first manual pass (results/manual.json, kept as written);
the second, independent pass with its adjudication (results/regrade.json). The final answer label is the adjudicated
one, and only when the answer text it was given still matches the record (answer_sha256); otherwise `unverified`.
answer_sha256 covers the answer text only, not the label: an edited `final` under a kept hash is not detected.
The adjudicated capture coverage counts only when capture_sha256, the sha256 of the canonical JSON of the run name,
the record's fact_changes, facts_second and cov_final, still matches and facts_second names exactly the changed facts;
otherwise the run's coverage is None, counted as unverified and as storing nothing.

Mechanics come from the run record alone (tool calls, their results, the base diff), not from a model: a search is a
memcue search call, the shelf is selected when a search names it in -in / in, a target is retrieved when its seeded ID
is in a tool result, a hint is seen when a tool result says "on the shelf match".
"""
import glob, hashlib, json, os, re, sys

HERE = os.path.dirname(os.path.abspath(__file__))
RUNS = os.path.join(HERE, "results", "runs")
REGRADE = os.path.join(HERE, "results", "regrade.json")
SCEN = {s["id"]: s for s in json.load(open(os.path.join(HERE, "fixtures", "scenarios.json")))["scenarios"]}
NEG = r"(?:not|n't|no|never|without|nor|neither|cannot)"


def rx(p, t):
    return re.search(p, t, re.I | re.S) is not None


def unresolved(t):
    return rx(r"\b(?:I (?:don't|do not|can't|cannot) (?:know|tell|find)|no (?:stored |recorded )?(?:information|record|fact|data)"
              r"|not (?:in|recorded|stored|documented|found)|nothing (?:stored|recorded|on)|unknown|isn't recorded"
              r"|no (?:record|mention)|have no|don't have)\b", t)


# Each rule returns correct, wrong, unresolved or no-verdict. A verdict needs the claim in a form that negation
# would flip: the patterns name the polarity on purpose.
def answer_label(sid, t):
    sid = sid.rstrip("w")  # C1w and C2w are graded as C1 and C2
    t = (t or "").strip()
    if not t:
        return "no-verdict"
    if sid in ("C1", "R1", "R2"):
        drop = rx(r"Brindle.{0,400}?(?:drop|discard|lose|loss|lost)", t) and not rx(r"Brindle (?:never|does not|doesn't) (?:drop|lose)", t)
        part = rx(r"partition", t)
        if drop and part:
            return "correct"
        if rx(r"Brindle (?:fits|is the (?:better|right) (?:fit|choice))|(?:use|choose|pick) Brindle\b", t) and not drop:
            return "wrong"
        return "unresolved" if unresolved(t) else "partial"
    if sid in ("C2", "R3"):
        limit = rx(r"binary|protobuf", t) and rx(r"(?:not|never|un)\s*-?tested|did(?:n't| not) (?:test|cover)|only (?:covered|tested)|says nothing|under 1 ?MB|text log", t)
        if limit:
            return "correct"
        if rx(r"\b(?:yes|good fit|great fit|well suited)\b", t) and not rx(NEG + r".{0,20}(?:good|great) fit", t):
            return "wrong"
        return "unresolved" if unresolved(t) else "partial"
    if sid == "C3":
        return "correct" if rx(r"4096", t) and rx(r"segment", t) else "partial"
    if sid == "C4":
        return "correct" if rx(r"10,?000|max_unacked", t) and rx(r"drop", t) else "partial"
    if sid in ("C5", "R4"):
        if rx(r"(?:does not|doesn't|no|not)\s+(?:yet\s+)?(?:support|have|offer)\s+HTTP/3|HTTP/3 (?:is|isn't) not supported|^\s*\**no\b", t):
            if not rx(r"(?:2\.0|v2|experimental|--h3|H3: ?true)", t):
                return "wrong"
        if rx(r"(?:2\.0|v2)", t) and rx(r"experimental|--h3|H3: ?true|off by default", t):
            return "correct"
        return "unresolved" if unresolved(t) else "partial"
    if sid == "C6":
        return "correct" if rx(r"\b3\b|three", t) and rx(r"a\.go", t) and rx(r"b\.go", t) else "partial"
    if sid == "R5":
        number = rx(r"\b\d+(?:\.\d+)?\s?(?:ms|s|sec|seconds|milliseconds|minutes)\b", t)
        if number and not rx(r"ack_timeout|redeliver", t):
            return "wrong"
        return "correct" if unresolved(t) or rx(r"(?:not|isn't|no)\b.{0,40}(?:document|record|stor|found|know)", t) else "partial"
    if sid == "R6":
        return "correct" if rx(r"awk", t) and rx(r"NR\s*>\s*1|NR!=1|NR\s*>=\s*2", t) and rx(r"\$2", t) and rx(r"-F\s*'?,'?|FS\s*=\s*\"?,", t) else "partial"
    if sid == "R7":
        if rx(r"/var/log/kestrel/runner\.log", t) and not rx(NEG + r"\b.{0,30}/var/log/kestrel/runner\.log", t):
            return "correct"
        return "unresolved" if unresolved(t) else "partial"
    if sid == "R8":
        caveat = rx(r"2\.0|v2|go\.mod|outdated|stale|may have changed|newer|since then|(?:re-?check|verify)", t)
        if rx(r"1\.8", t) and caveat:
            return "correct"
        if rx(r"(?:does not|doesn't|no)\b.{0,20}HTTP/3|^\s*\**no\b", t) and not caveat:
            return "wrong"
        return "unresolved" if unresolved(t) else "partial"
    if sid == "R9":
        if rx(r"vendor|Quarrel Inc", t) and rx(r"(?:one|single) run|not (?:independently )?verified|unverified|private|non-public", t):
            return "correct"
        return "unresolved" if unresolved(t) else "partial"
    return "no-verdict"


SELFTEST = [  # sid, text, expected
    ("R4", "Yes. go.mod pins v2.0.1 and Larkspur 2.0.0 added HTTP/3 (experimental, enable with --h3).", "correct"),
    ("R4", "No, Larkspur does not support HTTP/3.", "wrong"),
    ("R4", "Larkspur doesn't support HTTP/3 according to memory.", "wrong"),
    ("R4", "Since v2 it can speak QUIC-based HTTP/3 if you turn on Client{H3: true}; it is off by default.", "correct"),
    ("R4", "", "no-verdict"),
    ("R4", "I can't tell from what is here.", "unresolved"),
    ("R7", "/var/log/kestrel/runner.log", "correct"),
    ("R7", "It is not /var/log/kestrel/runner.log anymore, I don't know where.", "unresolved"),
    ("R7", "I don't know; memory has nothing on Kestrel.", "unresolved"),
    ("R5", "The default backoff is 500 ms, doubling up to 30 s.", "wrong"),
    ("R5", "Memory has no record of a retry backoff for Quarrel; only redelivery after ack_timeout (30 s) is known.", "correct"),
    ("R5", "Not documented in anything I can see.", "correct"),
    ("R1", "Neither as is: Brindle silently drops the oldest unacked message past 10,000; Quarrel only orders within a partition.", "correct"),
    ("R1", "Brindle discards old pending messages without telling anyone once a consumer lags; Quarrel keeps order per partition only.", "correct"),
    ("R1", "Use Brindle: it keeps strict order per queue.", "wrong"),
    ("R1", "Brindle never drops messages and Quarrel is unordered.", "partial"),
    ("R1", "I don't have any stored information on either library.", "unresolved"),
    ("R3", "Unproven: the benchmark only covered text logs under 1 MB; binary frames were not tested.", "correct"),
    ("R3", "Yes, Ostrich is a good fit, 4.1x compression.", "wrong"),
    ("R3", "It is not a good fit for binary payloads? Unknown; nothing stored.", "unresolved"),
    ("R8", "Our 2025 note says Larkspur 1.8 had no HTTP/3, but go.mod uses v2.0.1, so it may have changed.", "correct"),
    ("R8", "No, Larkspur doesn't support HTTP/3.", "wrong"),
    ("R9", "A vendor blog figure (3.1M vs 1.0M msg/s), one run on a private rig, not independently verified.", "correct"),
    ("R9", "Quarrel is 3x faster than Brindle.", "partial"),
]

# Cases written to break the rules: the label a reader would give, which the rules may miss. They do not fail the
# self-test; the count says how far a pre-label can be trusted, and is why every run is read by hand.
HARD = [
    ("R1", "Brindle does not silently drop messages; Quarrel orders per partition.", "partial"),
    ("R1", "Brindle: ordered per queue, but past ten thousand unacked it throws the oldest away. Quarrel: per-shard order only.", "correct"),
    ("R4", "HTTP/3: no.", "wrong"),
    ("R4", "Memory says 1.8 lacked HTTP/3, so no — although 2.0 might have experimental support, I did not check.", "partial"),
    ("R7", "Not at /var/log/kestrel/runner.log — that was the old path.", "wrong"),
    ("R5", "Quarrel retries after 30 s by default.", "wrong"),
    ("R3", "Ostrich is not tested on binary, so no.", "correct"),
    ("R8", "Larkspur 1.8 does not support HTTP/3.", "wrong"),
]


def capture_labels(sid, changes, seed):
    """Pre-labels of what a capture run stored; the manual pass checks every new or changed fact."""
    sid = sid.rstrip("w")
    new = [c for c in changes if c["change"] == "new"]
    changed = [c for c in changes if c["change"] == "changed"]
    txt = lambda c: c.get("content", "")
    lab = {"new": len(new), "changed": len(changed), "new_on_shelf": sum(c["scope"].startswith("shelf:") for c in new),
           "new_with_source": sum(rx(r"source|docs|documentation|changelog|reference|wiki|blog|forum", txt(c)) for c in new)}
    cov = {}
    if sid == "C1":
        cov["D1"] = any(rx(r"Brindle", txt(c)) and rx(r"10,?000|max_unacked|drop", txt(c)) for c in new)
        cov["D2"] = any(rx(r"Quarrel", txt(c)) and rx(r"partition", txt(c)) for c in new)
        cov["D3"] = any(rx(r"benchmark|3\.1 ?M|throughput|3x", txt(c)) and rx(r"vendor|one run|single run|not (?:independently )?verified|unverified|non-public|private", txt(c)) for c in new)
        cov["H1"] = any(rx(r"never loses", txt(c)) and not rx(r"claim|anonymous|forum|unsupported|unverified", txt(c)) for c in new)
        cov["H2"] = any(c.get("kind") == "decision" for c in new)
        cov["H3"] = any(rx(r"3x|3\.1 ?M", txt(c)) and not rx(r"vendor|one run|single run|verified|non-public|private|claim", txt(c)) for c in new)
    elif sid == "C2":
        cov["D1"] = any(rx(r"1 ?MB|text log", txt(c)) and rx(r"binary|not tested|untested", txt(c)) for c in new)
        cov["D2"] = any(rx(r"dictionar", txt(c)) for c in new)
        cov["H1"] = any(rx(r"4\.1", txt(c)) and not rx(r"text|1 ?MB|not tested|untested", txt(c)) for c in new)
    elif sid == "C3":
        cov["D1"] = any(rx(r"4096", txt(c)) and rx(r"segment|batch", txt(c)) for c in new + changed)
        cov["H1"] = any(rx(r"18234|exported|changed batch_size to|set batch_size to \d+ in", txt(c)) for c in new)
    elif sid == "C4":
        cov["H1"] = any(rx(r"Brindle", txt(c)) and rx(r"10,?000|max_unacked|drop", txt(c)) for c in new)
    elif sid == "C5":
        s5 = seed.get("S5")
        s5c = [c for c in changed if c["id"] == s5]
        cov["S5_fixed"] = bool(s5c)
        cov["D1"] = any(rx(r"2\.0", txt(c)) and rx(r"HTTP/3", txt(c)) for c in new + changed)
        cov["H1"] = (not s5c) and any(rx(r"HTTP/3", txt(c)) for c in new)
    elif sid == "C6":
        cov["H1"] = bool(new or changed)
    lab["cov"] = cov
    # A new fact that covers no labelled item: on-topic detail the labels did not ask for, or noise.
    items = {"C1": [lambda t: rx(r"Brindle", t) and rx(r"10,?000|max_unacked|drop", t),
                    lambda t: rx(r"Quarrel", t) and rx(r"partition", t),
                    lambda t: rx(r"benchmark|3\.1 ?M|throughput|3x", t)],
             "C2": [lambda t: rx(r"1 ?MB|text log", t), lambda t: rx(r"dictionar", t)],
             "C3": [lambda t: rx(r"4096", t)]}.get(sid, [])
    lab["extra"] = sum(not any(f(txt(c)) for f in items) for c in new)
    return lab


def mechanics(rec):
    calls = []  # (op, scope arg or None, result, index of the tool call)
    for i, t in enumerate(rec["tools"]):
        if t["name"] == "Bash":
            cmd = t["input"].get("command", "")
            for m in re.finditer(r"memcue\s+(search|add|correct|retire|reopen)\b([^\n;|&]*)", cmd):
                if re.match(r"\s*(?:--help|-h)\b", m.group(2)):  # reading the usage is not a call
                    continue
                ins = re.findall(r"-in\s+(\S+)", m.group(2))
                shelf = re.findall(r"-shelf\s+(\S+)", m.group(2))
                calls.append((m.group(1), ",".join(ins + ["shelf:" + s for s in shelf]) or None, t["result"], i))
        elif t["name"].startswith("mcp__memcue__memcue_"):
            op = t["name"].split("memcue_")[-1]
            a = t["input"]
            calls.append((op, a.get("in") or (("shelf:" + a["shelf"]) if a.get("shelf") else None), t["result"], i))
        elif t["name"] == "Skill":
            calls.append(("skill", t["input"].get("skill") or t["input"].get("command"), "", i))
    searches = [c for c in calls if c[0] == "search"]

    def reads_shelf(scope):
        # memcue returns at the first "all" of an -in list and leaves the shelf out (internal/recall/search.go:94-97),
        # so all,shelf names the shelf and does not read it.
        parts = [p.strip().strip("\"'") for p in (scope or "").split(",")]
        return any(p.startswith("shelf") for p in parts) and "all" not in parts
    results = "\n".join(t["result"] for t in rec["tools"])
    # The first shelf hint: did a shelf search come in the same tool call or before it ("before"), in a later call
    # ("followed"), or never ("ignored")?
    hint_at = next((i for i, t in enumerate(rec["tools"]) if "on the shelf match" in t["result"]), None)
    shelf_at = [c[3] for c in searches if reads_shelf(c[1])]
    hint_order = None
    if hint_at is not None:
        hint_order = ("before" if any(i <= hint_at for i in shelf_at)
                      else "followed" if any(i > hint_at for i in shelf_at) else "ignored")
    named_not_read = [c for c in searches if c[1] and "shelf" in c[1] and not reads_shelf(c[1])]
    s = SCEN[rec["scenario"].removesuffix("chain")]
    ids = rec["seed_ids"]
    return {
        "searches": len(searches),
        "shelf_searches": sum(1 for c in searches if reads_shelf(c[1])),
        "shelf_named_not_read": len(named_not_read),
        "shelf_named_not_read_at": [searches.index(c) + 1 for c in named_not_read],  # 1-based, among the searches
        "adds": sum(1 for c in calls if c[0] == "add"),
        "corrects": sum(1 for c in calls if c[0] == "correct"),
        "cli_corrects": sum(1 for c in calls if c[0] == "correct" and rec["tools"][c[3]]["name"] == "Bash"),
        "retires": sum(1 for c in calls if c[0] == "retire"),
        "skill": any(c[0] == "skill" for c in calls),
        "hint_seen": "on the shelf match" in results,
        "hint_order": hint_order,
        "target_retrieved": [l for l in s.get("target", []) if ids.get(l) and ids[l] in results],
        "noise_retrieved": [l for l in s.get("noise", []) if ids.get(l) and ids[l] in results],
        "first_search_scope": (searches[0][1] or "default") if searches else None,
        "longest_result": max([len(t["result"]) for t in rec["tools"]] or [0]),
    }


def answer_sha(text):
    return hashlib.sha256((text or "").encode("utf-8")).hexdigest()


def final_label(second, answer):
    """Fail closed: the adjudicated label of the second pass, only if it was given this exact answer text."""
    if not second or second.get("answer_sha256") != answer_sha(answer):
        return "unverified"
    return second.get("final") or "unverified"


def capture_sha(key, changes, facts, cov):
    """sha256 of the canonical JSON of a run's name, its stored or changed facts as the record holds them, and the
    second pass's reading of them (facts_second) and adjudicated coverage (cov_final)."""
    canon = json.dumps({"run": key, "fact_changes": changes, "facts_second": facts, "cov_final": cov},
                       sort_keys=True, ensure_ascii=False, separators=(",", ":"))
    return hashlib.sha256(canon.encode("utf-8")).hexdigest()


def final_capture(second, key, changes):
    """Fail closed: the adjudicated coverage and per-fact reading of the second pass, only if its capture_sha256 is
    the hash of exactly these changes of this run with exactly this reading, and the reading names every changed
    fact and nothing else; otherwise None (unverified)."""
    if not second or "cov_final" not in second or "facts_second" not in second:
        return None
    facts, cov = second["facts_second"], second["cov_final"]
    if second.get("capture_sha256") != capture_sha(key, changes, facts, cov):
        return None
    if not isinstance(facts, list) or sorted(f.get("id", "") for f in facts) != sorted(c["id"] for c in changes):
        return None
    return {"cov": cov, "facts": facts}


def table():
    manual = {}
    mp = os.path.join(HERE, "results", "manual.json")
    if os.path.exists(mp):
        manual = json.load(open(mp))
    regrade = json.load(open(REGRADE)) if os.path.exists(REGRADE) else {}
    rows = []
    for f in sorted(glob.glob(os.path.join(RUNS, "*.json"))):
        rec = json.load(open(f))
        sid = rec["scenario"]
        key = f"{rec['arm']}__{sid}__r{rec['rep']}"
        if rec.get("chain_from"):  # graded by hand; the mechanics use the base scenario's labels
            sid = sid[:-len("chain")]
        fin = rec.get("final") or {}
        row = {"run": key, "arm": rec["arm"], "scenario": rec["scenario"], "chain_from": rec.get("chain_from"), "rep": rec["rep"], "model": rec["model"], "exit": rec["exit"],
               "cost_usd": fin.get("total_cost_usd"), "duration_ms": fin.get("duration_ms"), "turns": fin.get("num_turns"),
               "out_tokens": (fin.get("usage") or {}).get("output_tokens"),
               "answer": fin.get("result") or "", "mech": mechanics(rec),
               "auto": answer_label(sid, fin.get("result") or "")}
        second = regrade.get(key, {})
        if SCEN[sid]["kind"] == "capture" and not rec.get("chain_from"):
            row["capture"] = capture_labels(sid, rec["fact_changes"], rec["seed_ids"])
            row["changes"] = rec["fact_changes"]
            # Coverage counts only where the second pass read exactly these stored facts; otherwise None, unverified.
            fc = final_capture(second, key, rec["fact_changes"])
            row["cov_final"] = fc and fc["cov"]
            row["facts_final"] = fc and fc["facts"]
        if rec.get("chain_from"):
            # What the agent stored itself in the run this one reads: those IDs, not the seeded ones, are its targets.
            src = json.load(open(os.path.join(RUNS, f"{rec['arm']}__{rec['chain_from']}__r{rec['rep']}.json")))
            own = [c["id"] for c in src["fact_changes"] if c["change"] == "new"]
            results = "\n".join(t["result"] for t in rec["tools"])
            row["own"] = {"stored": len(own), "returned": sum(i in results for i in own)}
        row["manual"] = manual.get(key, {})
        row["regrade"] = second
        row["final_answer"] = final_label(second, fin.get("result"))
        rows.append(row)
    return rows


def capture_selftest():
    """Tamper cases on a kept record, hm-base__C2__r3 (the one run whose coverage the adjudication changed): the
    adjudication holds only for the facts and the reading it was bound to. Returns the number of wrong outcomes."""
    key = "hm-base__C2__r3"
    rec = json.load(open(os.path.join(RUNS, key + ".json")))
    second = json.load(open(REGRADE))[key]
    ch = rec["fact_changes"]
    edit = lambda **kw: [dict(ch[0], **kw)] + ch[1:]
    without = lambda k: {x: v for x, v in second.items() if x != k}
    added = ch + [dict(ch[0], id="01TAMPERTAMPERTAMPERTAMPER", content="Ostrich needs its dictionary to decompress.")]
    rehashed = dict(second, capture_sha256=capture_sha(key, added, second["facts_second"], second["cov_final"]))
    cases = [
        ("intact", second, key, ch, {"D1": True, "D2": False, "H1": False}),
        ("fact text edited", second, key, edit(content=ch[0]["content"] + " The dictionary is needed to decompress."), None),
        ("fact moved off the shelf", second, key, edit(scope="project"), None),
        ("fact added", second, key, added, None),
        ("fact removed", second, key, [], None),
        ("capture_sha256 missing", without("capture_sha256"), key, ch, None),
        ("adjudication read as another run's", second, "hm-base__C2__r2", ch, None),
        ("cov_final edited, hash kept", dict(second, cov_final=dict(second["cov_final"], D2=True)), key, ch, None),
        ("facts_second edited, hash kept",
         dict(second, facts_second=[dict(second["facts_second"][0], names_source=True)]), key, ch, None),
        ("cov_final missing", without("cov_final"), key, ch, None),
        ("hash recomputed over a fact the second pass did not read", rehashed, key, added, None),
    ]
    bad = 0
    for name, sec, k, changes, want in cases:
        got = final_capture(sec, k, changes)
        got = got and got["cov"]
        bad += got != want
        print(("ok  " if got == want else "BAD ") + f"capture {key}, {name}: want={want} got={got}")
    answer = rec["final"]["result"]
    for name, text, want in [("intact", answer, "correct"), ("answer edited", answer + " Yes.", "unverified")]:
        got = final_label(second, text)
        bad += got != want
        print(("ok  " if got == want else "BAD ") + f"answer {key}, {name}: want={want} got={got}")
    return bad


def main():
    cmd = sys.argv[1] if len(sys.argv) > 1 else "table"
    if cmd == "selftest":
        bad = 0
        for sid, text, want in SELFTEST:
            got = answer_label(sid, text)
            ok = got == want
            bad += not ok
            print(("ok  " if ok else "BAD ") + f"{sid} want={want} got={got} :: {text[:90]!r}")
        print(f"{len(SELFTEST) - bad}/{len(SELFTEST)} as expected")
        miss = 0
        for sid, text, want in HARD:
            got = answer_label(sid, text)
            miss += got != want
            print(("hard-ok   " if got == want else "hard-MISS ") + f"{sid} reader={want} rule={got} :: {text[:80]!r}")
        print(f"hard cases: the rules agree with the reader on {len(HARD) - miss}/{len(HARD)}")
        sha = answer_sha("Neither fits.")
        for second, want in [({}, "unverified"), ({"final": "correct"}, "unverified"),
                             ({"final": "correct", "answer_sha256": answer_sha("other text")}, "unverified"),
                             ({"answer_sha256": sha}, "unverified"),
                             ({"final": "wrong", "answer_sha256": sha}, "wrong")]:
            got = final_label(second, "Neither fits.")
            bad += got != want
            print(("ok  " if got == want else "BAD ") + f"final label want={want} got={got} :: {sorted(second)}")
        bad += capture_selftest()
        for text, want in [("/home/someone/x", ["path"]), ("cd /Users/someone", ["path"]), ("x@mail.invalid", ["email"]),
                           ("/tmp/r531-stand/runs", ["stand"]), ("ops@example.com", []), ("/var/log/kestrel/runner.log", []),
                           ("s3://archive/telemetry", []), ("<STAND>/runs/cc-base/C1/r1/ws", [])]:
            got = [m.lastgroup for m in PRIVATE.finditer(text)]
            bad += got != want
            print(("ok  " if got == want else "BAD ") + f"privacy shape want={want} got={got}")
        rows = table()
        caps = [r for r in rows if "capture" in r]
        cov_ok = sum(r["cov_final"] is not None for r in caps)
        ans_ok = sum(r["final_answer"] != "unverified" for r in rows)
        bad += (cov_ok != len(caps)) + (ans_ok != len(rows))
        print(f"kept records: {cov_ok}/{len(caps)} capture adjudications and {ans_ok}/{len(rows)} answer labels verify")
        sys.exit(1 if bad else 0)
    if cmd == "privacy":
        hits, scanned = privacy()
        for f, kind, n in hits:
            print(f"private: {f}: {kind} x{n}")
        print(f"{sum(n for _, _, n in hits)} matches in {len(hits)} file/shape pairs; {scanned} files scanned. "
              f"Shapes only: {', '.join(PRIVATE_KINDS.values())}. Zero matches is not evidence that nothing private "
              "is left (see the report's data and isolation section).")
        sys.exit(1 if hits else 0)
    if cmd == "summary":
        m = metrics(table())
        section = None
        for k, v in m.items():
            if k.split(".")[0] != section:
                section = k.split(".")[0]
                print(f"## {section}")
            print(f"{k}: {v}")
        print("## not inferable from the kept files")
        for line in NOT_INFERABLE:
            print("- " + line)
        return
    if cmd == "claims":
        m = metrics(table())
        bad = 0
        for key, want in CLAIMS:
            got = m.get(key, "<missing>")
            ok = str(got) == str(want)
            bad += not ok
            if not ok:
                print(f"MISMATCH {key}: report={want} summary={got}")
        print(f"{len(CLAIMS) - bad}/{len(CLAIMS)} hard-coded expected values match the computed metrics")
        sys.exit(1 if bad else 0)
    print(json.dumps(table(), ensure_ascii=False, indent=1))


# Shapes of a local identifier: an absolute path under a home or data root, the stand or worktree directory, an e-mail
# address outside example.com. The synthetic paths of the fixtures (/var/log/kestrel/..., s3://...) do not match.
# Anything else (a bare host or user name, an IP address, a token, a person's name, a path outside these roots) is not
# looked for: zero matches does not mean nothing private is left.
PRIVATE_KINDS = {"path": "absolute path under /opt, /home, /Users, /root, /srv or /mnt",
                 "stand": "stand directory name", "worktree": "worktree directory",
                 "email": "e-mail address outside example.com"}
PRIVATE = re.compile(r"(?P<path>/(?:opt|home|Users|root|srv|mnt)/)|(?P<stand>r531-stand)|(?P<worktree>\.worktrees)"
                     r"|(?P<email>[\w.+-]+@(?!example\.com\b)[\w-]+\.[\w.]+)")
# What is published: the kept data and the report. grade.py holds the shapes themselves; __pycache__ is not kept.
PUBLISHED = [os.path.join(HERE, d) for d in ("results", "fixtures", "testdata")] + \
            [os.path.join(HERE, "..", "..", "research-shelf-531-2026-10.md")]


def privacy():
    """Which published file holds which shape, and how often; never the matched text, which may be the identifier."""
    hits, scanned = [], 0
    for p in PUBLISHED:
        files = [p] if os.path.isfile(p) else sorted(glob.glob(os.path.join(p, "**", "*"), recursive=True))
        for f in files:
            if not os.path.isfile(f) or "__pycache__" in f.split(os.sep):
                continue
            scanned += 1
            found = {}
            for m in PRIVATE.finditer(open(f, encoding="utf-8", errors="replace").read()):
                found[m.lastgroup] = found.get(m.lastgroup, 0) + 1
            hits += [(os.path.relpath(f, HERE), PRIVATE_KINDS[k], n) for k, n in sorted(found.items())]
    return hits, scanned


ARMS = ("cc-base", "cc-cand", "hm-base", "hm-cand")
LABELS = ("correct", "partial", "unresolved", "wrong")
ITEMS = {"C1": ["D1", "D2", "D3"], "C1w": ["D1", "D2", "D3"], "C2": ["D1", "D2"], "C2w": ["D1", "D2"], "C3": ["D1"]}
# Experiment A: which shelf facts are relevant to each query, labelled by hand from the fact texts after the run (the
# report's section A); mechanics.json holds what each path returned.
RELEVANT = {"q1": ["S1", "S2", "S3"], "q2": ["S4"], "q3": ["S5"], "q4": [], "q5": [], "q6": ["S1"], "q7": ["S1"],
            "q8": ["S5"], "q9": ["S4"], "q10": ["S4"]}
NOT_INFERABLE = [
    "the content of the session context (hook output, Hermes system block): not in the records, so the ten answers "
    "that state a seeded fact from it are not checked here",
    "the raw records before anonymization (the 315 privacy matches, the 62 runs with absolute paths): removed",
    "the isolation check (counts of the live bases): taken outside the records",
    "the size of today's session-start note part 1 (3,061 bytes): product output, not a kept file",
    "the stand's binary hash, the Claude Code version and the absence of a memcue binary on the default PATH",
    "anything about which instruction files the built-in plugins loaded",
    "the four R9 'one entry on Quarrel' partials and the R8 'general age caveat' wording: read by hand; only their "
    "labels and counts are here",
]


def counts(labels):
    return ",".join(str(labels.count(k)) for k in LABELS)


def first_label(r):
    """The first pass as written, only for an answer the hash still binds."""
    return r["manual"].get("answer", "unverified") if r["final_answer"] != "unverified" else "unverified"


def lenient_label(r):
    """The final label with the one-partition rule set aside, only for an answer the hash still binds."""
    if r["final_answer"] == "unverified":
        return "unverified"
    return r["regrade"].get("final_without_one_partition_rule", r["final_answer"])


def metrics(rows):
    """Every count the report quotes from the records, by section; values are strings as the report writes them."""
    m = {}
    pick = lambda rs, arm=None, sids=None: [r for r in rs if (arm is None or r["arm"] == arm)
                                            and (sids is None or r["scenario"] in sids)]
    grid = [r for r in rows if r["model"] == "opus" and not r["chain_from"]]
    chain = [r for r in rows if r["chain_from"]]
    sonnet = [r for r in rows if r["model"] == "sonnet"]
    caps = [r for r in rows if "capture" in r]
    cov = lambda r: r["cov_final"] or {}
    facts = lambda r: {f["id"]: f for f in (r["facts_final"] or [])}
    items = lambda rs: (f"{sum(cov(r).get(i, False) for r in rs for i in ITEMS[r['scenario']])}/"
                        f"{sum(len(ITEMS[r['scenario']]) for r in rs)}")
    frac = lambda rs, f: f"{sum(1 for r in rs if f(r))}/{len(rs)}"
    found_all = lambda r: len(r["mech"]["target_retrieved"]) == len(SCEN[r["scenario"].removesuffix("chain")].get("target", []))
    has_target = lambda r: bool(SCEN[r["scenario"].removesuffix("chain")].get("target")) and not r["chain_from"]

    m["runs.total"] = len(rows)
    m["runs.opus_grid"] = len(grid)
    m["runs.chain"] = len(chain)
    m["runs.sonnet"] = len(sonnet)
    m["runs.exit_nonzero"] = sum(r["exit"] != 0 for r in rows)
    m["runs.capture_read_by_second_pass"] = len(caps)
    m["runs.capture_unverified"] = sum(r["cov_final"] is None for r in caps)
    m["runs.answer_unverified"] = sum(r["final_answer"] == "unverified" for r in rows)
    m["runs.longest_tool_result_chars"] = max(r["mech"]["longest_result"] for r in rows)
    m["runs.answers_over_1500_chars"] = sum(len(r["answer"]) > 1500 for r in rows)
    raw = [open(f, encoding="utf-8").read() for f in sorted(glob.glob(os.path.join(RUNS, "*.json")))]
    marks = ("<STAND>", "<REPO>", "<real-hostname>")
    m["runs.anonymized_placeholders"] = ", ".join(f"{p} {sum(t.count(p) for t in raw)}" for p in marks)
    m["runs.files_with_placeholder"] = sum(any(p in t for p in marks) for t in raw)
    m["runs.answers_all_c,p,u,w"] = counts([r["final_answer"] for r in rows])

    m["grading.manual_differs_from_rule"] = sum(r["manual"].get("answer") != r["auto"] for r in rows)
    m["grading.first_differs_from_second"] = sum(r["regrade"]["first"] != r["regrade"]["second"] for r in rows)
    changed = [r for r in rows if r["regrade"]["final"] != r["regrade"]["first"]]
    m["grading.final_differs_from_first"] = len(changed)
    m["grading.correct_to_partial"] = sum(r["regrade"]["first"] == "correct" and r["final_answer"] == "partial" for r in changed)
    m["grading.correct_to_wrong"] = sum(r["regrade"]["first"] == "correct" and r["final_answer"] == "wrong" for r in changed)
    m["grading.changes_due_to_one_partition_alone"] = sum(lenient_label(r) == r["regrade"]["first"] for r in changed)
    m["grading.partial_only_for_one_partition"] = sum(r["final_answer"] == "partial" and lenient_label(r) == "correct"
                                                      for r in rows)
    against = [r for r in rows if r["regrade"]["final"] != r["regrade"]["second"]]
    m["grading.ruled_against_second"] = len(against)
    m["grading.ruled_against_second_R5"] = sum(r["scenario"] == "R5" for r in against)
    m["grading.ruled_against_second_one_partition_recall"] = sum(
        r["scenario"].startswith("R") and bool(r["regrade"].get("one_partition_unhedged")) for r in against)
    low = [r for r in rows if r["regrade"].get("second_confidence") == "low"]
    m["grading.second_low_confidence"] = len(low)
    m["grading.second_low_confidence_among_changed"] = sum(r in changed for r in low)
    pre = lambda r: dict(r["capture"]["cov"], **(r["manual"].get("cov") or {}))
    m["grading.capture_items_first_vs_final_differ"] = sum(pre(r).get(k) != v for r in caps for k, v in cov(r).items())

    for arm in ARMS:
        rs = pick(grid, arm)
        for sid in ("C1", "C1w", "C2", "C2w"):
            m[f"capture.{sid}.{arm}"] = items(pick(rs, sids=[sid]))
        out = pick(rs, sids=["C1", "C1w", "C2", "C2w"])
        m[f"capture.outside_total.{arm}"] = items(out)
        m[f"capture.runs_with_shelf_fact.{arm}"] = frac(out, lambda r: r["capture"]["new_on_shelf"] > 0)
        c3 = pick(rs, sids=["C3"])
        m[f"capture.C3_pitfall.{arm}"] = f"{items(c3)}, on the shelf {sum(cov(r).get('H_misplaced', False) for r in c3)}"
        stored = pick(rs, sids=list(ITEMS))
        new = [(r, c) for r in stored for c in r["changes"] if c["change"] == "new"]
        shelf = [(r, c) for r, c in new if c["scope"].startswith("shelf:")]
        m[f"capture.new_facts.{arm}"] = len(new)
        m[f"capture.new_on_shelf.{arm}"] = len(shelf)
        m[f"capture.shelf_naming_source.{arm}"] = (
            f"{sum(facts(r).get(c['id'], {}).get('names_source') is True for r, c in shelf)}/{len(shelf)}" if shelf else "—")
        m[f"capture.extra.{arm}"] = sum(facts(r).get(c["id"], {}).get("covers") == ["extra"] for r, c in new)
        m[f"capture.extra_rule_prelabel.{arm}"] = sum(r["capture"]["extra"] for r in stored)
        c1 = pick(rs, sids=["C1", "C1w"])
        bench = sum(cov(r).get("D3", False) or cov(r).get("H3", False) for r in c1)
        m[f"capture.vendor_benchmark_stored/as_claim.{arm}"] = f"{bench} / {sum(cov(r).get('D3', False) for r in c1) if bench else '—'}"
        m[f"capture.forum_claim_as_fact.{arm}"] = sum(cov(r).get("H1", False) for r in c1)
        m[f"capture.choice_as_decision.{arm}"] = sum(cov(r).get("H2", False) for r in c1)
        m[f"capture.new_facts_outside_C1_C3.{arm}"] = sum(c["change"] == "new" for r in pick(rs, sids=["C4", "C5", "C6"]) for c in r["changes"])
        c4 = pick(rs, sids=["C4"])
        m[f"capture.C4.{arm}"] = (f"duplicate {frac(c4, lambda r: cov(r).get('H1', False))}, "
                                  f"searched {frac(c4, lambda r: r['mech']['searches'] > 0)}, "
                                  f"shelf searched {frac(c4, lambda r: r['mech']['shelf_searches'] > 0)}")
        c5 = pick(rs, sids=["C5"])
        m[f"capture.C5.{arm}"] = (f"corrected {frac(c5, lambda r: cov(r).get('S5_fixed', False))}, "
                                  f"searched {frac(c5, lambda r: r['mech']['searches'] > 0)}, "
                                  f"with '-- ' {sum(c['content'].startswith('-- ') for r in c5 for c in r['changes'])}")
        m[f"capture.C6_stored.{arm}"] = frac(pick(rs, sids=["C6"]), lambda r: cov(r).get("H1", False))
        task = [r for r in rs if r["scenario"].startswith("C")]
        m[f"capture.answers_c,p,u,w.{arm}"] = counts([r["final_answer"] for r in task])
        m[f"capture.answers_first_pass.{arm}"] = counts([first_label(r) for r in task])
        m[f"capture.answers_without_one_partition.{arm}"] = counts([lenient_label(r) for r in task])
    m["capture.C1_C1w_partial_one_partition"] = sum(r["final_answer"] == "partial" and bool(r["regrade"].get("one_partition_unhedged"))
                                                   for r in pick(grid, sids=["C1", "C1w"]))

    rec_sids = sorted({r["scenario"] for r in grid if r["scenario"].startswith("R")})
    for arm in ARMS:
        rs = pick(grid, arm)
        for sid in rec_sids:
            cell = pick(rs, sids=[sid])
            tgt = frac(cell, found_all) if SCEN[sid].get("target") else "—"
            m[f"recall.{sid}.{arm}"] = f"{tgt} · {counts([r['final_answer'] for r in cell])}"
        shelf = pick(rs, sids=["R1", "R2", "R3", "R4", "R8", "R9"])
        m[f"recall.shelf_target_retrieved.{arm}"] = frac(shelf, found_all)
        rec = [r for r in rs if r["scenario"].startswith("R")]
        m[f"recall.answers_c,p,u,w.{arm}"] = counts([r["final_answer"] for r in rec])
        m[f"recall.answers_first_pass.{arm}"] = counts([first_label(r) for r in rec])
        m[f"recall.answers_without_one_partition.{arm}"] = counts([lenient_label(r) for r in rec])
        m[f"recall.R2_R4_R8_shelf_read.{arm}"] = frac(pick(rs, sids=["R2", "R4", "R8"]), lambda r: r["mech"]["shelf_searches"] > 0)
    grid_rec = [r for r in grid if r["scenario"].startswith("R")]
    misses = [r for r in grid_rec if has_target(r) and not found_all(r)]
    m["recall.retrieval_misses"] = len(misses)
    m["recall.retrieval_misses_after_reading_the_shelf"] = sum(r["mech"]["shelf_searches"] > 0 and r["scenario"] != "R7" for r in misses)
    m["recall.runs_needing_memory_without_search"] = sum(r["scenario"] != "R6" and r["mech"]["searches"] == 0 for r in grid_rec)
    m["recall.R6_runs_with_search"] = sum(r["scenario"] == "R6" and r["mech"]["searches"] > 0 for r in grid_rec)
    s = [r["mech"]["searches"] for r in pick(grid, "cc-base", ["R2"])]
    m["recall.cc-base_R2_searches_per_run"] = f"{min(s)}–{max(s)}"
    both = [r for r in pick(grid, sids=["R1", "R2"]) if found_all(r)]
    m["recall.R1_R2_found_both/one_partition"] = f"{len(both)}/{sum(bool(r['regrade'].get('one_partition_unhedged')) for r in both)}"
    r8 = [r for r in pick(grid, sids=["R8"]) if found_all(r)]
    m["recall.R8_found/correct"] = f"{len(r8)}/{sum(r['final_answer'] == 'correct' for r in r8)}"
    m["recall.R4_candidate_corrected"] = frac([r for r in pick(grid, sids=["R4"]) if r["arm"].endswith("cand")],
                                              lambda r: r["mech"]["corrects"] > 0)
    for arm in ARMS:
        m[f"recall.R5_shelf_noise_shown.{arm}"] = sum(any(n.startswith("S") for n in r["mech"]["noise_retrieved"])
                                                      for r in pick(grid, arm, ["R5"]))
    m["recall.R7_cc-cand_shelf_searched"] = frac(pick(grid, "cc-cand", ["R7"]), lambda r: r["mech"]["shelf_searches"] > 0)
    m["recall.R9_partial/low_confidence"] = (f"{sum(r['final_answer'] == 'partial' for r in pick(grid, sids=['R9']))}/"
                                             f"{sum(r['final_answer'] == 'partial' and r in low for r in pick(grid, sids=['R9']))}")
    hinted = [r for r in rows if r["mech"]["hint_seen"]]
    m["recall.hint_runs"] = len(hinted)
    for order in ("followed", "before", "ignored"):
        rs = [r for r in hinted if r["mech"]["hint_order"] == order]
        cells = {}
        for r in rs:
            cells[f"{r['arm']} {r['scenario']}"] = cells.get(f"{r['arm']} {r['scenario']}", 0) + 1
        m[f"recall.hint_{order}"] = f"{len(rs)} (" + ", ".join(f"{k} ×{v}" for k, v in sorted(cells.items())) + ")"
    allshelf = [r for r in rows if r["arm"].startswith("hm") and r["mech"]["shelf_named_not_read"]]
    m["recall.hermes_all_shelf_searches/runs"] = f"{sum(r['mech']['shelf_named_not_read'] for r in allshelf)}/{len(allshelf)}"
    m["recall.hermes_all_shelf_runs"] = ", ".join(
        f"{r['run']} (search {','.join(map(str, r['mech']['shelf_named_not_read_at']))} of {r['mech']['searches']}"
        f"{', target found' if r['mech']['target_retrieved'] else ''})" for r in allshelf)

    for arm in ARMS:
        rs = sorted(pick(chain, arm), key=lambda r: r["rep"])
        m[f"chain.own_facts_stored_per_run.{arm}"] = ",".join(str(r["own"]["stored"]) for r in rs)
        m[f"chain.shelf_read.{arm}"] = frac(rs, lambda r: r["mech"]["shelf_searches"] > 0)
        m[f"chain.own_findings_returned.{arm}"] = (f"{frac(rs, lambda r: r['own']['returned'] > 0)} ("
                                                   + ",".join(f"{r['own']['returned']}/{r['own']['stored']}" for r in rs) + ")")
        m[f"chain.answers_c,p,u,w.{arm}"] = counts([r["final_answer"] for r in rs])
        m[f"chain.answers_first_pass.{arm}"] = counts([first_label(r) for r in rs])
        m[f"chain.shelf_named_not_read.{arm}"] = sum(r["mech"]["shelf_named_not_read"] > 0 for r in rs)

    for arm in ("cc-base", "cc-cand"):
        rs = pick(sonnet, arm)
        cw = pick(rs, sids=["C1w", "C2w"])
        new = [(r, c) for r in cw for c in r["changes"] if c["change"] == "new"]
        m[f"sonnet.C1w_C2w_items.{arm}"] = items(cw)
        m[f"sonnet.C1w_C2w_new/on_shelf/naming_source.{arm}"] = (
            f"{len(new)}/{sum(c['scope'].startswith('shelf:') for _, c in new)}/"
            f"{sum(facts(r).get(c['id'], {}).get('names_source') is True for r, c in new)}")
        m[f"sonnet.C1w_benchmark_as_claim.{arm}"] = frac(pick(cw, sids=["C1w"]), lambda r: cov(r).get("D3", False))
        r28 = pick(rs, sids=["R2", "R8"])
        m[f"sonnet.R2_R8_target.{arm}"] = frac(r28, found_all)
        m[f"sonnet.R2_R8_answers.{arm}"] = counts([r["final_answer"] for r in r28])
        m[f"sonnet.R2_R8_answers_first_pass.{arm}"] = counts([first_label(r) for r in r28])
        m[f"sonnet.C1w_C2w_answers.{arm}"] = counts([r["final_answer"] for r in cw])
    m["sonnet.cc-base_outside_items_opus_and_sonnet"] = items(
        pick([r for r in rows if not r["chain_from"]], "cc-base", ["C1", "C1w", "C2", "C2w"]))
    m["sonnet.cc-base_R2_R4_R8_shelf_read_opus_and_sonnet"] = frac(
        pick([r for r in rows if not r["chain_from"]], "cc-base", ["R2", "R4", "R8"]), lambda r: r["mech"]["shelf_searches"] > 0)

    cost = lambda rs: sum(r["cost_usd"] or 0 for r in rs)
    calls = lambda r: r["mech"]["searches"] + r["mech"]["adds"] + r["mech"]["corrects"] + r["mech"]["retires"]
    for arm in ARMS:
        rs = pick(grid, arm)
        rec = [r for r in rs if r["scenario"].startswith("R")]
        cap = [r for r in rs if r["scenario"].startswith("C")]
        m[f"cost.mean_recall/capture.{arm}"] = f"${cost(rec) / len(rec):.3f} / ${cost(cap) / len(cap):.3f}"
        m[f"cost.mean_all_{len(rs)}.{arm}"] = f"${cost(rs) / len(rs):.3f}"
        if arm.endswith("cand"):
            base = pick(grid, arm.replace("cand", "base"))
            m[f"cost.vs_base.{arm}"] = f"{(cost(rs) / len(rs)) / (cost(base) / len(base)) - 1:+.0%}"
        d = sorted(r["duration_ms"] or 0 for r in rs)
        m[f"cost.median_duration.{arm}"] = f"{(d[len(d) // 2] if len(d) % 2 else (d[len(d) // 2 - 1] + d[len(d) // 2]) / 2) / 1000:.1f} s"
        m[f"cost.memcue_calls_per_run_recall/capture.{arm}"] = (f"{sum(map(calls, rec)) / len(rec):.2f} / "
                                                               f"{sum(map(calls, cap)) / len(cap):.2f}")
    m["cost.total_all_runs"] = f"${cost(rows):.2f}"
    m["cost.total_opus_grid/sonnet/chain"] = f"${cost(grid):.2f} / ${cost(sonnet):.2f} / ${cost(chain):.2f}"
    m["cost.candidate_arms_opus_grid"] = f"${cost([r for r in grid if r['arm'].endswith('cand')]):.2f}"
    m["cost.candidate_text_bytes_cc_note/cc_skill/hermes"] = "/".join(
        f"{os.path.getsize(os.path.join(HERE, 'fixtures', f)):,}"
        for f in ("candidate-cc-note.md", "candidate-cc-skill.md", "candidate-hermes.md"))

    corr = [c for t in raw for c in json.loads(t)["fact_changes"] if c["change"] == "changed"]
    m["defects.cli_corrections_with_dashes"] = (f"{sum(c['content'].startswith('-- ') for c in corr)}/"
                                                f"{sum(r['mech']['cli_corrects'] for r in rows)}")

    mech = json.load(open(os.path.join(HERE, "results", "mechanics.json")))["rows"]
    hint = lambda x: any("on the shelf match" in h for h in (x.get("hints") or [x.get("hint") or ""]))
    for path in ("cli_repo", "cli_norepo", "hermes"):
        m[f"mechanics.{path}.hint_relevant/hint_none/relevant_no_hint"] = (
            f"{sum(hint(q[path]) and bool(RELEVANT[q['id']]) for q in mech)}/"
            f"{sum(hint(q[path]) and not RELEVANT[q['id']] for q in mech)}/"
            f"{sum(not hint(q[path]) and bool(RELEVANT[q['id']]) for q in mech)}")
        m[f"mechanics.{path}.non_empty"] = sum(bool(q[path]["facts"]) for q in mech)
    m["mechanics.simulated_hint_on_every_shelf_match"] = (
        f"{sum(bool(q['cli_repo_shelf']['facts']) and bool(RELEVANT[q['id']]) for q in mech)}/"
        f"{sum(bool(q['cli_repo_shelf']['facts']) and not RELEVANT[q['id']] for q in mech)}/"
        f"{sum(not q['cli_repo_shelf']['facts'] and bool(RELEVANT[q['id']]) for q in mech)}")
    m["mechanics.hermes_shelf_below_floor_hint"] = sum("below the score floor" in (q["hermes_shelf"].get("hint") or "") for q in mech)
    return m


# Numbers the report quotes, copied by hand as it writes them, against the summary key that recomputes each. `claims`
# compares these values only and never reads the report; it prints every disagreement and exits 1 if there is any.
# A number the summary cannot recompute is in NOT_INFERABLE instead.
CLAIMS = [
    ('runs.total', 232),
    ('runs.opus_grid', 204),
    ('runs.chain', 12),
    ('runs.sonnet', 16),
    ('runs.exit_nonzero', 0),
    ('runs.capture_read_by_second_pass', 104),
    ('runs.capture_unverified', 0),
    ('runs.answer_unverified', 0),
    ('runs.answers_over_1500_chars', 25),
    ('runs.anonymized_placeholders', '<STAND> 101, <REPO> 4, <real-hostname> 2'),
    ('runs.files_with_placeholder', 65),
    ('runs.answers_all_c,p,u,w', '156,58,17,1'),
    ('grading.manual_differs_from_rule', 29),
    ('grading.first_differs_from_second', 37),
    ('grading.final_differs_from_first', 48),
    ('grading.correct_to_partial', 47),
    ('grading.correct_to_wrong', 1),
    ('grading.changes_due_to_one_partition_alone', 38),
    ('grading.partial_only_for_one_partition', 38),
    ('grading.ruled_against_second', 35),
    ('grading.ruled_against_second_R5', 12),
    ('grading.ruled_against_second_one_partition_recall', 23),
    ('grading.second_low_confidence', 6),
    ('grading.second_low_confidence_among_changed', 6),
    ('grading.capture_items_first_vs_final_differ', 1),
    ('capture.C1.cc-base', '0/9'),
    ('capture.C1.cc-cand', '9/9'),
    ('capture.C1.hm-base', '4/9'),
    ('capture.C1.hm-cand', '9/9'),
    ('capture.C1w.cc-base', '0/9'),
    ('capture.C1w.cc-cand', '9/9'),
    ('capture.C1w.hm-base', '6/9'),
    ('capture.C1w.hm-cand', '9/9'),
    ('capture.C2.cc-base', '0/6'),
    ('capture.C2.cc-cand', '6/6'),
    ('capture.C2.hm-base', '1/6'),
    ('capture.C2.hm-cand', '6/6'),
    ('capture.C2w.cc-base', '0/6'),
    ('capture.C2w.cc-cand', '6/6'),
    ('capture.C2w.hm-base', '4/6'),
    ('capture.C2w.hm-cand', '6/6'),
    ('capture.outside_total.cc-base', '0/30'),
    ('capture.outside_total.cc-cand', '30/30'),
    ('capture.outside_total.hm-base', '15/30'),
    ('capture.outside_total.hm-cand', '30/30'),
    ('capture.runs_with_shelf_fact.cc-base', '0/12'),
    ('capture.runs_with_shelf_fact.cc-cand', '12/12'),
    ('capture.runs_with_shelf_fact.hm-base', '8/12'),
    ('capture.runs_with_shelf_fact.hm-cand', '12/12'),
    ('capture.C3_pitfall.cc-base', '0/3, on the shelf 0'),
    ('capture.C3_pitfall.cc-cand', '2/3, on the shelf 1'),
    ('capture.C3_pitfall.hm-base', '3/3, on the shelf 0'),
    ('capture.C3_pitfall.hm-cand', '3/3, on the shelf 0'),
    ('capture.new_facts.cc-base', 0),
    ('capture.new_facts.cc-cand', 49),
    ('capture.new_facts.hm-base', 11),
    ('capture.new_facts.hm-cand', 32),
    ('capture.new_on_shelf.cc-base', 0),
    ('capture.new_on_shelf.cc-cand', 48),
    ('capture.new_on_shelf.hm-base', 8),
    ('capture.new_on_shelf.hm-cand', 29),
    ('capture.shelf_naming_source.cc-base', '—'),
    ('capture.shelf_naming_source.cc-cand', '48/48'),
    ('capture.shelf_naming_source.hm-base', '7/8'),
    ('capture.shelf_naming_source.hm-cand', '29/29'),
    ('capture.extra.cc-base', 0),
    ('capture.extra.cc-cand', 17),
    ('capture.extra.hm-base', 0),
    ('capture.extra.hm-cand', 0),
    ('capture.vendor_benchmark_stored/as_claim.cc-base', '0 / —'),
    ('capture.vendor_benchmark_stored/as_claim.cc-cand', '6 / 6'),
    ('capture.vendor_benchmark_stored/as_claim.hm-base', '0 / —'),
    ('capture.vendor_benchmark_stored/as_claim.hm-cand', '6 / 6'),
    ('capture.forum_claim_as_fact.cc-base', 0),
    ('capture.forum_claim_as_fact.cc-cand', 0),
    ('capture.forum_claim_as_fact.hm-base', 0),
    ('capture.forum_claim_as_fact.hm-cand', 0),
    ('capture.choice_as_decision.cc-base', 0),
    ('capture.choice_as_decision.cc-cand', 0),
    ('capture.choice_as_decision.hm-base', 0),
    ('capture.choice_as_decision.hm-cand', 0),
    ('capture.new_facts_outside_C1_C3.cc-base', 0),
    ('capture.new_facts_outside_C1_C3.cc-cand', 0),
    ('capture.new_facts_outside_C1_C3.hm-base', 0),
    ('capture.new_facts_outside_C1_C3.hm-cand', 0),
    ('capture.C4.cc-base', 'duplicate 0/3, searched 0/3, shelf searched 0/3'),
    ('capture.C4.cc-cand', 'duplicate 0/3, searched 3/3, shelf searched 3/3'),
    ('capture.C4.hm-base', 'duplicate 0/3, searched 0/3, shelf searched 0/3'),
    ('capture.C4.hm-cand', 'duplicate 0/3, searched 3/3, shelf searched 3/3'),
    ('capture.C5.cc-base', "corrected 0/3, searched 0/3, with '-- ' 0"),
    ('capture.C5.cc-cand', "corrected 3/3, searched 3/3, with '-- ' 3"),
    ('capture.C5.hm-base', "corrected 0/3, searched 0/3, with '-- ' 0"),
    ('capture.C5.hm-cand', "corrected 3/3, searched 3/3, with '-- ' 0"),
    ('capture.C6_stored.cc-base', '0/3'),
    ('capture.C6_stored.cc-cand', '0/3'),
    ('capture.C6_stored.hm-base', '0/3'),
    ('capture.C6_stored.hm-cand', '0/3'),
    ('capture.answers_c,p,u,w.cc-base', '22,2,0,0'),
    ('capture.answers_c,p,u,w.cc-cand', '18,6,0,0'),
    ('capture.answers_c,p,u,w.hm-base', '19,5,0,0'),
    ('capture.answers_c,p,u,w.hm-cand', '22,1,0,1'),
    ('capture.answers_first_pass.cc-base', '24,0,0,0'),
    ('capture.answers_first_pass.cc-cand', '24,0,0,0'),
    ('capture.answers_first_pass.hm-base', '24,0,0,0'),
    ('capture.answers_first_pass.hm-cand', '24,0,0,0'),
    ('capture.answers_without_one_partition.cc-base', '24,0,0,0'),
    ('capture.answers_without_one_partition.cc-cand', '23,1,0,0'),
    ('capture.answers_without_one_partition.hm-base', '23,1,0,0'),
    ('capture.answers_without_one_partition.hm-cand', '23,0,0,1'),
    ('capture.C1_C1w_partial_one_partition', 12),
    ('recall.R1.cc-base', '3/3 · 0,3,0,0'),
    ('recall.R1.cc-cand', '3/3 · 1,2,0,0'),
    ('recall.R1.hm-base', '3/3 · 0,3,0,0'),
    ('recall.R1.hm-cand', '3/3 · 0,3,0,0'),
    ('recall.R2.cc-base', '0/3 · 0,0,3,0'),
    ('recall.R2.cc-cand', '3/3 · 0,3,0,0'),
    ('recall.R2.hm-base', '2/3 · 0,2,1,0'),
    ('recall.R2.hm-cand', '3/3 · 1,2,0,0'),
    ('recall.R3.cc-base', '3/3 · 3,0,0,0'),
    ('recall.R3.cc-cand', '3/3 · 3,0,0,0'),
    ('recall.R3.hm-base', '3/3 · 3,0,0,0'),
    ('recall.R3.hm-cand', '3/3 · 3,0,0,0'),
    ('recall.R4.cc-base', '0/3 · 3,0,0,0'),
    ('recall.R4.cc-cand', '3/3 · 3,0,0,0'),
    ('recall.R4.hm-base', '0/3 · 3,0,0,0'),
    ('recall.R4.hm-cand', '3/3 · 3,0,0,0'),
    ('recall.R5.cc-base', '— · 3,0,0,0'),
    ('recall.R5.cc-cand', '— · 3,0,0,0'),
    ('recall.R5.hm-base', '— · 3,0,0,0'),
    ('recall.R5.hm-cand', '— · 3,0,0,0'),
    ('recall.R6.cc-base', '— · 3,0,0,0'),
    ('recall.R6.cc-cand', '— · 3,0,0,0'),
    ('recall.R6.hm-base', '— · 3,0,0,0'),
    ('recall.R6.hm-cand', '— · 3,0,0,0'),
    ('recall.R7.cc-base', '3/3 · 3,0,0,0'),
    ('recall.R7.cc-cand', '3/3 · 2,1,0,0'),
    ('recall.R7.hm-base', '3/3 · 3,0,0,0'),
    ('recall.R7.hm-cand', '3/3 · 3,0,0,0'),
    ('recall.R8.cc-base', '0/3 · 0,0,3,0'),
    ('recall.R8.cc-cand', '3/3 · 1,2,0,0'),
    ('recall.R8.hm-base', '3/3 · 0,3,0,0'),
    ('recall.R8.hm-cand', '3/3 · 0,3,0,0'),
    ('recall.R9.cc-base', '3/3 · 3,0,0,0'),
    ('recall.R9.cc-cand', '3/3 · 2,1,0,0'),
    ('recall.R9.hm-base', '3/3 · 2,1,0,0'),
    ('recall.R9.hm-cand', '3/3 · 1,2,0,0'),
    ('recall.shelf_target_retrieved.cc-base', '9/18'),
    ('recall.shelf_target_retrieved.cc-cand', '18/18'),
    ('recall.shelf_target_retrieved.hm-base', '14/18'),
    ('recall.shelf_target_retrieved.hm-cand', '18/18'),
    ('recall.answers_c,p,u,w.cc-base', '18,3,6,0'),
    ('recall.answers_c,p,u,w.cc-cand', '18,9,0,0'),
    ('recall.answers_c,p,u,w.hm-base', '17,9,1,0'),
    ('recall.answers_c,p,u,w.hm-cand', '17,10,0,0'),
    ('recall.answers_first_pass.cc-base', '21,0,6,0'),
    ('recall.answers_first_pass.cc-cand', '25,2,0,0'),
    ('recall.answers_first_pass.hm-base', '23,3,1,0'),
    ('recall.answers_first_pass.hm-cand', '24,3,0,0'),
    ('recall.answers_without_one_partition.cc-base', '21,0,6,0'),
    ('recall.answers_without_one_partition.cc-cand', '23,4,0,0'),
    ('recall.answers_without_one_partition.hm-base', '22,4,1,0'),
    ('recall.answers_without_one_partition.hm-cand', '22,5,0,0'),
    ('recall.R2_R4_R8_shelf_read.cc-base', '0/9'),
    ('recall.R2_R4_R8_shelf_read.cc-cand', '9/9'),
    ('recall.R2_R4_R8_shelf_read.hm-base', '5/9'),
    ('recall.R2_R4_R8_shelf_read.hm-cand', '9/9'),
    ('recall.R5_shelf_noise_shown.cc-base', 1),
    ('recall.R5_shelf_noise_shown.cc-cand', 3),
    ('recall.R5_shelf_noise_shown.hm-base', 0),
    ('recall.R5_shelf_noise_shown.hm-cand', 3),
    ('recall.retrieval_misses', 13),
    ('recall.retrieval_misses_after_reading_the_shelf', 0),
    ('recall.runs_needing_memory_without_search', 0),
    ('recall.R6_runs_with_search', 0),
    ('recall.cc-base_R2_searches_per_run', '4–5'),
    ('recall.R1_R2_found_both/one_partition', '20/18'),
    ('recall.R8_found/correct', '9/1'),
    ('recall.R4_candidate_corrected', '6/6'),
    ('recall.R7_cc-cand_shelf_searched', '2/3'),
    ('recall.R9_partial/low_confidence', '4/4'),
    ('recall.hint_runs', 19),
    ('recall.hint_followed', '15 (cc-base R1 ×3, cc-base R3 ×3, cc-base R5 ×1, cc-base R9 ×3, cc-cand R7 ×2, hm-base R3 ×3)'),
    ('recall.hint_before', '4 (cc-cand C4 ×1, cc-cand R3 ×3)'),
    ('recall.hint_ignored', '0 ()'),
    ('recall.hermes_all_shelf_searches/runs', '6/6'),
    ('recall.hermes_all_shelf_runs', 'hm-base__R1__r2 (search 3 of 4, target found), hm-base__R2chain__r2 (search 3 of 3), hm-base__R8__r1 (search 1 of 2, target found), hm-base__R8__r2 (search 1 of 2, target found), hm-base__R8__r3 (search 1 of 3, target found), hm-cand__C3__r2 (search 2 of 2)'),
    ('chain.own_facts_stored_per_run.cc-base', '0,0,0'),
    ('chain.own_facts_stored_per_run.cc-cand', '6,7,7'),
    ('chain.own_facts_stored_per_run.hm-base', '1,1,1'),
    ('chain.own_facts_stored_per_run.hm-cand', '3,3,3'),
    ('chain.shelf_read.cc-base', '0/3'),
    ('chain.shelf_read.cc-cand', '3/3'),
    ('chain.shelf_read.hm-base', '0/3'),
    ('chain.shelf_read.hm-cand', '3/3'),
    ('chain.own_findings_returned.cc-base', '0/3 (0/0,0/0,0/0)'),
    ('chain.own_findings_returned.cc-cand', '3/3 (6/6,7/7,7/7)'),
    ('chain.own_findings_returned.hm-base', '0/3 (0/1,0/1,0/1)'),
    ('chain.own_findings_returned.hm-cand', '3/3 (2/3,3/3,3/3)'),
    ('chain.answers_c,p,u,w.cc-base', '0,0,3,0'),
    ('chain.answers_c,p,u,w.cc-cand', '0,3,0,0'),
    ('chain.answers_c,p,u,w.hm-base', '0,0,3,0'),
    ('chain.answers_c,p,u,w.hm-cand', '0,3,0,0'),
    ('chain.answers_first_pass.cc-base', '0,0,3,0'),
    ('chain.answers_first_pass.cc-cand', '3,0,0,0'),
    ('chain.answers_first_pass.hm-base', '0,0,3,0'),
    ('chain.answers_first_pass.hm-cand', '2,1,0,0'),
    ('chain.shelf_named_not_read.cc-base', 0),
    ('chain.shelf_named_not_read.cc-cand', 0),
    ('chain.shelf_named_not_read.hm-base', 1),
    ('chain.shelf_named_not_read.hm-cand', 0),
    ('sonnet.C1w_C2w_items.cc-base', '0/10'),
    ('sonnet.C1w_C2w_items.cc-cand', '10/10'),
    ('sonnet.C1w_C2w_new/on_shelf/naming_source.cc-base', '0/0/0'),
    ('sonnet.C1w_C2w_new/on_shelf/naming_source.cc-cand', '10/10/10'),
    ('sonnet.C1w_benchmark_as_claim.cc-base', '0/2'),
    ('sonnet.C1w_benchmark_as_claim.cc-cand', '2/2'),
    ('sonnet.R2_R8_target.cc-base', '0/4'),
    ('sonnet.R2_R8_target.cc-cand', '4/4'),
    ('sonnet.R2_R8_answers.cc-base', '0,0,4,0'),
    ('sonnet.R2_R8_answers.cc-cand', '0,4,0,0'),
    ('sonnet.R2_R8_answers_first_pass.cc-base', '0,0,4,0'),
    ('sonnet.R2_R8_answers_first_pass.cc-cand', '2,2,0,0'),
    ('sonnet.C1w_C2w_answers.cc-base', '3,1,0,0'),
    ('sonnet.C1w_C2w_answers.cc-cand', '2,2,0,0'),
    ('sonnet.cc-base_outside_items_opus_and_sonnet', '0/40'),
    ('sonnet.cc-base_R2_R4_R8_shelf_read_opus_and_sonnet', '0/13'),
    ('cost.mean_recall/capture.cc-base', '$0.060 / $0.067'),
    ('cost.mean_recall/capture.cc-cand', '$0.064 / $0.083'),
    ('cost.mean_recall/capture.hm-base', '$0.033 / $0.045'),
    ('cost.mean_recall/capture.hm-cand', '$0.031 / $0.053'),
    ('cost.mean_all_51.cc-base', '$0.063'),
    ('cost.mean_all_51.cc-cand', '$0.073'),
    ('cost.mean_all_51.hm-base', '$0.039'),
    ('cost.mean_all_51.hm-cand', '$0.041'),
    ('cost.median_duration.cc-base', '10.7 s'),
    ('cost.median_duration.cc-cand', '11.3 s'),
    ('cost.median_duration.hm-base', '10.6 s'),
    ('cost.median_duration.hm-cand', '10.2 s'),
    ('cost.memcue_calls_per_run_recall/capture.cc-base', '3.70 / 1.00'),
    ('cost.memcue_calls_per_run_recall/capture.cc-cand', '2.74 / 3.79'),
    ('cost.memcue_calls_per_run_recall/capture.hm-base', '1.93 / 1.08'),
    ('cost.memcue_calls_per_run_recall/capture.hm-cand', '1.63 / 2.38'),
    ('cost.vs_base.cc-cand', '+15%'),
    ('cost.vs_base.hm-cand', '+6%'),
    ('cost.total_all_runs', '$12.33'),
    ('cost.total_opus_grid/sonnet/chain', '$11.03 / $0.57 / $0.74'),
    ('cost.candidate_arms_opus_grid', '$5.82'),
    ('cost.candidate_text_bytes_cc_note/cc_skill/hermes', '1,154/1,032/1,070'),
    ('defects.cli_corrections_with_dashes', '4/6'),
    ('mechanics.cli_repo.hint_relevant/hint_none/relevant_no_hint', '5/2/3'),
    ('mechanics.cli_repo.non_empty', 3),
    ('mechanics.cli_norepo.hint_relevant/hint_none/relevant_no_hint', '2/0/6'),
    ('mechanics.cli_norepo.non_empty', 8),
    ('mechanics.hermes.hint_relevant/hint_none/relevant_no_hint', '2/0/6'),
    ('mechanics.hermes.non_empty', 8),
    ('mechanics.simulated_hint_on_every_shelf_match', '8/2/0'),
    ('mechanics.hermes_shelf_below_floor_hint', 3),
]


if __name__ == "__main__":
    main()
