#!/usr/bin/env python3
"""One #606 run: a real prompt, one card arm, `claude -p` in plan mode on a clone of the repository as of the prompt.

    run606.py snap  --data DATA                      # snapshots of the text history, one a prompt
    run606.py one   --data DATA --prompt-id N --arm current|trim --rep R
    run606.py all   --data DATA --reps 2 --jobs 4
    run606.py cards --data DATA                      # the facts each prompt's card carries, from session-start

DATA holds prompts.json (the chosen prompts), keep.txt (the fact ids the trimmed card keeps), memcue-606 (memcue of
main with card-keep.patch, built with -X main.version= the plugin's version, or the plugin warns at every session
start that the binary is older and the agent goes digging) and gets snap/, runs/ and cards.json. The two arms differ only in the card: current is the
card of main; trim is the same card with MEMCUE_CARD_KEEP=keep.txt. Everything else is the live setup of a session:
the memcue plugin with its session-start note, the agent's own `memcue search` over the snapshot (shim-pull lets
search through and logs it), the user's settings and hooks. Writes are impossible: plan mode, read-only tools, shims
that refuse ssh, gh, mutating git and the like, and a throwaway copy of the snapshot for every run.

The snapshot is the text history as of the prompt: every version created after it is dropped, and a fact with none
left is gone. A relabel keeps the creation time of the fact (store.Relabel), so a scope set later can show through.
"""
import argparse, hashlib, json, os, random, shutil, subprocess, sys, time
from concurrent.futures import ThreadPoolExecutor

H = os.path.dirname(os.path.abspath(__file__))
E2E = os.path.join(H, "..", "e2e")
# Everything a run sees on PATH and in its environment lives under a neutral directory: the first runs read the
# experiment off the path of the shims and of the data directory.
BIN = "/private/tmp/cw/bin"
SHIM = os.path.join(BIN, "shim")  # ssh, glab, gh, mutating git and the like fail inside a run
SHIM_SEARCH = os.path.join(BIN, "search")  # memcue: search only, logged
HOME = os.path.expanduser("~")
# The live repositories, the live base and this experiment's data hold what was learnt after the prompt: the file
# tools may not read them. Bash still can; run_one counts the runs whose commands named them.
LIVE_PATHS = [f"{HOME}/src", f"{HOME}/research", f"{HOME}/.claude/memory", f"{HOME}/.local/state/memcue"]
CLAUDE_BIN = shutil.which("claude")
LIVE = os.path.expanduser("~/.claude/memory/facts")
CLAUDE = os.path.expanduser("~/.claude")
TOOLS = "Read,Grep,Glob,Bash"
DENY = ["Bash(ssh *)", "Bash(scp *)", "Bash(rsync *)", "Bash(glab *)", "Bash(gh *)", "Bash(atlassian *)",
        "Bash(curl *)", "Bash(wget *)", "Bash(claude *)", "Bash(docker *)", "Bash(git push *)",
        "Bash(git commit *)", "Bash(git merge *)", "Bash(git rebase *)", "Bash(git reset *)", "Bash(git checkout *)",
        "Bash(git pull *)", "Bash(git fetch *)", "Bash(rm *)", "Bash(sqlite3 *)", "Bash(brew *)", "Bash(sudo *)",
        *[f"{tool}(/{p}/**)" for p in LIVE_PATHS for tool in ("Read", "Grep", "Glob")]]


def install_bin(data):
    """The shims, the binary and the keep list under BIN, once."""
    os.makedirs(BIN, exist_ok=True)
    if not os.path.isdir(SHIM):
        shutil.copytree(os.path.join(H, "shim"), SHIM)
    if not os.path.isdir(SHIM_SEARCH):
        shutil.copytree(os.path.join(H, "search"), SHIM_SEARCH)
    # Once, and through a rename: a binary copied over itself while other runs execute it is killed by macOS
    # (Killed: 9), and the first full run lost its memory in 22 of 160 sessions that way.
    for src, name in ((os.path.join(data, "memcue-606"), "memcue-bin"), (os.path.join(data, "keep.txt"), "keep")):
        dst = os.path.join(BIN, name)
        if not os.path.exists(dst):
            shutil.copy2(src, dst + ".tmp")
            os.replace(dst + ".tmp", dst)
ARMS = ("current", "trim")


def sh(*cmd, **kw):
    return subprocess.run(cmd, check=True, capture_output=True, text=True, **kw).stdout.strip()


def prompts(data):
    return {p["id"]: p for p in json.load(open(os.path.join(data, "prompts.json")))}


