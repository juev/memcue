"""Validate fixtures/ against SCHEMA.md.

Run: python3 -I check_fixtures.py [--verbose]. Without --verbose it prints counts only, so the author of the candidate
text can check the layout without reading a scenario. Exits 1 on any problem.
"""

import json
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent / "fixtures"
KINDS = {"capture", "recall"}
FACT_KINDS = {"rule", "decision", "pitfall", "environment", "procedure", "event"}
SCOPE = re.compile(r"^(global|host:[a-z0-9][a-z0-9.-]*|service:[a-z0-9][a-z0-9.-]*|"
                   r"repo:example\.com/acme/[a-z0-9][a-z0-9-]*|shelf:research)$")
URL = re.compile(r"https?://([A-Za-z0-9.-]+)")
RESERVED = re.compile(r"(^|\.)(example|example\.com|example\.org)$")
CUE = re.compile(r"\b(memcue|shelf|memory|remember|earlier session)\b", re.I)

problems: list[tuple[str, str]] = []  # (category, detail); the detail is printed only with --verbose


def problem(category: str, detail: str) -> None:
    problems.append((category, detail))


def load(path: Path):
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except (OSError, ValueError) as e:
        problem("unreadable json", f"{path.relative_to(ROOT)}: {e}")
        return None


def check_seed(name: str) -> int:
    facts = load(ROOT / "seed" / f"{name}.json")
    if not isinstance(facts, list) or not facts:
        problem("seed shape", name)
        return 0
    for i, f in enumerate(facts):
        where = f"{name}[{i}]"
        if not isinstance(f, dict) or not isinstance(f.get("content"), str) or not f["content"].strip():
            problem("seed fact", where)
            continue
        kw = f.get("keywords")
        if not isinstance(kw, list) or not kw or not all(isinstance(k, str) and k.strip() for k in kw):
            problem("seed keywords", where)
        if not isinstance(f.get("scope"), str) or not SCOPE.match(f["scope"]):
            problem("seed scope", where)
        if "kind" in f and f["kind"] not in FACT_KINDS:
            problem("seed kind", where)
        if "origin" in f and not isinstance(f["origin"], str):
            problem("seed origin", where)
        if set(f) - {"content", "keywords", "scope", "kind", "origin"}:
            problem("seed extra field", where)
    return len(facts)


