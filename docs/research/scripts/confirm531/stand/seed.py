"""Store the facts of fixtures/seed/<name>.json in the run's base before the agent starts.

Run inside the stand container: python3 -I seed.py NAME. MEMCUE_FACTS names the base. A refused fact stops the run
with its index only, so the output never carries fixture text.
"""

import json
import subprocess
import sys
from pathlib import Path

SEED = Path("/opt/fixtures/seed")


def main() -> int:
    facts = json.loads((SEED / f"{sys.argv[1]}.json").read_text(encoding="utf-8"))
    for i, f in enumerate(facts):
        kind = f.get("kind", "")
        # Rules and decisions are the user's words, the only source that reaches the session card.
        source = "user" if kind in ("rule", "decision") else "tool"
        cmd = ["memcue", "add", "-source", source, "-cwd", "", "-scope", f["scope"],
               "-keywords", ",".join(f["keywords"])]
        if kind:
            cmd += ["-kind", kind]
        if f.get("origin"):
            cmd += ["-origin", f["origin"]]
        p = subprocess.run(cmd + ["--", f["content"]], capture_output=True, text=True, stdin=subprocess.DEVNULL)
        if p.returncode != 0:
            print(f"seed fact {i} refused (exit {p.returncode})", file=sys.stderr)
            return 1
    print(f"seeded {len(facts)} facts")
    return 0


if __name__ == "__main__":
    sys.exit(main())
