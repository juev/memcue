"""Every count the #531 confirming report quotes, recomputed from the kept records and grades.

Run: python3 -I summary.py RECORDS GRADES LABELS [--json]. The final label of a record is pass A where A and B agree;
where they differ, each field takes the majority of A, B and C, and a field all three answer differently is
`disputed`. A grade is used only while its record_sha256 matches the record. Prints Markdown tables by default.
"""

import hashlib
import json
import sys
from collections import Counter, defaultdict
from pathlib import Path

ARMS = ("cc-base", "cc-cand", "hm-base", "hm-cand")
ANSWERS = ("correct", "partial", "wrong", "unresolved", "disputed")
VERDICTS = ("expected", "extra_useful", "extra_noise", "forbidden", "disputed")


def record_hash(record: dict) -> str:
    return hashlib.sha256(json.dumps(record, sort_keys=True, ensure_ascii=False).encode()).hexdigest()


def vote(values: list):
    if not values:
        return "disputed"
    value, n = Counter(json.dumps(v, sort_keys=True) for v in values).most_common(1)[0]
    return json.loads(value) if n * 2 > len(values) else "disputed"


def final(grades: list[dict]) -> dict:
    """One label from the valid passes: their agreement, or the majority per field."""
    if len(grades) == 1:
        return grades[0]
    out = {f: vote([g[f] for g in grades]) for f in ("answer", "searched_memory", "named_shelf", "needed_fact_found")}
    keys = [c["key"] for c in grades[0]["capture"]]
    out["capture"] = []
    for i, k in enumerate(keys):
        v = vote([[g["capture"][i]["stored"], g["capture"][i]["place"]] for g in grades])
        out["capture"].append({"key": k, "stored": v[0] if v != "disputed" else "disputed",
                               "place": v[1] if v != "disputed" else "disputed"})
    out["changes"] = [{"id": c["id"], "verdict": vote([g["changes"][i]["verdict"] for g in grades])}
                      for i, c in enumerate(grades[0]["changes"])]
    return out


def load(records: Path, grades: Path) -> tuple[list[dict], Counter]:
    rows, status = [], Counter()
    for path in sorted(records.glob("*.json")):
        doc = json.loads(path.read_text(encoding="utf-8"))
        digest = record_hash(doc["record"])
        passes = {}
        for p in ("A", "B", "C"):
            gp = grades / p / path.name
            if gp.is_file():
                g = json.loads(gp.read_text(encoding="utf-8"))
                if g.get("record_sha256") == digest and "grade" in g:
                    passes[p] = g["grade"]
        if "A" in passes and "B" in passes and passes["A"] == passes["B"]:
            used = [passes["A"]]
        else:
            used = [passes[p] for p in ("A", "B", "C") if p in passes]
        if not used:
            status["ungraded"] += 1
            continue
        status["graded"] += 1
        status["third pass" if "C" in passes else "two passes"] += 1
        rows.append({"meta": doc["meta"], "record": doc["record"], "label": final(used)})
    return rows, status


def pct(n: int, d: int) -> str:
    return f"{n}/{d}" if d else "—"


