#!/usr/bin/env python3
"""Measure the object store of a scratch git repository for #530.

Every function here runs git on a scratch repository the caller created. Nothing in this module reads or writes a
repository it did not get as an argument, and none of it opens a network connection: transfers are measured with
pack-objects, the program that builds what a push or a fetch sends, and with pushes and fetches over file://.

The numbers it returns:

- logical: the sum of the sizes of the objects (what `git cat-file -s` says);
- disk: bytes the object store takes, by apparent size of the files, loose and packed;
- thin pack per commit: what a push of one commit sends to a remote that has the previous one;
- full pack: what a clone of the whole history sends.

Peak memory is the ru_maxrss that wait4 reports for the git process, which includes the children git waited for
(pack-objects under gc, receive-pack under a local push).

Safety. Every subprocess gets clean_env(): no GIT_* variable of the caller (GIT_DIR, GIT_WORK_TREE,
GIT_OBJECT_DIRECTORY, GIT_ALTERNATE_OBJECT_DIRECTORIES, GIT_INDEX_FILE, GIT_CONFIG_*, GIT_TEMPLATE_DIR and the rest)
reaches git, and neither the system nor the global config is read; hooks and fsmonitor are switched off on the
command line. Copies and remotes go to new paths only: an existing path is refused, never replaced, and only what
this module created is removed.

The repository itself is not trusted either. A `.git` that is a symlink or a gitfile, objects/info/alternates, a
commondir, core.worktree or an include in its config would send git, and `git gc --prune=now` with it, into a
repository outside the scratch directory. check_repo() fails closed on all of that: before measure() and
sync_replay() touch a repository, and again on every copy before gc runs on it, the whole repository, work tree and
git directory, must lie inside the scratch directory, hold only regular files and directories of this user (no
symlink, no hard link), and look like what `git init` plus commits writes; git's own idea of the git directory must
agree. Anything else is refused, not repaired.
"""

from __future__ import annotations

import json
import os
import shutil
import subprocess
import tempfile
import time
from dataclasses import dataclass, field
from pathlib import Path

BENCH_ENV = {
    "GIT_AUTHOR_NAME": "bench",
    "GIT_AUTHOR_EMAIL": "bench@example.invalid",
    "GIT_COMMITTER_NAME": "bench",
    "GIT_COMMITTER_EMAIL": "bench@example.invalid",
    "GIT_CONFIG_NOSYSTEM": "1",
    "GIT_CONFIG_GLOBAL": os.devnull,
    "GIT_TERMINAL_PROMPT": "0",
    # Command-line config wins over the repository's: no hook and no fsmonitor of a repository runs.
    "GIT_CONFIG_COUNT": "2",
    "GIT_CONFIG_KEY_0": "core.hooksPath",
    "GIT_CONFIG_VALUE_0": os.devnull,
    "GIT_CONFIG_KEY_1": "core.fsmonitor",
    "GIT_CONFIG_VALUE_1": "false",
    "LC_ALL": "C",
}

# What `git init`, commits, pushes, fetches and gc leave in a git directory. Anything else (commondir, gitdir,
# worktrees, modules, shallow, config.worktree, ...) is refused.
GITDIR_ENTRIES = {"HEAD", "config", "description", "hooks", "info", "logs", "objects", "refs", "branches", "index",
                  "COMMIT_EDITMSG", "packed-refs", "ORIG_HEAD", "FETCH_HEAD"}
# objects/info: alternates and http-alternates are not here.
OBJECTS_INFO_ENTRIES = {"packs", "commit-graph", "commit-graphs"}
# The config keys synth and init_repo set (lower case, as git config --list prints them).
CONFIG_KEYS = {"core.repositoryformatversion", "core.filemode", "core.bare", "core.logallrefupdates", "core.fsync",
               "core.autocrlf", "gc.auto", "transfer.unpacklimit"}


class UnsafeRepo(Exception):
    """A repository check_repo refuses."""


def _inside(path: str, root: str) -> bool:
    return path == root or path.startswith(root.rstrip(os.sep) + os.sep)


