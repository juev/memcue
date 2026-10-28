"""Name the records whose two grading passes disagree, for a third pass.

Run: python3 -I disagree.py GRADES. GRADES holds A/ and B/ from grade.py. Prints the record names (comma-separated)
where the passes differ on the answer, on whether and where an expected item is stored, on the search fields, or on
the verdict of any change, and where either pass ended in an error; a summary of agreement goes to stderr.
"""

import json
import sys
from pathlib import Path

FIELDS = ("answer", "searched_memory", "named_shelf", "needed_fact_found")


def key(grade: dict) -> dict:
    return {**{f: grade.get(f) for f in FIELDS},
            "capture": [(c["key"], c["stored"], c["place"]) for c in grade.get("capture", [])],
            "changes": [(c["id"], c["verdict"]) for c in grade.get("changes", [])]}


def main() -> int:
    root = Path(sys.argv[1])
    names = sorted(p.name for p in (root / "A").glob("*.json"))
    disagree, agree_by = [], {f: 0 for f in (*FIELDS, "capture", "changes")}
    for name in names:
        a = json.loads((root / "A" / name).read_text(encoding="utf-8"))
        b_path = root / "B" / name
        b = json.loads(b_path.read_text(encoding="utf-8")) if b_path.is_file() else {"error": "missing"}
        if "grade" not in a or "grade" not in b or a["record_sha256"] != b.get("record_sha256"):
            disagree.append(name[:-5])
            continue
        ka, kb = key(a["grade"]), key(b["grade"])
        for f in agree_by:
            agree_by[f] += ka[f] == kb[f]
        if ka != kb:
            disagree.append(name[:-5])
    print(",".join(disagree))
    print(json.dumps({"records": len(names), "disagree": len(disagree), "agree_by_field": agree_by}), file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
