"""Replay saved Flash holdout decisions with native dream -apply in isolated SQLite copies."""

import argparse
from contextlib import closing
import hashlib
import json
from pathlib import Path
import shutil
import sqlite3
import tempfile

import flash_holdout
import pipeline
import pipeline_native
import replay


def read_json(path):
    return json.loads(path.read_text(encoding="utf-8"))


def snapshot(root, native_to_fixture):
    with closing(sqlite3.connect((root / "fixture.db").as_uri() + "?mode=ro", uri=True)) as db:
        rows = db.execute("""
            SELECT id, version, status, content, scope, kind, source
            FROM facts f
            WHERE version = (SELECT max(version) FROM facts WHERE id = f.id)
        """).fetchall()
    fields = ("version", "status", "content", "scope", "kind", "source")
    return {native_to_fixture.get(id_, id_): dict(zip(fields, values)) for id_, *values in rows}


def run(binary, root, native_to_fixture, actions, *, enroll_noise=False, update_noise=False):
    fixture_to_native = {v: k for k, v in native_to_fixture.items()}
    packet_path = root / "state" / replay.PACKET
    packet = read_json(packet_path)
    noise_id = fixture_to_native["h13"]
    assert not any(noise_id in group for group in packet["groups"])
    assert noise_id not in (packet.get("noise") or {})
    if enroll_noise:
        version = packet["split"][noise_id]
        packet["groups"].append({noise_id: version})
        packet["noise"] = {**(packet.get("noise") or {}), noise_id: version}
        packet_path.write_text(json.dumps(packet), encoding="utf-8")
    if update_noise:
        replay.native(binary, root, "correct", noise_id,
                      "The saffron menu preview requires the high-contrast palette to check focus indicators.")

    before = snapshot(root, native_to_fixture)
    answer = {"packet": packet["id"], "actions": [], "splits": [], "scopes": []}
    for action in actions:
        converted = {**action, "id": fixture_to_native[action["id"]], "reason": "Saved holdout decision"}
        if action.get("by"):
            converted["by"] = fixture_to_native[action["by"]]
        answer["actions"].append(converted)
    answer_path = root / "answer.json"
    answer_path.write_text(json.dumps(answer), encoding="utf-8")
    stdout = replay.native(binary, root, "dream", "-apply", str(answer_path))
    record = json.loads((root / "state" / "dream.log").read_text(encoding="utf-8").splitlines()[-1])
    applied = [
        {**action, "id": native_to_fixture[action["id"]],
         "by": native_to_fixture[action["by"]] if action.get("by") else ""}
        for action in record.get("actions", [])
    ]
    refused = record.get("refused", [])
    refused = [line.replace(noise_id, "h13") for line in refused]
    after = snapshot(root, native_to_fixture)
    return {"stdout": stdout, "applied": applied, "refused": refused, "before": before, "after": after}


def check_state(result, expected, refusal=None):
    evaluation = pipeline.evaluate(expected, result["applied"])
    assert not evaluation["missed"] and not evaluation["unexpected"], evaluation
    assert result["refused"] == ([] if refusal is None else [refusal]), result["refused"]
    next_state = {id_: fact.copy() for id_, fact in result["before"].items()}
    for action in result["applied"]:
        fact = next_state[action["id"]]
        fact["version"] += 1
        if action["op"] == "retire":
            fact["status"] = "retired"
        else:
            fact["content"] = action["content"]
    assert result["after"] == next_state, result["after"]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--results", type=Path, required=True, help="saved flash_holdout.py output directory")
    parser.add_argument("--out", type=Path, required=True, help="new directory for the offline report")
    args = parser.parse_args()
    binary = str(args.binary.resolve(strict=True))
    fixture = flash_holdout.FIXTURE
    cases = pipeline.read_cases(fixture)
    packed, confirmed = (read_json(args.results / name) for name in ("packed.json", "report.json"))
    fixture_sha = hashlib.sha256(fixture.read_bytes()).hexdigest()
    if confirmed["fixture_sha256"] != fixture_sha:
        raise ValueError("saved report refers to a different fixture")
    actions_by_order = {
        item["order"]: item["actions"] + packed[item["order"]]["fallback"]["actions"]
        for item in confirmed["variants"]
    }
    actions = actions_by_order["original"]
    if actions_by_order["reversed"] != actions:
        raise ValueError("saved orders produced different action lists")
    gold = [action for case in cases for action in case["expected"]]
    no_noise = [action for action in gold if action["id"] != "h13"]
    facts = [fact for case in cases for fact in case["facts"]]

    args.out.mkdir(mode=0o700, exist_ok=False)
    with tempfile.TemporaryDirectory(prefix="memcue-apply-holdout-") as tmp:
        base = Path(tmp) / "base"
        base.mkdir()
        native_to_fixture = pipeline_native._import_fixture(facts, binary, base)
        replay.native(binary, base, "dream", "-prepare", "-all")
        results = {}
        for label, options in (
            ("native_packet", {}),
            ("noise_enrolled", {"enroll_noise": True}),
            ("noise_changed", {"enroll_noise": True, "update_noise": True}),
        ):
            root = base.parent / label
            shutil.copytree(base, root)
            results[label] = run(binary, root, native_to_fixture, actions, **options)

    (args.out / "report.json").write_text(json.dumps(results, indent=2) + "\n", encoding="utf-8")
    check_state(results["native_packet"], no_noise, "retire h13: not a fact of the groups")
    check_state(results["noise_enrolled"], gold)
    check_state(results["noise_changed"], no_noise, "retire h13: group changed since the packet (h13)")
    assert results["noise_changed"]["before"]["h13"]["version"] == results["native_packet"]["before"]["h13"]["version"] + 1


if __name__ == "__main__":
    main()