def check_scratch(scratch: Path) -> str:
    """scratch must be a real directory of this user, not a symlink; returns its real path."""
    st = os.lstat(scratch)
    if not os.path.isdir(scratch) or os.path.islink(scratch) or st.st_uid != os.getuid():
        raise UnsafeRepo(f"{scratch} is not a directory of this user (or it is a symlink)")
    return os.path.realpath(scratch)


def check_repo(repo: Path, scratch: Path, bare: bool = False) -> Path:
    """Refuse repo unless it is a plain repository wholly inside scratch (see the module doc); return its git dir."""
    root = check_scratch(scratch)
    if os.path.islink(repo) or not os.path.isdir(repo):
        raise UnsafeRepo(f"{repo} is a symlink or not a directory")
    real = os.path.realpath(repo)
    if real == root or not _inside(real, root):
        raise UnsafeRepo(f"{repo} is not inside {scratch}")
    uid = os.getuid()
    for dirpath, dirnames, filenames in os.walk(real):
        for n in dirnames + filenames:
            p = os.path.join(dirpath, n)
            st = os.lstat(p)
            if st.st_uid != uid:
                raise UnsafeRepo(f"{p} belongs to another user")
            if os.path.islink(p):
                raise UnsafeRepo(f"{p} is a symlink")
            if n in filenames and not (os.path.isfile(p) and st.st_nlink == 1):
                raise UnsafeRepo(f"{p} is not a regular file with a single link")
    gd = Path(real) if bare else Path(real) / ".git"
    if not gd.is_dir():
        raise UnsafeRepo(f"{gd} is not a directory (a gitfile is refused)")
    extra = sorted(set(os.listdir(gd)) - GITDIR_ENTRIES)
    if extra:
        raise UnsafeRepo(f"{gd} holds {extra}, which this tooling never writes")
    info = gd / "objects" / "info"
    extra = sorted(set(os.listdir(info)) - OBJECTS_INFO_ENTRIES) if info.is_dir() else []
    if extra:
        raise UnsafeRepo(f"{info} holds {extra} (alternates are refused)")
    hooks = [n for n in os.listdir(gd / "hooks") if not n.endswith(".sample")] if (gd / "hooks").is_dir() else []
    if hooks:
        raise UnsafeRepo(f"{gd / 'hooks'} holds hooks {sorted(hooks)}")
    keys = run(["git", "config", "--file", str(gd / "config"), "--list", "--name-only"]).out.decode().split()
    extra = sorted(set(k.lower() for k in keys) - CONFIG_KEYS)
    if extra:
        raise UnsafeRepo(f"{gd / 'config'} sets {extra}")
    view = run(["git", "-C", real, "rev-parse", "--path-format=absolute", "--absolute-git-dir", "--git-common-dir",
                "--git-path", "objects", "--is-bare-repository"]).out.decode().split("\n")[:4]
    want = [str(gd), str(gd), str(gd / "objects"), "true" if bare else "false"]
    if view != want:
        raise UnsafeRepo(f"git sees {repo} as {view}, expected {want}")
    return gd


def clean_env(extra: dict | None = None) -> dict:
    """The environment of every subprocess: the caller's without any GIT_* variable, then BENCH_ENV, then extra."""
    env = {k: v for k, v in os.environ.items() if not k.startswith("GIT_")}
    env.update(BENCH_ENV)
    if extra:
        env.update(extra)
    return env


def new_dir(path: Path) -> Path:
    """Create path, which must not exist (a symlink counts as existing); its parent must exist."""
    path.mkdir(mode=0o700)
    return path


@dataclass
class Run:
    out: bytes
    seconds: float
    maxrss_kib: int
    code: int
    err: bytes = b""


