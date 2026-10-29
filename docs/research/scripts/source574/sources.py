"""Count the memcue add calls of the #574 measurement, whether they name -source, and the source each stored fact got.

Run: python3 -I sources.py RECORDS. RECORDS holds the compact records of confirm531/extract.py; the arm comes from
their meta. A call is a `memcue add` in a Bash command of the run; `memcue add -h` counts as a call too.
"""

import json
import re
import sys
from collections import Counter, defaultdict
from pathlib import Path

ADD = re.compile(r"memcue add\b[^;&|\n]*")


def main() -> None:
    arms = defaultdict(lambda: {"runs": 0, "adds": 0, "help": 0, "named": 0, "stored": Counter(), "rows": []})
    for path in sorted(Path(sys.argv[1]).glob("*.json")):
        doc = json.loads(path.read_text(encoding="utf-8"))
        meta, record = doc["meta"], doc["record"]
        a = arms[meta["arm"]]
        a["runs"] += 1
        calls = 0
        for call in record["memory_calls"]:
            text = call["input"] if isinstance(call["input"], str) else json.dumps(call["input"])
            for cmd in ADD.findall(text):
                calls += 1
                a["adds"] += 1
                a["help"] += cmd.split()[2:3] == ["-h"]
                a["named"] += "-source" in cmd
        stored = [(f["kind"], f["source"]) for f in record["base_changes"] if f["change"] == "added"]
        a["stored"].update(source for _, source in stored)
        a["rows"].append(f"{meta['scenario']} {meta['repeat']}: add calls {calls}, stored {stored}")
    for arm, a in sorted(arms.items()):
        print(f"== {arm}: runs {a['runs']}, add calls {a['adds']} (help {a['help']}), naming -source {a['named']}, "
              f"stored by source {dict(a['stored'])}")
        print("\n".join(a["rows"]))


if __name__ == "__main__":
    main()
