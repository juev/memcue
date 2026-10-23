#!/usr/bin/env python3
"""One e2e run: a real prompt, one memory mode, `claude -p` in plan mode on a clone of the repository as of the prompt.

    run.py one  --prompt-id N --arm none|files|memcue --rep R
    run.py all  --ids ids.json --reps 2 --jobs 4

Modes differ only in memory. none: memcue hook silenced (MEMCUE_BIN=true), built-in memory off. files: built-in memory
on, the project's memory dir a symlink into the snapshot laid out as notes, _global injected by the old memory-sync.sh.
memcue: the plugin hook over a copy of the snapshot base, built-in memory off, extraction off, no reranker (the words
alone). judge: memcue with the Jev reranker, its endpoint taken from MEMCUE_JUDGE_* of the calling environment.
"""
import argparse, json, os, random, shutil, subprocess, sys, time
from concurrent.futures import ThreadPoolExecutor

E = os.path.dirname(os.path.abspath(__file__))
PROMPTS = os.path.join(E, "prompts-all.json")
SHIM = os.path.join(E, "shim")  # ssh, glab, gh, mutating git and the like fail inside a run
MEMCUE = os.path.expanduser("~/.local/bin/memcue")
CLAUDE_BIN = shutil.which("claude")  # resolved before the shims go on PATH
TOOLS = "Read,Grep,Glob,Bash"
DENY = ["Bash(ssh *)", "Bash(scp *)", "Bash(rsync *)", "Bash(glab *)", "Bash(gh *)", "Bash(atlassian *)",
        "Bash(curl *)", "Bash(wget *)", "Bash(memcue *)", "Bash(claude *)", "Bash(docker *)", "Bash(git push *)",
        "Bash(git commit *)", "Bash(git merge *)", "Bash(git rebase *)", "Bash(git reset *)", "Bash(git checkout *)",
        "Bash(git pull *)", "Bash(git fetch *)", "Bash(rm *)", "Bash(sqlite3 *)", "Bash(brew *)", "Bash(sudo *)"]
ARCHIVE = os.path.expanduser("~/.claude/archive/memory-2026-10-19")
CLAUDE = os.path.expanduser("~/.claude")
ARMS = ("none", "files", "memcue", "judge")
JUDGE_ENV = ("MEMCUE_JUDGE_URL", "MEMCUE_JUDGE_MODEL", "MEMCUE_JUDGE_KEY")


def sh(*cmd, **kw):
    return subprocess.run(cmd, check=True, capture_output=True, text=True, **kw).stdout.strip()


def main_root(cwd):
    common = sh("git", "-C", cwd, "rev-parse", "--path-format=absolute", "--git-common-dir")
    return os.path.dirname(common)


def tier_of(root):
    if root == CLAUDE:
        return "_global"
    base = os.path.basename(root)
    return f"{os.path.basename(os.path.dirname(root))}-{base[1:]}" if base.startswith(".") else base


def snapshot(p):
    d = os.path.join(E, "snap", str(p["id"]))
    if not os.path.exists(os.path.join(d, "store", "_global", "MEMORY.md")):
        sh(sys.executable, os.path.join(E, "snap.py"), "base", "--src", os.path.join(E, "live.db"), "--at", p["time"],
           "--out", os.path.join(d, "facts.db"))
        sh(sys.executable, os.path.join(E, "snap.py"), "files", "--db", os.path.join(d, "facts.db"), "--archive",
           ARCHIVE, "--out", os.path.join(d, "store"))
    return d


def clone(p, dst):
    root = main_root(p["cwd"])
    rel = os.path.relpath(os.path.realpath(p["cwd"]), os.path.realpath(sh("git", "-C", p["cwd"], "rev-parse",
                                                                          "--show-toplevel")))
    commit = sh("git", "-C", root, "rev-list", "-1", f"--before={p['time']}", "HEAD")
    name = os.path.basename(root)
    target = os.path.join(dst, name)
    sh("git", "clone", "-q", "--shared", "--no-checkout", root, target)
    sh("git", "-C", target, "checkout", "-q", "--detach", commit)
    trim_refs(target, p["time"])
    return root, os.path.normpath(os.path.join(target, rel)), commit


def trim_refs(repo, at):
    """Make the clone's refs what they were at the prompt: a branch goes back to its last commit before `at`, a tag
    newer than `at` goes away. Without this the clone carries tags and branch tips of later days (run 1 leaked them)."""
    refs = sh("git", "-C", repo, "for-each-ref", "--format=%(refname) %(creatordate:unix)",
              "refs/heads", "refs/remotes", "refs/tags").splitlines()
    limit = int(sh("date", "-j", "-u", "-f", "%Y-%m-%dT%H:%M:%S", at[:19], "+%s"))
    for line in refs:
        ref, created = line.rsplit(" ", 1)
        if ref.endswith("/HEAD"):
            continue
        if ref.startswith("refs/tags/"):
            if int(created or 0) > limit:
                sh("git", "-C", repo, "update-ref", "-d", ref)
            continue
        old = sh("git", "-C", repo, "rev-list", "-1", f"--before={at}", ref)
        if old:
            sh("git", "-C", repo, "update-ref", ref, old)
        else:
            sh("git", "-C", repo, "update-ref", "-d", ref)
    sh("git", "-C", repo, "reflog", "expire", "--expire=now", "--all")


def slug(path):
    return path.replace("/", "-").replace(".", "-")