def run(cmd: list[str], cwd: Path | None = None, stdin: bytes | None = None, env: dict | None = None,
        check: bool = True, stdout_file=None) -> Run:
    """Run cmd and return its output, wall time and peak RSS (KiB, from wait4)."""
    full_env = clean_env(env)
    t0 = time.perf_counter()
    p = subprocess.Popen(cmd, cwd=cwd, env=full_env, stdin=subprocess.PIPE if stdin is not None else subprocess.DEVNULL,
                         stdout=stdout_file if stdout_file is not None else subprocess.PIPE, stderr=subprocess.PIPE)
    out, err = p.communicate(stdin)
    seconds = time.perf_counter() - t0
    if check and p.returncode != 0:
        raise RuntimeError(f"{' '.join(cmd)} failed ({p.returncode}): {err.decode(errors='replace')[-2000:]}")
    # Peak memory is measured only by run_peak, which owns the wait4 of its child.
    return Run(out or b"", seconds, 0, p.returncode, err or b"")


def run_peak(cmd: list[str], cwd: Path | None = None, env: dict | None = None, stdin_path: str | None = None,
             stdout_path: str | None = None, check: bool = True) -> Run:
    """Run cmd under the peakrss wrapper (GITGROWTH_PEAKRSS), so maxrss is the peak of this process tree alone.

    Without the wrapper the figure comes from Python's own wait4 and cannot be lower than the resident set of this
    Python process (a forked child starts with it); such a result is flagged with rss_floor_kib.
    """
    full_env = clean_env(env)
    wrapper = os.environ.get("GITGROWTH_PEAKRSS")
    report = None
    if wrapper:
        fd, report = tempfile.mkstemp(prefix="peakrss-")
        os.close(fd)
        cmd = [wrapper, report, *cmd]
    fin = open(stdin_path, "rb") if stdin_path else open(os.devnull, "rb")
    fout = open(stdout_path, "wb") if stdout_path else open(os.devnull, "wb")
    errf = tempfile.TemporaryFile()
    try:
        t0 = time.perf_counter()
        p = subprocess.Popen(cmd, cwd=cwd, env=full_env, stdin=fin, stdout=fout, stderr=errf)
        _, status, usage = os.wait4(p.pid, 0)
        seconds = time.perf_counter() - t0
        p.returncode = os.waitstatus_to_exitcode(status)
        errf.seek(0)
        err = errf.read()
        maxrss = usage.ru_maxrss
        if report:
            with open(report) as f:
                rep = json.load(f)
            seconds, maxrss = rep["seconds"], rep["maxrss_kib"]
    finally:
        fin.close()
        fout.close()
        errf.close()
        if report:
            os.unlink(report)
    if check and p.returncode != 0:
        raise RuntimeError(f"{' '.join(cmd)} failed ({p.returncode}): {err.decode(errors='replace')[-2000:]}")
    return Run(b"", seconds, maxrss, p.returncode, err)


def git(repo: Path, *args: str, stdin: bytes | None = None, env: dict | None = None, check: bool = True) -> Run:
    return run(["git", "-C", str(repo), *args], stdin=stdin, env=env, check=check)


def init_repo(path: Path, bare: bool = False) -> Path:
    new_dir(path)
    run(["git", "init", "-q", "-b", "main", *(["--bare"] if bare else []), str(path)])
    # No automatic gc: the measurement decides when the store is packed.
    git(path, "config", "gc.auto", "0")
    git(path, "config", "core.fsync", "none")
    return path


def git_dir(repo: Path) -> Path:
    d = repo / ".git"
    return d if d.is_dir() else repo


def store_usage(repo: Path) -> dict:
    """Apparent and allocated bytes of the loose objects and of the packs."""
    objects = git_dir(repo) / "objects"
    res = {"loose_files": 0, "loose_bytes": 0, "loose_alloc": 0, "packs": 0, "pack_bytes": 0, "pack_alloc": 0}
    for p in objects.rglob("*"):
        if not p.is_file():
            continue
        st = p.stat()
        if p.parent.name == "pack":
            if p.suffix in (".pack", ".idx", ".rev", ".bitmap", ".mtimes"):
                res["pack_bytes"] += st.st_size
                res["pack_alloc"] += st.st_blocks * 512
                if p.suffix == ".pack":
                    res["packs"] += 1
        elif len(p.parent.name) == 2:
            res["loose_files"] += 1
            res["loose_bytes"] += st.st_size
            res["loose_alloc"] += st.st_blocks * 512
    res["total_bytes"] = res["loose_bytes"] + res["pack_bytes"]
    res["total_alloc"] = res["loose_alloc"] + res["pack_alloc"]
    return res