def snapshot(data, p):
    """The text history as of the prompt, built once."""
    d = os.path.join(data, "snap", str(p["id"]), "facts")
    if os.path.exists(d):
        return d
    tmp = d + ".tmp"
    shutil.rmtree(tmp, ignore_errors=True)
    kept = 0
    for sub in os.listdir(LIVE):
        src = os.path.join(LIVE, sub)
        if not os.path.isdir(src):
            continue
        for name in os.listdir(src):
            lines = [l for l in open(os.path.join(src, name)) if l.strip()]
            keep = [l for l in lines if json.loads(l)["created"] <= p["time"]]
            if not keep:
                continue
            os.makedirs(os.path.join(tmp, sub), exist_ok=True)
            with open(os.path.join(tmp, sub, name), "w") as f:
                f.writelines(keep)
            kept += 1
    os.replace(tmp, d)
    return d


def default_branch(root):
    head = subprocess.run(["git", "-C", root, "symbolic-ref", "-q", "refs/remotes/origin/HEAD"], capture_output=True,
                          text=True).stdout.strip()
    if head:
        return head.rsplit("/", 1)[-1]
    for b in ("main", "master"):
        if subprocess.run(["git", "-C", root, "rev-parse", "-q", "--verify", f"refs/heads/{b}"],
                          capture_output=True).returncode == 0:
            return b
    return "main"


def clone(p, dst):
    """The repository as of the prompt, and nothing later: an empty repository that fetches only the history of the
    prompt's commit and of the default branch's last commit before it. A clone of the whole repository (--shared, or
    with its refs cut back) still carried the later objects, and an agent read today's commits out of them."""
    root = p["root"]
    rel = os.path.relpath(os.path.realpath(p["cwd"]), os.path.realpath(sh("git", "-C", p["cwd"], "rev-parse",
                                                                          "--show-toplevel")))
    commit = sh("git", "-C", root, "rev-list", "-1", f"--before={p['time']}", "--all")
    default = default_branch(root)
    tip = sh("git", "-C", root, "rev-list", "-1", f"--before={p['time']}", default) or commit
    target = os.path.join(dst, os.path.basename(root.rstrip("/")).lstrip(".") or "repo")
    sh("git", "init", "-q", "-b", default, target)
    sh("git", "-C", target, "fetch", "-q", "--no-tags", "--upload-pack",
       "git -c uploadpack.allowAnySHA1InWant=true upload-pack", root, commit, tip)
    sh("git", "-C", target, "update-ref", f"refs/heads/{default}", tip)
    sh("git", "-C", target, "update-ref", f"refs/remotes/origin/{default}", tip)
    sh("git", "-C", target, "checkout", "-q", "--detach", commit)
    sh("git", "-C", target, "reflog", "expire", "--expire=now", "--all")
    for line in sh("git", "-C", root, "remote", "-v").splitlines():  # memcue keys a session by its remotes
        name, url = line.split()[:2]
        sh("git", "-C", target, "config", f"remote.{name}.url", url)
    cwd = os.path.normpath(os.path.join(target, rel))
    os.makedirs(cwd, exist_ok=True)
    return cwd, commit


def workdir(key):
    """A neutral path for a run's clone: the pilot's agent read the experiment off a path under DATA."""
    return os.path.join("/private/tmp/cw", hashlib.sha256(key.encode()).hexdigest()[:8])


def setup(data, p, arm, work):
    """The clone, the throwaway base and the environment of one run."""
    shutil.rmtree(work, ignore_errors=True)
    os.makedirs(work)
    cwd, commit = clone(p, work)
    base = os.path.join(work, "facts")
    shutil.copytree(snapshot(data, p), base)
    state = os.path.join(work, "state")
    install_bin(data)
    binary = os.path.join(BIN, "memcue-bin")
    senv = {"MEMCUE_FACTS": base, "MEMCUE_STATE_DIR": state, "MEMCUE_BIN": binary, "MEMCUE_REAL": binary,
            "MEMCUE_DREAM_BACKGROUND": "0", "MEMCUE_AUTO_INSTALL": "0",
            "PATH": SHIM_SEARCH + ":" + SHIM + ":" + os.environ["PATH"]}
    if arm == "trim":
        senv["MEMCUE_CARD_KEEP"] = os.path.join(BIN, "keep")
    env = {k: v for k, v in os.environ.items() if not k.startswith(("MEMCUE_", "CLAUDECODE"))}
    env.update(senv)
    settings = {"autoMemoryEnabled": False, "enabledPlugins": {"memcue@memcue": True}, "env": senv}
    sfile = os.path.join(work, "settings.json")
    json.dump(settings, open(sfile, "w"))
    return cwd, commit, env, sfile, state


