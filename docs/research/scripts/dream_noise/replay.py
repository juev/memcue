"""Replay one native dream packet on two independent copies of the synthetic base."""

from contextlib import closing
import json
import os
from pathlib import Path
import shutil
import sqlite3
import subprocess
import tempfile


# Keep this identical to dreamRunSystem in internal/extract/dreamrun.go.
DREAM_SYSTEM = (
    "You are the memcue dream model. The user message is one complete work packet: instructions, "
    "tasks, and data. Complete it and return exactly the JSON object described in the 'Answer format' section, "
    "with no explanation or markdown."
)
SPLIT_SECTION = "\n## Data: splitting\n\n```json\n"
SCOPE_SECTION = "\n```\n\n## Data: scope\n"
PACKET = "dream.packet.json"

# Each anchor is a conjunction of literal terms. The content check keeps a
# correct replacement or split part eligible even when the original ID retires.
RECALL = (
    ("read-only worker cache EACCES", ("eacces", "read-only")),
    ("parser timeout final newline", ("timeout", "newline")),
    ("exporter UTF-8 LF line endings", ("utf-8", "lf")),
    ("worker configuration worker.toml SAMPLE_HOME", ("worker.toml", "sample_home")),
    ("proxy idle connection 30 seconds EOF", ("30 seconds", "eof")),
    ("failed incremental sync stored cursor acknowledged event", ("cursor", "acknowledged")),
)


def native(binary, root, *args, model_env=None, timeout=45):
    env = {
        "PATH": os.environ.get("PATH", os.defpath),
        "HOME": str(root),
        "XDG_CONFIG_HOME": str(root / "config"),
        "XDG_STATE_HOME": str(root / "xdg-state"),
        "XDG_CACHE_HOME": str(root / "cache"),
        "CLAUDE_CONFIG_DIR": str(root / "claude-config"),
        "MEMCUE_FACTS": str(root / "fixture.db"),
        "MEMCUE_STATE_DIR": str(root / "state"),
        "MEMCUE_DREAM_BACKGROUND": "0",
    }
    if model_env:
        env.update(model_env)
    return subprocess.run(
        [binary, *args], cwd=root, env=env, check=True, capture_output=True,
        encoding="utf-8", timeout=timeout,
    ).stdout


def without_splitting(packet_text):
    before, split, after = packet_text.partition(SPLIT_SECTION)
    _, scope, remaining = after.partition(SCOPE_SECTION)
    if not split or not scope:
        raise ValueError("cannot identify the native split data section")
    return before + split + '{"facts":null}' + scope + remaining


def dream_answer(response, packet_id):
    if response is None:
        return None
    try:
        choice = response["choices"][0]
        if choice["finish_reason"] != "stop":
            return None
        answer = json.loads(choice["message"]["content"])
        if answer["packet"] == packet_id and all(
            isinstance(answer[key], list) for key in ("splits", "actions", "scopes")
        ):
            return answer
    except (KeyError, IndexError, TypeError, ValueError):
        return None
    return None


def run_arm(binary, root, name, ask, packet, text, fact_ids):
    response = ask(name, {
        "model": "deepseek-flash",
        "messages": [
            {"role": "system", "content": DREAM_SYSTEM},
            {"role": "user", "content": text},
        ],
        "response_format": {"type": "json_object"},
    }, 28_000)
    answer = dream_answer(response, packet["id"])
    if answer is None:
        return {"error": "missing, incomplete or invalid dream response"}

    answer_path = root / "answer.json"
    answer_path.write_text(json.dumps(answer, ensure_ascii=False), encoding="utf-8")
    try:
        native(binary, root, "dream", "-apply", str(answer_path))
    except subprocess.CalledProcessError as error:
        return {"error": "native apply rejected the answer", "diagnostic": error.stderr.strip()}

    log_path = root / "state" / "dream.log"
    record = json.loads(log_path.read_text(encoding="utf-8").splitlines()[-1])
    return {
        "splits": len(record.get("splits", [])),
        "parts": sum(len(split["parts"]) for split in record.get("splits", [])),
        "actions": [
            {
                "op": action["op"],
                "id": fact_ids[action["id"]],
                "kind": action.get("kind", ""),
                "by": fact_ids[action["by"]] if action.get("by") else "",
                "content": " ".join(action.get("content", "").split()),
            }
            for action in record.get("actions", [])
        ],
        "refused": len(record.get("refused", [])),
        "scope_changes": len(record.get("rescoped", [])),
        "recall": recall(binary, root),
    }