def object_stats(repo: Path) -> dict:
    """Per type: count, logical size, size on disk and how many are stored as deltas."""
    r = git(repo, "cat-file", "--batch-all-objects", "--unordered",
            "--batch-check=%(objecttype) %(objectsize) %(objectsize:disk) %(deltabase)")
    zero = "0" * 40
    stats: dict[str, dict] = {}
    for line in r.out.decode().splitlines():
        typ, size, disk, base = line.split()
        s = stats.setdefault(typ, {"count": 0, "logical": 0, "disk": 0, "deltas": 0})
        s["count"] += 1
        s["logical"] += int(size)
        s["disk"] += int(disk)
        if base != zero:
            s["deltas"] += 1
    return stats


def commits(repo: Path, ref: str = "main") -> list[str]:
    return git(repo, "rev-list", "--reverse", ref).out.decode().split()


def thin_pack_bytes(repo: Path, new: str, old: str | None) -> tuple[int, float, int]:
    """Bytes of the pack a push of new sends to a remote that has old; time and peak RSS of pack-objects."""
    with tempfile.NamedTemporaryFile(delete=False) as fin:
        fin.write((new + "\n" + (f"^{old}\n" if old else "")).encode())
        stdin_path = fin.name
    with tempfile.NamedTemporaryFile(delete=False) as fout:
        stdout_path = fout.name
    try:
        r = run_peak(["git", "-C", str(repo), "pack-objects", "--revs", "--thin", "--stdout", "--delta-base-offset",
                      "-q"], stdin_path=stdin_path, stdout_path=stdout_path)
        return os.path.getsize(stdout_path), r.seconds, r.maxrss_kib
    finally:
        os.unlink(stdin_path)
        os.unlink(stdout_path)


