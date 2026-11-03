#!/usr/bin/env python3
"""The #613 comparison: the shipped SessionStart note against the note with the response-preference line, on the
frozen scenarios of #601 and the project-limited scenario P1 of this study.

    run613.py --inputs IN --out OUT [--reps 3] [--jobs 4] [--arms shipped,cand]

IN holds memcue-shipped and memcue-cand (Linux builds of main and of main with the line, both carrying the plugin's
version), plugin/ (plugins/claude-code of main) and the token file claude-token. OUT gets one directory a run with the
streams, the base before and after, the hook delivery and the exit codes.

A run is isolated by environment, not by a container: its own HOME, Claude config, state and base under the run
directory, no settings sources, no MCP servers, the plugin from IN only, and `claude -p` signed in with the token. A
capture is two turns of one session in the scenario's workspace, a git repository with its own remote; a probe is a
fresh session in the probe's workspace over the base the capture left. The model is Sonnet, as in #601.
"""
import argparse, json, os, shutil, subprocess, sys
from concurrent.futures import ThreadPoolExecutor

H = os.path.dirname(os.path.abspath(__file__))
P601 = os.path.join(H, "..", "preference601", "fixtures")
CLAUDE = os.path.expanduser("~/.local/bin/claude")
DENIED = "WebFetch,WebSearch,RemoteTrigger,CronCreate,CronDelete,ScheduleWakeup,Workflow,PushNotification"


def scenarios():
    out = []
    for path in (os.path.join(P601, "scenarios.json"), os.path.join(H, "fixtures", "scenarios613.json")):
        out += json.load(open(path))["scenarios"]
    return out


def workspace(name, dst):
    for root in (os.path.join(P601, "workspaces"), os.path.join(H, "fixtures", "workspaces")):
        src = os.path.join(root, name)
        if os.path.isdir(src):
            shutil.copytree(src, dst)
            break
    else:
        raise SystemExit(f"no workspace {name}")
    git = ["git", "-C", dst, "-c", "user.name=stand", "-c", "user.email=stand@example.com"]
    subprocess.run(["git", "init", "-q", "-b", "main", dst], check=True)
    subprocess.run(git + ["add", "-A"], check=True)
    subprocess.run(git + ["commit", "-q", "-m", "fixture"], check=True)
    subprocess.run(["git", "-C", dst, "remote", "add", "origin", f"git@example.com:me/{name}.git"], check=True)


def env_for(run, inputs, arm):
    home = os.path.join(run, "home")
    binary = os.path.join(inputs, f"memcue-{arm}")
    bindir = os.path.join(run, "bin")
    os.makedirs(bindir, exist_ok=True)
    if not os.path.exists(os.path.join(bindir, "memcue")):
        os.symlink(binary, os.path.join(bindir, "memcue"))
    keep = {k: os.environ[k] for k in ("LANG", "TERM") if k in os.environ}
    return keep | {
        "PATH": bindir + ":/usr/local/bin:/usr/bin:/bin", "HOME": home, "CLAUDE_CONFIG_DIR": os.path.join(home, ".claude"),
        "XDG_CONFIG_HOME": os.path.join(home, ".config"), "XDG_DATA_HOME": os.path.join(home, ".local", "share"),
        "XDG_STATE_HOME": os.path.join(home, ".local", "state"), "XDG_CACHE_HOME": os.path.join(home, ".cache"),
        "TMPDIR": os.path.join(run, "tmp"), "MEMCUE_FACTS": os.path.join(run, "base", "facts"),
        "MEMCUE_STATE_DIR": os.path.join(run, "state"), "MEMCUE_BIN": binary, "MEMCUE_AUTO_INSTALL": "0",
        "MEMCUE_DREAM_BACKGROUND": "0", "CLAUDE_CODE_DISABLE_AUTO_MEMORY": "1", "DISABLE_TELEMETRY": "1",
        "DISABLE_ERROR_REPORTING": "1", "DISABLE_AUTOUPDATER": "1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
        "CLAUDE_CODE_OAUTH_TOKEN": open(os.path.join(inputs, "claude-token")).read().strip(),
    }


def claude(prompt, cwd, env, out, inputs, *extra):
    cmd = [CLAUDE, "-p", prompt, "--output-format", "stream-json", "--verbose", "--include-hook-events",
           "--model", "sonnet", "--permission-mode", "bypassPermissions", "--setting-sources", "",
           "--strict-mcp-config", "--mcp-config", '{"mcpServers":{}}', "--plugin-dir", os.path.join(inputs, "plugin"),
           "--disallowedTools", DENIED, "--max-budget-usd", "0.60", *extra]
    with open(out, "w") as f:
        r = subprocess.run(cmd, cwd=cwd, env=env, stdout=f, stderr=subprocess.PIPE, text=True,
                           stdin=subprocess.DEVNULL, timeout=900)
    return r.returncode, r.stderr[-2000:]


