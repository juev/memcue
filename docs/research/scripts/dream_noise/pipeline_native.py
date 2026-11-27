"""Compare native dream with a classifier-prefiltered native dream on a fixture."""

from contextlib import closing
import json
from pathlib import Path
import re
import shutil
import sqlite3
import tempfile

import replay


MAX_OUTPUT = 18_000


def _import_fixture(facts, binary, root):
    fixture = root / "fixture.json"
    fixture.write_text(json.dumps({
        "origin": "synthetic dream benchmark",
        "facts": [{key: fact[key] for key in ("content", "source", "kind", "scope")}
                  for fact in facts],
    }, ensure_ascii=False), encoding="utf-8")
    replay.native(binary, root, "import", str(fixture))
    with closing(sqlite3.connect((root / "fixture.db").as_uri() + "?mode=ro", uri=True)) as db:
        rows = db.execute("SELECT id, content, source, kind, scope FROM facts WHERE status = 'active' ORDER BY id").fetchall()
    # Duplicate contents remain distinguishable through all imported fields and acquisition order.
    remaining = list(facts)
    native_to_fixture = {}
    for native_id, content, source, kind, scope in rows:
        match = next((fact for fact in remaining if fact["content"] == content
                      and fact["source"] == source and fact["kind"] == kind and fact["scope"] == scope), None)
        if match is None:
            raise ValueError("native import changed or duplicated fixture facts")
        remaining.remove(match)
        native_to_fixture[native_id] = match["id"]
    if remaining or len(native_to_fixture) != len(facts):
        raise ValueError("native import did not preserve every fixture fact")
    return native_to_fixture


def _groups(packet, id_map):
    return [[id_map[native_id] for native_id in sorted(group)] for group in packet["groups"]]


def _selected_groups(escalate, groups):
    selected_ids = set()
    for item in escalate:
        ids = item if isinstance(item, list) else [item]
        containing = next((group for group in groups if set(ids) <= set(group)), None)
        if containing is None or not ids:
            raise ValueError(f"classifier escalated IDs outside a native group: {item!r}")
        selected_ids.update(ids)
    return [[fact_id for fact_id in group if fact_id in selected_ids] for group in groups
            if any(fact_id in selected_ids for fact_id in group)]


def _filtered_text(text, selected_ids):
    head, *blocks = re.split(r"^(## (?:Group|Noise candidate) \d+\n\n)", text, flags=re.MULTILINE)
    selected = []
    seen = set()
    for heading, block in zip(blocks[::2], blocks[1::2]):
        facts = []
        for fact in re.finditer(r"^- id (\S+) \(origin:[^\n]*\n  [^\n]*\n", block, flags=re.MULTILINE):
            if fact[1] in selected_ids:
                seen.add(fact[1])
                facts.append(fact[0])
        if facts:
            selected.append(heading + "".join(facts) + "\n")
    if seen != selected_ids:
        raise ValueError("native prompt and fallback packet contain different facts")
    return replay.without_splitting(head + "".join(selected))


def _run(binary, root, name, ask, packet, body, id_map, permitted=None):
    request = {
        "model": "deepseek-flash",
        "messages": [
            {"role": "system", "content": replay.DREAM_SYSTEM},
            {"role": "user", "content": body},
        ],
        "response_format": {"type": "json_object"},
    }
    answer = replay.dream_answer(ask(name, request, MAX_OUTPUT), packet["id"])
    if answer is None:
        return {"error": "missing, incomplete or invalid dream response"}
    if permitted is not None:
        answer["splits"] = []
        answer["scopes"] = []
        allowed = {native_id for group in permitted for native_id in group}
        if any(action.get("id") not in allowed or (action.get("by") and action["by"] not in allowed)
               for action in answer["actions"]):
            return {"error": "fallback answer referenced a fact outside escalated groups"}
    answer_path = root / "answer.json"
    answer_path.write_text(json.dumps(answer, ensure_ascii=False), encoding="utf-8")
    try:
        replay.native(binary, root, "dream", "-apply", str(answer_path))
    except Exception as error:
        return {"error": "native apply rejected the answer", "diagnostic": str(error)}
    log_path = root / "state" / "dream.log"
    record = json.loads(log_path.read_text(encoding="utf-8").splitlines()[-1])
    actions = []
    for action in record.get("actions", []):
        if permitted is not None and action["id"] not in {key for group in permitted for key in group}:
            continue
        actions.append({"op": action["op"], "id": id_map[action["id"]],
                        "kind": action.get("kind", ""),
                        "by": id_map[action["by"]] if action.get("by") else "",
                        "content": " ".join(action.get("content", "").split())})
    return {"actions": actions, "refused": len(record.get("refused", [])),
            "scope_changes": len(record.get("rescoped", []))}


def compare_native(facts, binary, classify, ask, previous=None, judge="jev"):
    """Run a full control, classify its native groups, then replay only escalated groups."""
    binary = str(Path(binary).resolve(strict=True))
    if not facts or len({fact["id"] for fact in facts}) != len(facts):
        raise ValueError("fixture facts must be non-empty and have unique fixture IDs")
    with tempfile.TemporaryDirectory(prefix="memcue-native-pipeline-") as directory:
        root = Path(directory)
        full = root / "full"
        full.mkdir()
        id_map = _import_fixture(facts, binary, full)
        full_text = replay.native(binary, full, "dream", "-prepare", "-all")
        packet_path = full / "state" / replay.PACKET
        packet = json.loads(packet_path.read_text(encoding="utf-8"))
        groups = _groups(packet, id_map)
        if previous is not None and (groups != previous["groups"] or "error" in previous["full"]):
            raise ValueError("cannot reuse a failed baseline or one with different native groups")
        classification = classify(groups)
        selected = _selected_groups(classification.get("escalate", []), groups)

        if previous is None:
            full_copy = root / "full-control"
            shutil.copytree(full, full_copy)
            full_packet = dict(packet, split={})
            (full_copy / "state" / replay.PACKET).write_text(json.dumps(full_packet), encoding="utf-8")
            full_result = _run(binary, full_copy, "native-full", ask, full_packet,
                               replay.without_splitting(full_text), id_map)
        else:
            full_result = previous["full"]

        fallback = root / "fallback"
        shutil.copytree(full, fallback)
        native_groups = []
        native_noise = {}
        selected_ids = {fixture_id for group in selected for fixture_id in group}
        for group in packet["groups"]:
            filtered = {key: value for key, value in group.items() if id_map[key] in selected_ids}
            if filtered:
                native_groups.append(filtered)
                native_noise.update({key: value for key, value in packet.get("noise", {}).items() if key in filtered})
        if native_groups:
            fallback_packet = dict(packet, split={}, groups=native_groups, noise=native_noise, scope={})
            (fallback / "state" / replay.PACKET).write_text(json.dumps(fallback_packet), encoding="utf-8")
            fallback_text = _filtered_text(full_text, {key for group in native_groups for key in group})
            fallback_result = _run(binary, fallback, "native-fallback", ask, fallback_packet,
                                   fallback_text, id_map, native_groups)
        else:
            fallback_result = None
    return {"fixture_facts": len(facts), "groups": groups,
            "escalated_groups": selected, "full": full_result,
            "fallback": fallback_result, "judge": judge, judge: classification}
