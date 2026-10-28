"""Store the facts of fixtures/seed/<name>.json in the run's base before the agent starts.

Run inside the stand container: python3 -I seed.py NAME. MEMCUE_FACTS names the base. The facts go in through
`memcue import`, as stored earlier, so they keep their kind, scope, source and origin and skip the check `add` runs
on what an agent writes: that check refused a shelf finding about another system's config default as installation
state. A failure stops the run without fixture text in the output.
"""

import json
import os
import subprocess
import sys
import tempfile
from pathlib import Path

SEED = Path("/opt/fixtures/seed")


def main() -> int:
    facts = json.loads((SEED / f"{sys.argv[1]}.json").read_text(encoding="utf-8"))
    fd, path = tempfile.mkstemp(suffix=".json")
    with os.fdopen(fd, "w", encoding="utf-8") as f:
        for i, fact in enumerate(facts):
            kind = fact.get("kind", "")
            # Rules and decisions are the user's words, the only source that reaches the session card.
            source = "user" if kind in ("rule", "decision") else "tool"
            f.write(json.dumps({"note": f"seed {i}", "origin": fact.get("origin", ""), "facts": [{
                "content": fact["content"], "keywords": fact["keywords"], "kind": kind, "scope": fact["scope"],
                "source": source}]}) + "\n")
    p = subprocess.run(["memcue", "import", path], capture_output=True, text=True, stdin=subprocess.DEVNULL)
    os.unlink(path)
    if p.returncode != 0 or f"imported {len(facts)} facts" not in p.stdout:
        print(f"seed import failed (exit {p.returncode})", file=sys.stderr)
        return 1
    print(f"seeded {len(facts)} facts")
    return 0


if __name__ == "__main__":
    sys.exit(main())