def session_id(stream):
    for line in open(stream):
        try:
            e = json.loads(line)
        except ValueError:
            continue
        if e.get("type") == "system" and e.get("subtype") == "init":
            return e.get("session_id")
    return None


def snap(run, name):
    src = os.path.join(run, "base", "facts")
    if os.path.isdir(src):
        shutil.copytree(src, os.path.join(run, "rec", f"base-{name}"))


def prepare(run, inputs, arm):
    shutil.rmtree(run, ignore_errors=True)
    for d in ("rec", "tmp", "state", "home/.claude", "base"):
        os.makedirs(os.path.join(run, d), exist_ok=True)
    return env_for(run, inputs, arm)


def capture(out, inputs, arm, sc, rep):
    run = os.path.join(out, "runs", f"{arm}-{sc['id']}-{rep}-capture")
    if os.path.exists(os.path.join(run, "rec", "done.json")):
        return run
    env = prepare(run, inputs, arm)
    ws = os.path.join(run, "ws", sc["workspace"])
    workspace(sc["workspace"], ws)
    rec = os.path.join(run, "rec")
    code, err = claude(sc["turn1"], ws, env, os.path.join(rec, "turn1.jsonl"), inputs)
    sid = session_id(os.path.join(rec, "turn1.jsonl"))
    code2, err2 = (None, "")
    snap(run, "after1")
    if code == 0 and sid:
        code2, err2 = claude(sc["turn2"], ws, env, os.path.join(rec, "turn2.jsonl"), inputs, "--resume", sid)
    snap(run, "after2")
    json.dump({"arm": arm, "scenario": sc["id"], "rep": rep, "phase": "capture", "exit1": code, "exit2": code2,
               "stderr": (err + err2)[-2000:]}, open(os.path.join(rec, "done.json"), "w"))
    return run


def probe(out, inputs, arm, sc, rep, cap):
    run = os.path.join(out, "runs", f"{arm}-{sc['id']}-{rep}-probe")
    if os.path.exists(os.path.join(run, "rec", "done.json")):
        return run
    env = prepare(run, inputs, arm)
    shutil.rmtree(os.path.join(run, "base"))
    left = os.path.join(cap, "rec", "base-after2")
    os.makedirs(os.path.join(run, "base"))
    if os.path.isdir(left):
        shutil.copytree(left, os.path.join(run, "base", "facts"))
    ws = os.path.join(run, "ws", sc["probe"]["workspace"])
    workspace(sc["probe"]["workspace"], ws)
    snap(run, "before")
    code, err = claude(sc["probe"]["prompt"], ws, env, os.path.join(run, "rec", "probe.jsonl"), inputs,
                       "--no-session-persistence")
    snap(run, "after")
    json.dump({"arm": arm, "scenario": sc["id"], "rep": rep, "phase": "probe", "exit": code, "stderr": err[-2000:]},
              open(os.path.join(run, "rec", "done.json"), "w"))
    return run


def one(args):
    out, inputs, arm, sc, rep = args
    try:
        cap = capture(out, inputs, arm, sc, rep)
        if sc.get("probe"):
            probe(out, inputs, arm, sc, rep, cap)
        return f"ok {arm} {sc['id']} {rep}"
    except Exception as e:  # one failed run must not stop the grid
        return f"FAIL {arm} {sc['id']} {rep}: {e}"


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--inputs", required=True)
    ap.add_argument("--out", required=True)
    ap.add_argument("--reps", type=int, default=3)
    ap.add_argument("--jobs", type=int, default=4)
    ap.add_argument("--arms", default="shipped,cand")
    ap.add_argument("--scenarios", default="", help="comma-separated ids; all by default")
    a = ap.parse_args()
    only = set(filter(None, a.scenarios.split(",")))
    jobs = [(os.path.abspath(a.out), os.path.abspath(a.inputs), arm, sc, rep)
            for rep in range(1, a.reps + 1) for sc in scenarios() for arm in a.arms.split(",")
            if not only or sc["id"] in only]
    with ThreadPoolExecutor(a.jobs) as ex:
        for res in ex.map(one, jobs):
            print(res, flush=True)


if __name__ == "__main__":
    sys.exit(main())