def run_one(data, pid, arm, rep):
    p = prompts(data)[pid]
    out_dir = os.path.join(data, "runs", str(pid))
    os.makedirs(out_dir, exist_ok=True)
    out = os.path.join(out_dir, f"{arm}-{rep}.jsonl")
    if os.path.exists(out) and os.path.getsize(out) > 0:
        return out
    work = workdir(f"{pid}-{arm}-{rep}")
    cwd, commit, env, sfile, state = setup(data, p, arm, work)
    start = time.time()
    with open(out + ".tmp", "w") as f:
        r = subprocess.run([CLAUDE_BIN, "-p", "--model", "opus", "--permission-mode", "plan", "--settings", sfile,
                            "--tools", TOOLS, "--strict-mcp-config", "--disallowedTools", *DENY,
                            "--output-format", "stream-json", "--verbose", "--max-turns", "40"],
                           input=p["prompt"], stdout=f, stderr=subprocess.PIPE, text=True, cwd=cwd, env=env,
                           timeout=1500)
    meta = {"pid": pid, "arm": arm, "rep": rep, "exit": r.returncode, "stderr": r.stderr[-2000:], "commit": commit,
            "cwd": cwd, "seconds": round(time.time() - start, 1)}
    cmds = []
    for line in open(out + ".tmp"):
        e = json.loads(line) if line.strip().startswith("{") else {}
        for c in (e.get("message") or {}).get("content") or [] if e.get("type") == "assistant" else []:
            if isinstance(c, dict) and c.get("type") == "tool_use" and c.get("name") == "Bash":
                cmds.append(c["input"].get("command", ""))
    meta["live_bash"] = [c for c in cmds if any(p in c for p in LIVE_PATHS) or "~/src" in c or "~/research" in c
                         or "~/.claude/memory" in c]
    for name in ("search", "card"):
        lp = os.path.join(state, f"{name}.log")
        if os.path.exists(lp):
            meta[name] = [l.rstrip("\n") for l in open(lp)]
    json.dump(meta, open(os.path.join(out_dir, f"{arm}-{rep}.meta.json"), "w"), ensure_ascii=False, indent=1)
    os.replace(out + ".tmp", out)
    shutil.rmtree(work, ignore_errors=True)
    shutil.rmtree(os.path.join(CLAUDE, "projects", cwd.replace("/", "-").replace(".", "-")), ignore_errors=True)
    return out


def cards(data):
    """The card each prompt's session starts with in both arms: session-start on the clone and the snapshot."""
    res = {}
    for pid, p in sorted(prompts(data).items()):
        res[pid] = {}
        for arm in ARMS:
            work = workdir(f"card-{pid}-{arm}")
            cwd, _, env, _, state = setup(data, p, arm, work)
            chars = 0
            for part in (1, 2, 3):
                o = subprocess.run([env["MEMCUE_BIN"], "session-start", "-part", str(part)], cwd=cwd, env=env,
                                   input=json.dumps({"session_id": f"card-{pid}", "cwd": cwd}), capture_output=True,
                                   text=True, check=True).stdout
                if o.strip():
                    chars += len(json.loads(o)["hookSpecificOutput"]["additionalContext"])
            ids = set()
            lp = os.path.join(state, "card.log")
            for l in open(lp) if os.path.exists(lp) else []:
                ids.update(json.loads(l).get("facts") or [])
            res[pid][arm] = {"ids": sorted(ids), "chars": chars}
            shutil.rmtree(work, ignore_errors=True)
        print(pid, {a: len(v["ids"]) for a, v in res[pid].items()}, flush=True)
    json.dump(res, open(os.path.join(data, "cards.json"), "w"), indent=1)


def main():
    ap = argparse.ArgumentParser()
    sub = ap.add_subparsers(dest="cmd", required=True)
    for name in ("snap", "one", "all", "cards"):
        s = sub.add_parser(name)
        s.add_argument("--data", required=True)
        if name == "one":
            s.add_argument("--prompt-id", type=int, required=True)
            s.add_argument("--arm", choices=ARMS, required=True)
            s.add_argument("--rep", type=int, default=1)
        if name == "all":
            s.add_argument("--reps", type=int, default=2)
            s.add_argument("--jobs", type=int, default=4)
    a = ap.parse_args()
    data = os.path.abspath(a.data)
    if a.cmd == "snap":
        for p in prompts(data).values():
            snapshot(data, p)
        print("snapshots", len(prompts(data)))
    elif a.cmd == "one":
        print(run_one(data, a.prompt_id, a.arm, a.rep))
    elif a.cmd == "cards":
        cards(data)
    else:
        jobs = []
        rnd = random.Random(7)
        for rep in range(1, a.reps + 1):
            for pid in sorted(prompts(data)):
                arms = list(ARMS)
                rnd.shuffle(arms)
                jobs += [(pid, arm, rep) for arm in arms]

        install_bin(data)  # once: four jobs copying it at once raced

        def go(j):
            try:
                return run_one(data, *j)
            except Exception as e:  # one failed run must not stop the rest
                return f"FAIL {j}: {e}"
        with ThreadPoolExecutor(a.jobs) as ex:
            for res in ex.map(go, jobs):
                print(res, flush=True)


if __name__ == "__main__":
    sys.exit(main())
