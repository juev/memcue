"""Runner of the #531 stand, on the docker host.

    python3 -I run.py one ARM SCENARIO REPEAT
    python3 -I run.py grid --repeats 3 --jobs 3 --budget 40 [--arms cc-base,cc-cand,hm-base,hm-cand] [--only S01,S02]
    python3 -I run.py cost

ROOT (default ~/stand531) holds fixtures/ (a copy of the repository's), secrets/claude.env and secrets/deepseek.env
(mode 600, written by the owner), and runs/<arm>/<scenario>/r<n>/. A run directory is never deleted
or overwritten: a finished run is skipped, and an unfinished one is moved aside to r<n>.failed-<k> and run again;
a full rerun is a new ROOT. Each run is one container: no capabilities, no new privileges, limits on memory, CPU and
processes, its own network, the run directory at /stand and the fixtures read-only; nothing else of the host.
"""

import argparse
import json
import os
import shutil
import subprocess
import sys
import time
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path

ROOT = Path(os.environ.get("STAND531_ROOT", Path.home() / "stand531")).resolve()
ARMS = {
    "cc-base": ("stand531-cc", "base", "claude.env"),
    "cc-cand": ("stand531-cc", "cand", "claude.env"),
    "hm-base": ("stand531-hermes", "base", "deepseek.env"),
    "hm-cand": ("stand531-hermes", "cand", "deepseek.env"),
}
NETWORK = "stand531"
TIMEOUT = 20 * 60


def scenarios() -> dict:
    doc = json.loads((ROOT / "fixtures/scenarios.json").read_text(encoding="utf-8"))
    return {s["id"]: s for s in doc["scenarios"]}


def run_dir(arm: str, sid: str, repeat: int) -> Path:
    return ROOT / "runs" / arm / sid / f"r{repeat}"


def finished(d: Path) -> bool:
    return (d / "rec" / "exit").is_file()


def git(ws: Path, *args: str) -> None:
    subprocess.run(["git", "-C", str(ws), "-c", "user.name=stand", "-c", "user.email=stand@example.com",
                    "-c", "init.defaultBranch=main", *args], check=True, capture_output=True)


def prepare(arm: str, s: dict, repeat: int) -> Path:
    d = run_dir(arm, s["id"], repeat)
    d.parent.mkdir(parents=True, exist_ok=True)
    if d.exists():  # a run that did not finish is moved aside, not deleted, and started again
        k = 1
        while d.with_name(f"{d.name}.failed-{k}").exists():
            k += 1
        d.rename(d.with_name(f"{d.name}.failed-{k}"))
    d.mkdir()
    ws = d / "ws"
    if s["workspace"]:
        shutil.copytree(ROOT / "fixtures/workspaces" / s["workspace"], ws)
    else:
        ws.mkdir()
    name = s["workspace"] or "scratch"
    git(ws, "init", "-q")
    git(ws, "remote", "add", "origin", f"https://example.com/acme/{name}.git")
    git(ws, "add", "-A")
    git(ws, "commit", "-q", "--allow-empty", "-m", "initial")
    (d / "base").mkdir()
    if s["chain_from"]:
        src = run_dir(arm, s["chain_from"], repeat) / "base" / "facts.db"
        if not src.is_file():
            raise RuntimeError(f"{arm}/{s['id']}/r{repeat}: the run it chains from left no base")
        shutil.copy2(src, d / "base" / "facts.db")
    (d / "prompt.txt").write_text(s["prompt"], encoding="utf-8")
    (d / "run.json").write_text(json.dumps({"arm": arm, "scenario": s["id"], "repeat": repeat, "seed": s["seed"],
                                            "chain_from": s["chain_from"], "prepared": time.time()}),
                                encoding="utf-8")
    return d


def image_id(image: str) -> str:
    return subprocess.run(["docker", "image", "inspect", image, "--format", "{{.Id}}"], check=True,
                          capture_output=True, text=True).stdout.strip()


