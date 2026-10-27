#!/usr/bin/env python3
"""Measure the scratch repositories that `synth workload` wrote (#530): one per encoding of the same synthetic history.

For each repository: the loose store, the thin pack of every commit, a full clone pack, git gc (and gc --aggressive
for the two that matter most), and a push and a fetch per commit over file:// for the encodings a sync would use.

--root must be a directory `synth workload` created (it holds the marker file that synth writes); --root/work and
--out must not exist. Besides the marker, --root may hold only repositories that pass gitmeasure.check_repo: plain
repositories of this user inside --root, without a symlink anywhere, a gitfile, alternates, a commondir or foreign
config. Anything else is refused before the first git command, so gc never runs on a repository outside --root.
Temporary files go to --root/work/tmp, and only --root/work, which this script created, is removed at the end. A run
killed with SIGKILL leaves --root/work behind.
"""

from __future__ import annotations

import argparse
import json
import shutil
import sys
import tempfile
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import gitmeasure as gm  # noqa: E402

GC = ("gc", ["gc", "-q", "--prune=now"])
AGGRESSIVE = ("gc-aggressive", ["gc", "-q", "--aggressive", "--prune=now"])
SYNC = {"sqlite-every", "sqlite-batch5", "sqlite-batch20", "text-single", "text-perfact", "text-events"}
MARKER = ".gitgrowth-scratch"  # written by synth into every directory it creates with -out


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--root", type=Path, required=True, help="directory synth workload wrote")
    ap.add_argument("--workload", type=Path, required=True, help="the JSON synth workload wrote")
    ap.add_argument("--out", type=Path, required=True, help="new JSON file for the results")
    a = ap.parse_args()
    marker = a.root / MARKER
    if a.root.is_symlink() or not a.root.is_dir() or marker.is_symlink() or not marker.is_file():
        sys.exit(f"{a.root} is not a directory synth workload created (no {MARKER})")
    try:
        repos = sorted(p for p in a.root.iterdir() if p != marker)
        for repo in repos:
            gm.check_repo(repo, a.root)
    except (gm.UnsafeRepo, OSError, RuntimeError) as e:
        sys.exit(f"refusing {a.root}: {e}")
    with open(a.out, "x") as out:  # refuse an existing file or symlink
        res = {"git": gm.run(["git", "--version"]).out.decode().strip(),
               "workload": json.loads(a.workload.read_text()), "variants": {}}
        work = gm.new_dir(a.root / "work")
        tempfile.tempdir = str(gm.new_dir(work / "tmp"))
        for repo in repos:
            print(f"measuring {repo.name}", flush=True)
            variants = [GC, AGGRESSIVE] if repo.name in ("sqlite-every", "text-single") else [GC]
            r = gm.measure(repo, work, a.root, variants, limit_kib=24 * 2**20)
            if repo.name in SYNC:
                print(f"sync replay {repo.name}", flush=True)
                r["sync"] = gm.sync_replay(repo, work, a.root)
            if repo.name == "sqlite-every":
                print(f"sync replay {repo.name}, transfer.unpackLimit=1", flush=True)
                r["sync_keep_packs"] = gm.sync_replay(repo, work, a.root, unpack_limit=1)
            res["variants"][repo.name] = r
        shutil.rmtree(work)
        out.write(json.dumps(res, indent=1) + "\n")
    print(f"wrote {a.out}")


if __name__ == "__main__":
    main()
