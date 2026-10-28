"""Turn the raw records of the #531 stand into one compact record per run.

Run: python3 -I extract.py RUNS OUT. RUNS is runs/ of the stand (runs/<arm>/<scenario>/r<n>/), OUT a new directory.
OUT/<arm>__<scenario>__r<n>.json has two parts: "meta" (arm, scenario, repeat, exit, time, cost), which the graders
never see, and "record" (the prompt, what the run changed in its base, its memory calls with their results, the pages
it read and its final answer), which is all they see. A grader therefore does not know the arm.
"""

import json
import re
import sys
from pathlib import Path

RESULT_CHARS = 1500  # a tool result is cut to this many characters; the memory and the answer are kept whole
DUMP = re.compile(r"^(\S+) v(\d+) (\S+) (.*?) \[([^\]]*)\] (.*)$")
MEMORY_TOOLS = ("memcue_search", "memcue_add", "memcue_correct", "memcue_retire")


def parse_dump(path: Path) -> dict[tuple[str, int], dict]:
    out = {}
    if not path.is_file():
        return out
    for line in path.read_text(encoding="utf-8", errors="replace").splitlines():
        m = DUMP.match(line)
        if not m:
            continue
        head = m.group(4)
        scope = re.search(r"\bscope=(\S+)", head)
        kind = re.search(r"\bkind=(\S+)", head)
        source = re.search(r"\bsource=(\S+)", head)
        out[(m.group(1), int(m.group(2)))] = {
            "id": m.group(1), "version": int(m.group(2)), "status": m.group(3),
            "scope": scope.group(1) if scope else "", "kind": kind.group(1) if kind else "",
            "source": source.group(1) if source else "", "keywords": m.group(5), "content": m.group(6)}
    return out


def changes(rec: Path) -> list[dict]:
    before, after = parse_dump(rec / "base_before.txt"), parse_dump(rec / "base_after.txt")
    known = {fid for fid, _ in before}
    out = []
    for key in sorted(set(after) - set(before)):
        f = dict(after[key])
        f["change"] = "added" if f["id"] not in known and f["version"] == 1 else (
            "retired" if f["status"] == "retired" else "corrected")
        out.append(f)
    return out


def cut(text) -> str:
    if not isinstance(text, str):
        text = json.dumps(text, ensure_ascii=False)
    return text if len(text) <= RESULT_CHARS else text[:RESULT_CHARS] + f"… [{len(text) - RESULT_CHARS} chars cut]"


def block_text(content) -> str:
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        return "\n".join(b.get("text", "") for b in content if isinstance(b, dict))
    return ""


def claude_code(rec: Path) -> tuple[list[dict], list[str], str]:
    events = []
    for line in (rec / "stream.jsonl").read_text(encoding="utf-8", errors="replace").splitlines():
        try:
            events.append(json.loads(line))
        except ValueError:
            pass
    results = {}
    for e in events:
        if e.get("type") == "user":
            for b in (e.get("message") or {}).get("content") or []:
                if isinstance(b, dict) and b.get("type") == "tool_result":
                    results[b.get("tool_use_id")] = block_text(b.get("content"))
    calls, tools, answer = [], [], ""
    for e in events:
        if e.get("type") == "assistant":
            for b in (e.get("message") or {}).get("content") or []:
                if not isinstance(b, dict) or b.get("type") != "tool_use":
                    continue
                name, inp = b.get("name", ""), b.get("input") or {}
                tools.append(name)
                command = str(inp.get("command", "")) if name == "Bash" else ""
                if "memcue" in command or (name == "Skill" and "memcue" in json.dumps(inp)):
                    calls.append({"tool": name, "input": command or inp, "result": cut(results.get(b.get("id"), ""))})
        elif e.get("type") == "result":
            answer = e.get("result") or ""
    return calls, tools, answer


def hermes(rec: Path) -> tuple[list[dict], list[str], str]:
    answer = (rec / "answer.txt").read_text(encoding="utf-8", errors="replace") if (rec / "answer.txt").is_file() else ""
    path = rec / "session.jsonl"
    if not path.is_file():
        return [], [], answer
    session = json.loads(path.read_text(encoding="utf-8").splitlines()[0])
    messages = session.get("messages") or []
    results = {m.get("tool_call_id"): m.get("content") for m in messages if m.get("role") == "tool"}
    calls, tools = [], []
    for m in messages:
        for tc in m.get("tool_calls") or []:
            fn = tc.get("function") or {}
            name, raw = fn.get("name", ""), fn.get("arguments") or "{}"
            try:
                args = json.loads(raw) if isinstance(raw, str) else raw
            except ValueError:
                args = {"raw": raw}
            # A deferred tool is called through tool_call with its own name inside.
            inner = str(args.get("name") or args.get("tool") or "") if name == "tool_call" else ""
            tools.append(inner or name)
            if name in MEMORY_TOOLS or inner in MEMORY_TOOLS or (name == "terminal" and "memcue" in json.dumps(args)):
                calls.append({"tool": inner or name, "input": args, "result": cut(results.get(tc.get("id"), ""))})
    return calls, tools, answer


def main() -> int:
    runs, out = Path(sys.argv[1]).resolve(), Path(sys.argv[2]).resolve()
    out.mkdir(parents=True)  # a new directory; nothing is overwritten
    n = 0
    for d in sorted(runs.glob("*/*/r*")):
        if not d.name[1:].isdigit():  # r<n>.failed-<k>: a run moved aside before it was run again
            continue
        arm, sid, rep = d.parent.parent.name, d.parent.name, d.name
        rec = d / "rec"
        meta = json.loads((d / "run.json").read_text(encoding="utf-8"))
        calls, tools, answer = (claude_code if arm.startswith("cc-") else hermes)(rec)
        pages = []
        if (rec / "web.log").is_file():
            pages = [json.loads(l)["url"] for l in (rec / "web.log").read_text(encoding="utf-8").splitlines() if l.strip()]
        exit_code = (rec / "exit").read_text(encoding="utf-8").strip() if (rec / "exit").is_file() else "missing"
        doc = {
            "meta": {"arm": arm, "scenario": sid, "repeat": rep, "chain_from": meta.get("chain_from"),
                     "exit": exit_code, "seconds": meta.get("seconds"), "docker_exit": meta.get("docker_exit")},
            "record": {"prompt": (d / "prompt.txt").read_text(encoding="utf-8"), "base_changes": changes(rec),
                       "memory_calls": calls, "tools_in_order": tools, "pages_read": pages,
                       "final_answer": answer.strip()},
        }
        (out / f"{arm}__{sid}__{rep}.json").write_text(json.dumps(doc, ensure_ascii=False, indent=1), encoding="utf-8")
        n += 1
    print(json.dumps({"records": n, "out": str(out)}))
    return 0


if __name__ == "__main__":
    sys.exit(main())
