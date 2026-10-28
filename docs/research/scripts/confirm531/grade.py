"""Grade the compact records of the #531 stand with fresh Claude Code instances, on the docker host.

    python3 -I grade.py RECORDS LABELS OUT --pass A [--jobs 4] [--only NAME,NAME]

RECORDS is the output of extract.py, LABELS fixtures/labels.json, OUT a directory of passes. Each record is graded by
a new `claude -p` (Opus 5.5) in a stand531-cc container with no tools: it sees grade_prompt.md, the scenario's labels
and the record, never the run's arm. OUT/<pass>/<name>.json keeps the grade with the sha256 of the record it judged;
a grade whose hash still matches is not redone, and a changed record is graded again. An invalid answer is retried
once, then kept as an error.
"""

import argparse
import hashlib
import json
import os
import re
import subprocess
import sys
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path

HERE = Path(__file__).resolve().parent
ROOT = Path(os.environ.get("STAND531_ROOT", Path.home() / "stand531")).resolve()
MODEL = "claude-opus-5-5"
VERDICTS = {"expected", "extra_useful", "extra_noise", "forbidden"}
ANSWERS = {"correct", "partial", "wrong", "unresolved"}
NO_TOOLS = "Bash,Read,Write,Edit,Glob,Grep,WebFetch,WebSearch,Task,NotebookEdit,TodoWrite,Skill"


def record_hash(record: dict) -> str:
    return hashlib.sha256(json.dumps(record, sort_keys=True, ensure_ascii=False).encode()).hexdigest()


def validate(grade: dict, label: dict, record: dict) -> str:
    """Empty when the grade has the promised shape, else what is wrong."""
    keys = [c["key"] for c in label["expected_capture"]]
    cap = grade.get("capture")
    if not isinstance(cap, list) or [c.get("key") for c in cap] != keys:
        return "capture keys"
    for c in cap:
        if not isinstance(c.get("stored"), bool) or c.get("place") not in (("shelf", "ordinary") if c["stored"] else (None,)):
            return "capture entry"
    ch = grade.get("changes")
    if not isinstance(ch, list) or len(ch) != len(record["base_changes"]):
        return "changes count"
    for g, c in zip(ch, record["base_changes"]):
        if g.get("id") != c["id"] or g.get("verdict") not in VERDICTS:
            return "change entry"
    if not isinstance(grade.get("searched_memory"), bool) or not isinstance(grade.get("named_shelf"), bool):
        return "search fields"
    if grade.get("needed_fact_found") not in ("yes", "no", "not_applicable"):
        return "needed_fact_found"
    if grade.get("answer") not in ANSWERS or not isinstance(grade.get("answer_reason"), str):
        return "answer"
    return ""


def ask(prompt: str) -> tuple[dict | None, float, str]:
    cmd = ["docker", "run", "--rm", "-i", "--network", "stand531", "--user", f"{os.getuid()}:{os.getgid()}",
           "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--pids-limit", "256", "--memory", "2g",
           "--tmpfs", "/tmp:rw,exec,size=256m", "--env-file", str(ROOT / "secrets" / "claude.env"),
           "-e", "HOME=/tmp/home", "-e", "DISABLE_TELEMETRY=1", "-e", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
           "--entrypoint", "sh", "stand531-cc", "-c",
           f'mkdir -p /tmp/home && cd /tmp && claude -p --output-format json --model {MODEL} --setting-sources "" '
           f'--strict-mcp-config --no-session-persistence --disallowedTools {NO_TOOLS}']
    p = subprocess.run(cmd, input=prompt, capture_output=True, text=True, timeout=600)
    try:
        out = json.loads(p.stdout)
    except ValueError:
        return None, 0.0, f"no json from claude (exit {p.returncode})"
    cost = float(out.get("total_cost_usd") or 0)
    text = out.get("result") or ""
    m = re.search(r"\{.*\}", text, re.S)
    if not m:
        return None, cost, "no object in the answer"
    try:
        return json.loads(m.group(0)), cost, ""
    except ValueError:
        return None, cost, "invalid object in the answer"


def grade_one(path: Path, labels: dict, out: Path, pass_name: str) -> str:
    doc = json.loads(path.read_text(encoding="utf-8"))
    record, sid = doc["record"], doc["meta"]["scenario"]
    label = labels[sid]
    digest = record_hash(record)
    target = out / pass_name / path.name
    if target.is_file() and json.loads(target.read_text(encoding="utf-8")).get("record_sha256") == digest:
        return f"{path.stem}: kept"
    prompt = ((HERE / "grade_prompt.md").read_text(encoding="utf-8")
              + "\n\n## Labels\n\n```json\n" + json.dumps(label, ensure_ascii=False, indent=1)
              + "\n```\n\n## Record\n\n```json\n" + json.dumps(record, ensure_ascii=False, indent=1) + "\n```\n")
    spent, error, grade = 0.0, "", None
    for _ in range(2):
        grade, cost, error = ask(prompt)
        spent += cost
        if grade is not None:
            error = validate(grade, label, record)
            if not error:
                break
    result = {"pass": pass_name, "record_sha256": digest, "cost_usd": round(spent, 4)}
    if error:
        result["error"] = error
    else:
        result["grade"] = grade
    target.write_text(json.dumps(result, ensure_ascii=False, indent=1), encoding="utf-8")
    return f"{path.stem}: {'error ' + error if error else grade['answer']} ${spent:.3f}"


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("records")
    ap.add_argument("labels")
    ap.add_argument("out")
    ap.add_argument("--pass", dest="pass_name", required=True)
    ap.add_argument("--jobs", type=int, default=4)
    ap.add_argument("--only", default="")
    a = ap.parse_args()
    records, out = Path(a.records), Path(a.out)
    labels = json.loads(Path(a.labels).read_text(encoding="utf-8"))
    (out / a.pass_name).mkdir(parents=True, exist_ok=True)
    only = {x for x in a.only.split(",") if x}
    files = [p for p in sorted(records.glob("*.json")) if not only or p.stem in only]
    with ThreadPoolExecutor(max_workers=a.jobs) as pool:
        for line in pool.map(lambda p: grade_one(p, labels, out, a.pass_name), files):
            print(line, flush=True)
    return 0


if __name__ == "__main__":
    sys.exit(main())