def main() -> int:
    verbose = "--verbose" in sys.argv[1:]
    doc = load(ROOT / "scenarios.json")
    labels = load(ROOT / "labels.json")
    scenarios = doc.get("scenarios") if isinstance(doc, dict) else None
    if not isinstance(scenarios, list) or not scenarios:
        problem("scenarios shape", "scenarios.json needs {\"scenarios\": [...]}")
        scenarios = []
    if not isinstance(labels, dict):
        problem("labels shape", "labels.json needs an object keyed by scenario id")
        labels = {}

    ids, kinds, seeds_used, workspaces_used = [], {}, set(), set()
    for s in scenarios:
        if not isinstance(s, dict):
            problem("scenario shape", repr(s)[:40])
            continue
        sid = s.get("id")
        if not isinstance(sid, str) or not re.fullmatch(r"S\d{2}", sid) or sid in ids:
            problem("scenario id", str(sid))
            continue
        ids.append(sid)
        kinds[sid] = s.get("kind")
        if s.get("kind") not in KINDS:
            problem("scenario kind", sid)
        if not isinstance(s.get("prompt"), str) or not s["prompt"].strip():
            problem("scenario prompt", sid)
        if set(s) != {"id", "kind", "prompt", "workspace", "seed", "chain_from"}:
            problem("scenario fields", sid)
        ws = s.get("workspace")
        if ws is not None:
            if not isinstance(ws, str) or not re.fullmatch(r"[a-z0-9][a-z0-9-]*", ws) or not (ROOT / "workspaces" / ws).is_dir():
                problem("scenario workspace", sid)
            else:
                workspaces_used.add(ws)
        seed = s.get("seed")
        if seed is not None:
            if not isinstance(seed, str) or not (ROOT / "seed" / f"{seed}.json").is_file():
                problem("scenario seed", sid)
            else:
                seeds_used.add(seed)
    for s in scenarios:
        if not isinstance(s, dict) or s.get("chain_from") is None:
            continue
        src = s["chain_from"]
        if kinds.get(src) != "capture" or s.get("kind") != "recall" or s.get("seed") is not None:
            problem("chain", str(s.get("id")))

    for sid in ids:
        lab = labels.get(sid)
        if not isinstance(lab, dict):
            problem("label missing", sid)
            continue
        if set(lab) != {"purpose", "explicit_cue", "expected_capture", "must_not_store", "shelf_needed", "answer"}:
            problem("label fields", sid)
        if not isinstance(lab.get("purpose"), str) or not isinstance(lab.get("explicit_cue"), bool) \
                or not isinstance(lab.get("shelf_needed"), bool) or not isinstance(lab.get("must_not_store"), list):
            problem("label types", sid)
        caps = lab.get("expected_capture")
        if not isinstance(caps, list):
            problem("label capture", sid)
        else:
            keys = [c.get("key") for c in caps if isinstance(c, dict)]
            if len(keys) != len(caps) or len(set(keys)) != len(keys) or not all(
                    isinstance(c.get("statement"), str) and c.get("place") in ("shelf", "ordinary") for c in caps):
                problem("label capture", sid)
        ans = lab.get("answer")
        if not isinstance(ans, dict) or set(ans) != {"correct_if", "partial_if", "wrong_if"}:
            problem("label answer", sid)
        prompt = next((s["prompt"] for s in scenarios if isinstance(s, dict) and s.get("id") == sid), "")
        if isinstance(prompt, str) and CUE.search(prompt) and lab.get("explicit_cue") is False:
            problem("cue without explicit_cue", sid)
    for sid in set(labels) - set(ids):
        problem("label without scenario", sid)

    n_facts = sum(check_seed(name) for name in sorted(seeds_used))
    for path in sorted((ROOT / "seed").glob("*.json")):
        if path.stem not in seeds_used:
            problem("unused seed", path.stem)

    pages = [p for p in (ROOT / "web").rglob("*") if p.is_file()] if (ROOT / "web").is_dir() else []
    for host in (ROOT / "web").iterdir() if (ROOT / "web").is_dir() else []:
        if not RESERVED.search(host.name):
            problem("web host not reserved", host.name)
    texts = [p for p in ROOT.rglob("*") if p.is_file()]
    for p in texts:
        try:
            text = p.read_text(encoding="utf-8")
        except (OSError, UnicodeDecodeError):
            problem("not utf-8 text", str(p.relative_to(ROOT)))
            continue
        for host in URL.findall(text):
            if not RESERVED.search(host.rstrip(".").lower()):
                problem("url host not reserved", f"{p.relative_to(ROOT)}: {host}")

    counts = {k: sum(1 for v in kinds.values() if v == k) for k in sorted(KINDS)}
    print(json.dumps({"scenarios": len(ids), "by_kind": counts,
                      "chains": sum(1 for s in scenarios if isinstance(s, dict) and s.get("chain_from")),
                      "seeds": len(seeds_used), "seed_facts": n_facts, "workspaces": len(workspaces_used),
                      "web_pages": len(pages), "files": len(texts)}))
    by_cat: dict[str, int] = {}
    for cat, _ in problems:
        by_cat[cat] = by_cat.get(cat, 0) + 1
    print(json.dumps({"problems": len(problems), "by_category": by_cat}))
    if verbose:
        for cat, detail in problems:
            print(f"{cat}: {detail}")
    return 1 if problems else 0


if __name__ == "__main__":
    sys.exit(main())