def incremental_transfer(repo: Path) -> dict:
    """Sum of the thin packs of every commit pushed one at a time, as a sync after every commit does."""
    cs = commits(repo)
    sizes, secs, peak = [], 0.0, 0
    prev = None
    for c in cs:
        n, s, m = thin_pack_bytes(repo, c, prev)
        sizes.append(n)
        secs += s
        peak = max(peak, m)
        prev = c
    srt = sorted(sizes)
    return {"commits": len(cs), "bytes_total": sum(sizes), "bytes_first": sizes[0] if sizes else 0,
            "bytes_after_first": sum(sizes[1:]), "bytes_median": srt[len(srt) // 2] if srt else 0,
            "bytes_max": max(sizes) if sizes else 0, "seconds": round(secs, 2), "peak_rss_kib": peak}


def full_pack(repo: Path) -> dict:
    """What a clone of the whole history sends: pack-objects --all --stdout, reusing what the store has."""
    with tempfile.NamedTemporaryFile(delete=False) as fout:
        out = fout.name
    try:
        r = run_peak(["git", "-C", str(repo), "pack-objects", "--all", "--revs", "--stdout", "--delta-base-offset",
                      "-q"], stdout_path=out)
        return {"bytes": os.path.getsize(out), "seconds": round(r.seconds, 2), "peak_rss_kib": r.maxrss_kib}
    finally:
        os.unlink(out)


def copy_repo(src: Path, dst: Path) -> Path:
    if os.path.lexists(dst):
        raise FileExistsError(f"{dst} exists; refusing to replace it")
    shutil.copytree(src, dst, symlinks=True)
    return dst


def repack(repo: Path, args: list[str], limit_kib: int | None = None) -> dict:
    """Run a repack command on repo and report its time, peak RSS and the store after it."""
    cmd = ["git", "-C", str(repo), *args]
    if limit_kib:
        cmd = ["prlimit", f"--as={limit_kib * 1024}", "--", *cmd]
    r = run_peak(cmd, check=False)
    res = {"command": "git " + " ".join(args), "exit": r.code, "seconds": round(r.seconds, 2),
           "peak_rss_kib": r.maxrss_kib}
    if r.code != 0:
        res["error"] = r.err.decode(errors="replace")[-500:]
        return res
    res["store"] = store_usage(repo)
    res["objects"] = object_stats(repo)
    return res


def check_work(work: Path, scratch: Path) -> None:
    """work, where copies and remotes go, must be a real directory of this user inside scratch."""
    root = check_scratch(scratch)
    check_scratch(work)
    if not _inside(os.path.realpath(work), root):
        raise UnsafeRepo(f"{work} is not inside {scratch}")


def measure(repo: Path, work: Path, scratch: Path, variants: list[tuple[str, list[str]]] | None = None,
            limit_kib: int | None = None) -> dict:
    """Loose store, transfers, then each repack variant on its own copy of the repository.

    repo and work must lie inside scratch (check_repo, check_work); each copy is checked again before gc runs on it.
    """
    check_repo(repo, scratch)
    check_work(work, scratch)
    res = {"commits": len(commits(repo)), "loose": {"store": store_usage(repo), "objects": object_stats(repo)}}
    res["loose"]["incremental"] = incremental_transfer(repo)
    res["loose"]["full_pack"] = full_pack(repo)
    if variants is None:
        variants = [("gc", ["gc", "-q", "--prune=now"])]
    res["repacked"] = {}
    for name, args in variants:
        cp = copy_repo(repo, work / f"{repo.name}-{name}")
        check_repo(cp, scratch)
        v = repack(cp, args, limit_kib)
        if v.get("exit") == 0:
            v["incremental"] = incremental_transfer(cp)
            v["full_pack"] = full_pack(cp)
        res["repacked"][name] = v
        shutil.rmtree(cp)
    return res


def sync_replay(repo: Path, work: Path, scratch: Path, every: int = 1, unpack_limit: int | None = None) -> dict:
    """Push every commit of repo, one at a time, to a bare remote over file://, and fetch each into a second clone.

    The remote and the peer keep git's defaults (transfer.unpackLimit 100): a push or a fetch of fewer objects than
    that is unpacked into loose objects, so this measures what their stores hold after a sync per commit, not only
    what crossed the wire. repo, work and the two new repositories must pass check_repo/check_work against scratch.
    """
    check_repo(repo, scratch)
    check_work(work, scratch)
    remote = init_repo(work / f"{repo.name}-remote.git", bare=True)
    peer = init_repo(work / f"{repo.name}-peer.git", bare=True)
    if unpack_limit is not None:
        # With a limit of 1 every received pack is kept as it arrived, deltas included, instead of exploded into
        # loose objects.
        for r in (remote, peer):
            git(r, "config", "transfer.unpackLimit", str(unpack_limit))
    for r in (remote, peer):
        check_repo(r, scratch, bare=True)
    cs = commits(repo)
    pushed = fetched = 0
    push_s = fetch_s = 0.0
    peak = 0
    for i, c in enumerate(cs):
        if i % every and i != len(cs) - 1:
            continue
        # GIT_TRACE_PACKFILE traces what fetch receives, not what push sends; the bytes of a push are the thin pack
        # of the same commit, which incremental_transfer measures.
        r = run_peak(["git", "-C", str(repo), "push", "-q", f"file://{remote}", f"{c}:refs/heads/main"])
        pushed += 1
        push_s += r.seconds
        peak = max(peak, r.maxrss_kib)
        with tempfile.NamedTemporaryFile(delete=False) as t:
            trace = t.name
        r = run_peak(["git", "-C", str(peer), "fetch", "-q", f"file://{remote}", "main:refs/remotes/origin/main"],
                     env={"GIT_TRACE_PACKFILE": trace})
        fetched += os.path.getsize(trace)
        fetch_s += r.seconds
        peak = max(peak, r.maxrss_kib)
        os.unlink(trace)
    res = {"pushes": pushed, "unpack_limit": unpack_limit, "fetch_pack_bytes": fetched,
           "push_seconds": round(push_s, 2), "fetch_seconds": round(fetch_s, 2), "peak_rss_kib": peak,
           "remote_store": store_usage(remote), "peer_store": store_usage(peer)}
    shutil.rmtree(remote)
    shutil.rmtree(peer)
    return res
