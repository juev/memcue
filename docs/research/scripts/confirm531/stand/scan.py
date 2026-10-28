"""Look for secrets and host details in what the #531 stand is about to publish, on the docker host.

Run: python3 -I scan.py PATH... It reads the exact secret values from ROOT/secrets/*.env and looks for each of them,
and for the host's home directory, user name and host name, in every file under the given paths. It prints counts per
kind and the files that hold a match, never the matched text. Exits 1 on any secret match.
"""

import os
import socket
import sys
from pathlib import Path

ROOT = Path(os.environ.get("STAND531_ROOT", Path.home() / "stand531")).resolve()


def secrets() -> list[str]:
    values = []
    for env in sorted((ROOT / "secrets").glob("*.env")):
        for line in env.read_text(encoding="utf-8").splitlines():
            if "=" in line:
                value = line.split("=", 1)[1].strip()
                if len(value) >= 12:
                    values.append(value)
    return values


def main() -> int:
    needles = {"secret": secrets(), "home": [str(Path.home())], "user": [os.environ.get("USER") or Path.home().name],
               "hostname": [socket.gethostname()]}
    counts = {k: 0 for k in needles}
    files: dict[str, set[str]] = {k: set() for k in needles}
    for root in sys.argv[1:]:
        for path in Path(root).rglob("*"):
            if not path.is_file():
                continue
            try:
                text = path.read_text(encoding="utf-8", errors="replace")
            except OSError:
                continue
            for kind, values in needles.items():
                n = sum(text.count(v) for v in values if v)
                if n:
                    counts[kind] += n
                    files[kind].add(str(path))
    print({"matches": counts, "files": {k: len(v) for k, v in files.items()}})
    for kind in needles:
        for f in sorted(files[kind])[:20]:
            print(f"{kind}: {f}")
    return 1 if counts["secret"] else 0


if __name__ == "__main__":
    sys.exit(main())