def main() -> int:
    records, grades, labels_path = Path(sys.argv[1]), Path(sys.argv[2]), Path(sys.argv[3])
    as_json = "--json" in sys.argv[4:]
    labels = json.loads(labels_path.read_text(encoding="utf-8"))
    kinds = {x["id"]: x["kind"] for x in json.loads(
        (labels_path.parent / "scenarios.json").read_text(encoding="utf-8"))["scenarios"]}
    rows, status = load(records, grades)
    m: dict = defaultdict(lambda: defaultdict(int))
    per_scenario: dict = defaultdict(lambda: defaultdict(Counter))
    for r in rows:
        arm, sid = r["meta"]["arm"], r["meta"]["scenario"]
        lab, g = labels[sid], r["label"]
        a = m[arm]
        group = "chain" if r["meta"].get("chain_from") else kinds[sid]
        a["runs"] += 1
        a["seconds"] += float(r["meta"].get("seconds") or 0)
        a["cost_usd"] += float(r["meta"].get("cost_usd") or 0)
        a["nonzero_exit"] += r["meta"].get("exit") not in ("0", 0)
        for c in g["capture"]:
            want = next(x["place"] for x in lab["expected_capture"] if x["key"] == c["key"])
            a["items"] += 1
            a[f"items_{want}"] += 1
            if c["stored"] == "disputed":
                a["items_disputed"] += 1
            elif c["stored"]:
                a["items_stored"] += 1
                a[f"items_{want}_stored_{c['place']}"] += 1
        for c in g["changes"]:
            a[f"change_{c['verdict']}"] += 1
        a["runs_with_forbidden"] += any(c["verdict"] == "forbidden" for c in g["changes"])
        if not lab["expected_capture"]:
            a["no_capture_runs"] += 1
            a["no_capture_runs_that_stored"] += bool(r["record"]["base_changes"])
        a["searched"] += g["searched_memory"] is True
        if lab["shelf_needed"]:
            a["shelf_needed_runs"] += 1
            a["shelf_needed_named"] += g["named_shelf"] is True
        if g["needed_fact_found"] in ("yes", "no"):
            a["needed_applicable"] += 1
            a["needed_found"] += g["needed_fact_found"] == "yes"
        a[f"answer_{g['answer']}"] += 1
        a[f"{group}_answer_{g['answer']}"] += 1
        a[f"{group}_runs"] += 1
        per_scenario[sid][arm][g["answer"]] += 1
        per_scenario[sid][arm]["stored"] += sum(1 for c in g["capture"] if c["stored"] is True)
        per_scenario[sid][arm]["items"] += len(g["capture"])
        per_scenario[sid][arm]["shelf"] += g["named_shelf"] is True

    if as_json:
        print(json.dumps({"status": status, "arms": {k: dict(v) for k, v in m.items()},
                          "per_scenario": {s: {a: dict(c) for a, c in v.items()} for s, v in per_scenario.items()}},
                         indent=1, sort_keys=True))
        return 0
    print(f"records: {dict(status)}\n")
    print("| | " + " | ".join(ARMS) + " |\n|---|" + "---|" * len(ARMS))
    rows_out = [
        ("runs", lambda a: str(a["runs"])),
        ("expected items stored", lambda a: pct(a["items_stored"], a["items"])),
        ("shelf items stored on the shelf", lambda a: pct(a["items_shelf_stored_shelf"], a["items_shelf"])),
        ("shelf items stored elsewhere", lambda a: pct(a["items_shelf_stored_ordinary"], a["items_shelf"])),
        ("ordinary items stored in place", lambda a: pct(a["items_ordinary_stored_ordinary"], a["items_ordinary"])),
        ("ordinary items put on the shelf", lambda a: pct(a["items_ordinary_stored_shelf"], a["items_ordinary"])),
        ("changes: expected", lambda a: str(a["change_expected"])),
        ("changes: extra useful", lambda a: str(a["change_extra_useful"])),
        ("changes: extra noise", lambda a: str(a["change_extra_noise"])),
        ("changes: forbidden", lambda a: str(a["change_forbidden"])),
        ("runs with a forbidden change", lambda a: str(a["runs_with_forbidden"])),
        ("runs with nothing to store that stored", lambda a: pct(a["no_capture_runs_that_stored"], a["no_capture_runs"])),
        ("searched memory", lambda a: pct(a["searched"], a["runs"])),
        ("named the shelf when needed", lambda a: pct(a["shelf_needed_named"], a["shelf_needed_runs"])),
        ("needed fact found", lambda a: pct(a["needed_found"], a["needed_applicable"])),
        *[(f"answers {x}", (lambda x: lambda a: str(a[f"answer_{x}"]))(x)) for x in ANSWERS],
        *[(f"{grp} answers {x}", (lambda grp, x: lambda a: str(a[f"{grp}_answer_{x}"]))(grp, x))
          for grp in ("capture", "recall", "chain") for x in ANSWERS[:4]],
        ("mean seconds per run", lambda a: f"{a['seconds'] / a['runs']:.0f}" if a["runs"] else "—"),
        ("cost, $ (API prices)", lambda a: f"{a['cost_usd']:.2f}"),
        ("runs with a nonzero exit", lambda a: str(a["nonzero_exit"])),
    ]
    for name, f in rows_out:
        print(f"| {name} | " + " | ".join(f(m[arm]) for arm in ARMS) + " |")
    print("\nPer scenario: answers correct/partial/wrong/unresolved, expected items stored, runs that named the shelf\n")
    print("| scenario | " + " | ".join(ARMS) + " |\n|---|" + "---|" * len(ARMS))
    for sid in sorted(per_scenario):
        cells = []
        for arm in ARMS:
            c = per_scenario[sid][arm]
            cells.append(f"{c['correct']}/{c['partial']}/{c['wrong']}/{c['unresolved']}"
                         f"{'+' + str(c['disputed']) + 'd' if c['disputed'] else ''}"
                         f"; {c['stored']}/{c['items']}; {c['shelf']}")
        print(f"| {sid} | " + " | ".join(cells) + " |")
    return 0


if __name__ == "__main__":
    sys.exit(main())