def recall(binary, root):
    result = {}
    for query, terms in RECALL:
        hits = json.loads(native(binary, root, "search", "-in", "all", "-n", "5", "-json", query))["facts"]
        contents = [hit["content"] for hit in hits]
        rank = next((i for i, content in enumerate(contents, 1)
                     if all(term in content.casefold() for term in terms)), None)
        result[query] = {"rank": rank, "hits": contents}
    return result


def replay(facts, binary, ask):
    binary = str(Path(binary).resolve(strict=True))
    by_content = {row["content"]: row["id"] for row in facts}
    if len(facts) != 60 or len(by_content) != len(facts):
        raise ValueError("replay needs 60 distinct synthetic facts")

    with tempfile.TemporaryDirectory(prefix="memcue-dream-replay-") as directory:
        root = Path(directory)
        full = root / "full"
        full.mkdir()
        fixture_path = full / "fixture.json"
        fixture_path.write_text(json.dumps({
            "origin": "synthetic dream benchmark",
            "facts": [{k: fact[k] for k in ("content", "source", "kind", "scope")} for fact in facts],
        }, ensure_ascii=False), encoding="utf-8")
        native(binary, full, "import", str(fixture_path))
        with closing(sqlite3.connect((full / "fixture.db").as_uri() + "?mode=ro", uri=True)) as db:
            imported = dict(db.execute("SELECT id, content FROM facts WHERE status = 'active'"))
        if len(imported) != 60 or set(imported.values()) != set(by_content):
            raise ValueError("native import did not preserve every synthetic record")
        fact_ids = {key: by_content[content] for key, content in imported.items()}
        before = recall(binary, full)
        if any(row["rank"] is None for row in before.values()):
            raise ValueError("fixture search did not find every predeclared control")

        full_text = native(binary, full, "dream", "-prepare", "-all")
        full_packet = json.loads((full / "state" / PACKET).read_text(encoding="utf-8"))
        if not full_packet.get("split") or not full_packet.get("groups"):
            raise ValueError("native packet must include split and group work")
        no_split = root / "no-split"
        shutil.copytree(full, no_split)
        no_split_packet = dict(full_packet, split={})
        (no_split / "state" / PACKET).write_text(json.dumps(no_split_packet), encoding="utf-8")

        arms = {
            "full": run_arm(binary, full, "replay-full", ask, full_packet, full_text, fact_ids),
            "no_split": run_arm(binary, no_split, "replay-no-split", ask, no_split_packet,
                                without_splitting(full_text), fact_ids),
        }
    evidence = {
        "fixture_facts": len(facts),
        "split_inputs": sorted(fact_ids[key] for key in full_packet["split"]),
        "group_inputs": [[fact_ids[key] for key in sorted(group)] for group in full_packet["groups"]],
        "before": before,
        **arms,
    }
    if all("error" not in arm for arm in arms.values()):
        full_actions, no_split_actions = (arms[name]["actions"] for name in ("full", "no_split"))
        evidence["missing_full_actions"] = [action for action in full_actions if action not in no_split_actions]
        evidence["extra_no_split_actions"] = [action for action in no_split_actions if action not in full_actions]
        rank_pairs = {query: tuple(arms[name]["recall"][query]["rank"] for name in ("full", "no_split"))
                      for query in before}
        evidence["lost_recall"] = [
            query for query, (full_rank, no_split_rank) in rank_pairs.items()
            if full_rank is not None and no_split_rank is None
        ]
        evidence["rank_regressions"] = [
            query for query, (full_rank, no_split_rank) in rank_pairs.items()
            if full_rank is not None and no_split_rank is not None and no_split_rank > full_rank
        ]
    return evidence