def run_one(arm: str, s: dict, repeat: int) -> str:
    d = run_dir(arm, s["id"], repeat)
    if finished(d):
        return f"{arm}/{s['id']}/r{repeat}: done before"
    image, variant, secret = ARMS[arm]
    d = prepare(arm, s, repeat)
    name = f"stand531-{arm}-{s['id']}-r{repeat}".lower()
    cmd = ["docker", "run", "--rm", "--name", name, "--network", NETWORK, "--user", f"{os.getuid()}:{os.getgid()}",
           "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--pids-limit", "512", "--memory", "4g",
           "--cpus", "2", "--tmpfs", "/tmp:rw,exec,size=1g", "--env-file", str(ROOT / "secrets" / secret),
           "-v", f"{d}:/stand", "-v", f"{ROOT / 'fixtures'}:/opt/fixtures:ro", image, variant]
    meta = json.loads((d / "run.json").read_text(encoding="utf-8"))
    meta["image"] = image_id(image)
    start = time.time()
    try:
        p = subprocess.run(cmd, capture_output=True, text=True, timeout=TIMEOUT)
        meta["docker_exit"] = p.returncode
        (d / "docker.log").write_text(p.stdout[-4000:] + p.stderr[-4000:], encoding="utf-8")
    except subprocess.TimeoutExpired:
        subprocess.run(["docker", "kill", name], capture_output=True)
        meta["docker_exit"] = "timeout"
    meta["seconds"] = round(time.time() - start, 1)
    (d / "run.json").write_text(json.dumps(meta), encoding="utf-8")
    return f"{arm}/{s['id']}/r{repeat}: exit {meta['docker_exit']} in {meta['seconds']} s, ${run_cost(d):.3f}"


def run_cost(d: Path) -> float:
    """The provider's cost of a run: Claude Code's result event, or Hermes's usage report."""
    stream = d / "rec" / "stream.jsonl"
    if stream.is_file():
        for line in stream.read_text(encoding="utf-8", errors="replace").splitlines():
            try:
                ev = json.loads(line)
            except ValueError:
                continue
            if ev.get("type") == "result":
                return float(ev.get("total_cost_usd") or 0)
    usage = d / "rec" / "usage.json"
    if usage.is_file():
        try:
            u = json.loads(usage.read_text(encoding="utf-8"))
        except ValueError:
            return 0.0
        for key in ("estimated_cost_usd", "cost_usd", "estimated_cost", "cost"):
            if isinstance(u.get(key), (int, float)):
                return float(u[key])
    return 0.0


def total_cost() -> float:
    runs = ROOT / "runs"
    return sum(run_cost(d) for d in runs.glob("*/*/r*") if d.name[1:].isdigit()) if runs.is_dir() else 0.0


def grid(arms: list[str], repeats: int, jobs: int, budget: float, only: set[str]) -> int:
    sc = scenarios()
    todo = [s for sid, s in sorted(sc.items()) if not only or sid in only]
    phases = [[s for s in todo if not s["chain_from"]], [s for s in todo if s["chain_from"]]]
    for phase in phases:
        work = [(a, s, r) for r in range(1, repeats + 1) for s in phase for a in arms]
        with ThreadPoolExecutor(max_workers=jobs) as pool:
            futures = []
            for a, s, r in work:
                futures.append(pool.submit(guarded, a, s, r, budget))
            for f in futures:
                print(f.result(), flush=True)
    print(f"total ${total_cost():.2f}")
    return 0


def guarded(arm: str, s: dict, repeat: int, budget: float) -> str:
    spent = total_cost()
    if spent >= budget:
        return f"{arm}/{s['id']}/r{repeat}: skipped, ${spent:.2f} spent of ${budget:.2f}"
    try:
        return run_one(arm, s, repeat)
    except Exception as e:  # one failed run must not stop the grid; its directory stays for a look
        return f"{arm}/{s['id']}/r{repeat}: error {type(e).__name__}: {e}"


def main() -> int:
    ap = argparse.ArgumentParser()
    sub = ap.add_subparsers(dest="cmd", required=True)
    one = sub.add_parser("one")
    one.add_argument("arm", choices=sorted(ARMS))
    one.add_argument("scenario")
    one.add_argument("repeat", type=int)
    g = sub.add_parser("grid")
    g.add_argument("--arms", default=",".join(ARMS))
    g.add_argument("--repeats", type=int, default=3)
    g.add_argument("--jobs", type=int, default=3)
    g.add_argument("--budget", type=float, required=True)
    g.add_argument("--only", default="")
    sub.add_parser("cost")
    a = ap.parse_args()
    for secret in ("claude.env", "deepseek.env"):
        path = ROOT / "secrets" / secret
        if path.exists() and path.stat().st_mode & 0o077:
            sys.exit(f"{path} must be readable by its owner only (chmod 600)")
    if a.cmd == "one":
        print(run_one(a.arm, scenarios()[a.scenario], a.repeat))
    elif a.cmd == "grid":
        arms = [x for x in a.arms.split(",") if x]
        if set(arms) - set(ARMS):
            sys.exit(f"unknown arm in {a.arms}")
        return grid(arms, a.repeats, a.jobs, a.budget, {x for x in a.only.split(",") if x})
    else:
        print(f"${total_cost():.2f}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