def run_one(pid, arm, rep):
    prompts = {p["id"]: p for p in json.load(open(PROMPTS))}
    p = prompts[pid]
    out_dir = os.path.join(E, "runs", str(pid))
    os.makedirs(out_dir, exist_ok=True)
    out = os.path.join(out_dir, f"{arm}-{rep}.jsonl")
    if os.path.exists(out) and os.path.getsize(out) > 0:
        return out
    snap = snapshot(p)
    work = os.path.realpath(os.path.join(E, "wt", f"{pid}-{arm}-{rep}"))
    shutil.rmtree(work, ignore_errors=True)
    os.makedirs(work)
    root, cwd, commit = clone(p, work)
    tier = tier_of(root)
    state = os.path.join(work, "state")
    env = {k: v for k, v in os.environ.items() if not k.startswith(("MEMCUE_", "MEMSYNC_", "CLAUDECODE"))}
    base = os.path.join(work, "facts.db")  # every mode gets a throwaway base: nothing reaches the live one
    shutil.copyfile(os.path.join(snap, "facts.db"), base)
    senv = {"MEMCUE_EXTRACT": "0", "MEMCUE_DREAM": "0", "MEMCUE_RECALL_LOG": "1", "MEMCUE_STATE_DIR": state,
            "MEMCUE_FACTS": base, "PATH": SHIM + ":" + os.environ["PATH"]}
    # The plugin is on in every mode (none and files silence it through MEMCUE_BIN), whatever the user settings say
    settings = {"autoMemoryEnabled": False, "enabledPlugins": {"memcue@memcue": True}}
    link = None
    senv["MEMCUE_BIN"] = MEMCUE if arm in ("memcue", "judge") else "/usr/bin/true"
    senv["MEMCUE_RERANK"] = "off"  # memcue: the words alone, as in the first three modes' run
    if arm == "judge":  # the same hook with the Jev reranker of the live setup
        missing = [k for k in JUDGE_ENV if not os.environ.get(k)]
        if missing:
            raise SystemExit(f"judge mode needs {', '.join(missing)}")
        senv.update({k: os.environ[k] for k in JUDGE_ENV}, MEMCUE_RERANK="judge")
    if arm == "files":
        store = os.path.join(work, "store")
        shutil.copytree(os.path.join(snap, "store"), store)
        os.makedirs(os.path.join(store, tier), exist_ok=True)
        settings["autoMemoryEnabled"] = True
        senv.update(MEMSYNC_STORE=store, MEMSYNC_TIER=tier)
        hook = os.path.join(E, "memory-sync.sh")
        settings["hooks"] = {"SessionStart": [{"hooks": [
            {"type": "command", "command": f"bash {hook}" + ("" if n == 1 else f" {n}"), "timeout": 10}
            for n in (1, 2, 3)]}]}
        link = os.path.join(CLAUDE, "projects", slug(cwd), "memory")
        os.makedirs(os.path.dirname(link), exist_ok=True)
        os.symlink(os.path.join(store, tier), link)
    settings["env"] = senv
    env.update(senv)
    sfile = os.path.join(work, "settings.json")
    json.dump(settings, open(sfile, "w"))
    start = time.time()
    with open(out + ".tmp", "w") as f:
        r = subprocess.run([CLAUDE_BIN, "-p", "--model", "opus", "--permission-mode", "plan", "--settings", sfile,
                            "--tools", TOOLS, "--strict-mcp-config", "--disallowedTools", *DENY,
                            "--output-format", "stream-json", "--verbose", "--max-turns", "40"],
                           input=p["prompt"], stdout=f, stderr=subprocess.PIPE, text=True, cwd=cwd, env=env,
                           timeout=1200)
    meta = {"pid": pid, "arm": arm, "rep": rep, "exit": r.returncode, "stderr": r.stderr[-2000:], "tier": tier,
            "commit": commit, "cwd": cwd, "seconds": round(time.time() - start, 1)}
    rl = os.path.join(state, "recall.log")
    meta["recall"] = [json.loads(l) for l in open(rl)] if os.path.exists(rl) else []
    json.dump(meta, open(os.path.join(out_dir, f"{arm}-{rep}.meta.json"), "w"), ensure_ascii=False, indent=1)
    os.replace(out + ".tmp", out)
    if link:
        os.unlink(link)
    shutil.rmtree(os.path.join(CLAUDE, "projects", slug(cwd)), ignore_errors=True)
    shutil.rmtree(work, ignore_errors=True)
    return out


def cmd_one(a):
    print(run_one(a.prompt_id, a.arm, a.rep))


def cmd_all(a):
    ids = json.load(open(a.ids))
    rnd = random.Random(7)
    jobs = []
    for rep in range(1, a.reps + 1):
        for pid in ids:
            arms = list(ARMS)
            rnd.shuffle(arms)
            jobs += [(pid, arm, rep) for arm in arms]
    for pid in {j[0] for j in jobs}:  # snapshots first, so parallel runs do not race on one
        snapshot({p["id"]: p for p in json.load(open(PROMPTS))}[pid])

    def go(j):
        try:
            return run_one(*j)
        except Exception as e:  # one failed run must not stop the rest
            return f"FAIL {j}: {e}"
    with ThreadPoolExecutor(a.jobs) as ex:
        for res in ex.map(go, jobs):
            print(res, flush=True)


ap = argparse.ArgumentParser()
sub = ap.add_subparsers(dest="cmd", required=True)
o = sub.add_parser("one"); o.add_argument("--prompt-id", type=int, required=True)
o.add_argument("--arm", choices=ARMS, required=True); o.add_argument("--rep", type=int, default=1)
o.set_defaults(func=cmd_one)
al = sub.add_parser("all"); al.add_argument("--ids", required=True); al.add_argument("--reps", type=int, default=2)
al.add_argument("--jobs", type=int, default=4); al.set_defaults(func=cmd_all)
args = ap.parse_args()
args.func(args)
