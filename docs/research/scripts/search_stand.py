#!/usr/bin/env python3
"""Score the pull search (0.8.0) on a set of real prompts, or on a labelled synthetic case file (#510).

Since 0.8.0 the agent searches the base itself with ``memcue search``: there is
no recall, no judge and no ``eval`` verb, so the only cheap way to see what a
change did to the found facts is to run the search command over prompts whose
answers are known.

Prompts mode. The prompts file is a JSON list of objects::

    [{"prompt": "where does the service write its log", "labels": "UNRNN"}]

``labels`` is optional and one character per fact of the search result, in rank
order: ``U`` the fact answers the prompt, ``R`` it is on the topic but does not
answer, ``N`` it is unrelated. With labels the stand reports the fact-level
shares and how many prompts keep a useful fact in the top k; without them it
reports only coverage, latency and size, so a run without labels still catches a
search that broke.

    python3 docs/research/scripts/search_stand.py --prompts prompts.json \\
        --base /path/to/facts.db --sweeps 2,5,10

The state directory is a scratch path: probes must not write into the live one.

Cases mode. A case file (``search_stand/synthetic.json``) brings invented
repositories, facts and queries, each query labelled with the keys of its
useful, related and misleading facts before any run. For every arm the stand
creates a new base in a new directory under ``--out``, writes the facts with
``memcue add`` and runs every query as a full ``memcue search -json`` process.
The first arm is the baseline; the others are compared with it per query.

    python3 -I docs/research/scripts/search_stand.py --cases docs/research/scripts/search_stand/synthetic.json \\
        --out /tmp/stand-run --binary bin/memcue --arm baseline --arm 'wide:-min-score 0' --sweeps 1,3,5

An arm is ``NAME[@BINARY][:ARGS]``; ARGS may hold only -min-score, -in, -kind,
-since, -expired and -all-words, each once and with a value of its shape, since
memcue would search for any other word as query text. ``--out`` must not exist and must lie
outside this repository. memcue runs with an allowlisted environment (no
``MEMCUE_API_URL`` or key, a scratch ``HOME``, base and state) and first on its
PATH a ``claude`` that records the call and fails; any process error, an
unknown label or a fact the case did not write stops the run.

That is all the isolation there is: the binary is not sandboxed. It can still
open the network, read or write any path the user can, and call a model by any
route other than ``claude`` on the PATH, so a run says nothing about that for a
binary other than a memcue whose add and search make no such call. The stand
never opens a live base itself.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import math
import os
import re
import shlex
import shutil
import statistics
import subprocess
import sys
import tempfile
import time
from datetime import date
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[3]
CLASSES = ("short", "paraphrase", "prompt")
KINDS = ("rule", "decision", "pitfall", "environment", "procedure", "event")
LABELS = ("useful", "related", "misleading")
# Case ids, fact keys, repository and arm names: each may become a directory name, so nothing but these characters.
NAME = re.compile(r"[a-z0-9][a-z0-9_-]{0,63}")
# A project key such as example.com/team/billing-api: it only goes into a git remote URL.
REPO_KEY = re.compile(r"[a-z0-9][a-z0-9.-]{0,62}(/[A-Za-z0-9_][A-Za-z0-9_.-]{0,62}){1,4}")
FACT_ID = re.compile(r"[A-Za-z0-9_-]{1,64}")
CASE_FIELDS = {"origin", "labeling", "complete_labels", "repos", "facts", "queries"}
FACT_FIELDS = {"key", "repo", "kind", "scope", "keywords", "text"}
QUERY_FIELDS = {"id", "class", "repo", "text", *LABELS}
# Flags the stand sets itself; an arm that set them would change what is compared or where the search looks.
OWN_FLAGS = {"n", "cwd", "json", "session"}
# The search flags an arm may set (memcue 0.14.0 on) and the shape of each value. memcue reads any word that is not a
# flag or a flag's value as query text, so an arm takes these and nothing else; a binary older than a flag refuses it.
ARM_FLAGS = {"min-score": "share", "in": "places", "kind": "kinds", "since": "day",
             "expired": "bool", "all-words": "bool"}
ARM_FLAG = re.compile(r"--?([a-z][a-z-]*)(?:=(.*))?", re.S)
SHARE = re.compile(r"(\d+(\.\d*)?|\.\d+)")
PLACE = re.compile(r"[A-Za-z0-9][^\s,]*")
DAY = re.compile(r"\d{4}-\d{2}-\d{2}")
# strconv.ParseBool, which the flag package uses for -expired=VALUE.
GO_BOOLS = {"1", "t", "T", "TRUE", "true", "True", "0", "f", "F", "FALSE", "false", "False"}
# What the stand guarantees about the processes it runs, printed with every report so that no reader takes more.
ISOLATION = ("an allowlisted environment with a scratch HOME, base and state, and a claude trap first on the PATH. "
             "The binary is not sandboxed: network, other file paths and other model routes are not blocked or "
             "observed. No live base was opened or checked by the stand.")
# Chars per token for the size of returned facts: an estimate, not a tokenizer.
CHARS_PER_TOKEN = 3.2


def sweeps(value: str) -> list[int]:
    """--sweeps: comma-separated positive top-k values, sorted and deduplicated."""
    try:
        ks = sorted({int(k) for k in value.split(",") if k.strip()})
    except ValueError:
        raise argparse.ArgumentTypeError(f"{value!r} is not a comma-separated list of integers") from None
    if not ks or ks[0] < 1:
        raise argparse.ArgumentTypeError(f"{value!r}: top-k values must be positive integers")
    return ks


def search(binary: str, base: str, state: str, cwd: str, prompt: str, n: int) -> tuple[list[dict], float]:
    env = dict(os.environ, MEMCUE_FACTS=base, MEMCUE_STATE_DIR=state)
    started = time.perf_counter()
    proc = subprocess.run(
        [binary, "search", "-json", "-n", str(n), "-cwd", cwd, "--", prompt],
        capture_output=True, text=True, env=env,
    )
    ms = (time.perf_counter() - started) * 1000
    if proc.returncode != 0:
        raise SystemExit(f"search failed for {prompt[:60]!r}: {proc.stderr.strip()}")
    out = proc.stdout.strip()
    if not out:
        return [], ms
    facts = json.loads(out)
    facts = facts["facts"] if isinstance(facts, dict) else facts  # {"facts", "hints"} since #453
    return facts if isinstance(facts, list) else [], ms


def grade(runs: list[dict], k: int) -> dict:
    """The numbers for one top-k: what the agent gets if it reads k facts."""
    useful_prompts = useful = related = noise = 0
    chars = 0
    for run in runs:
        labels = (run.get("labels") or "")[:k]
        if not labels:
            continue
        labels = labels[:len(run["facts"])]
        useful_prompts += 1 if "U" in labels else 0
        useful += labels.count("U")
        related += labels.count("R")
        noise += labels.count("N")
        chars += sum(len(f.get("content") or "") for f in run["facts"][:len(labels)])
    graded = useful + related + noise
    return {
        "k": k,
        "prompts": len(runs),
        "prompts_with_useful": useful_prompts,
        "useful": useful,
        "related": related,
        "noise": noise,
        "useful_share": useful / graded if graded else 0.0,
        "noise_share": noise / graded if graded else 0.0,
        "chars": chars,
        "tokens": round(chars / 3.2),
    }


def run_prompts(args: argparse.Namespace) -> int:
    prompts = json.loads(Path(args.prompts).read_text(encoding="utf-8"))
    ks = args.sweeps or [10]
    state = args.state or tempfile.mkdtemp(prefix="memcue-stand-")

    runs: list[dict] = []
    for item in prompts:
        prompt = item["prompt"].strip().replace("\n", " ")
        facts, ms = [], 0.0
        for _ in range(max(1, args.repeat)):
            got, took = search(args.binary, args.base, state, args.cwd, prompt, max(ks))
            if len(got) >= len(facts):
                facts, ms = got, max(ms, took)
        runs.append({"prompt": prompt, "labels": item.get("labels"), "facts": facts, "ms": ms})

    latencies = [r["ms"] for r in runs]
    report = {
        "prompts": len(runs),
        "searches": len(runs) * args.repeat,
        "base": args.base,
        "covered": sum(1 for r in runs if r["facts"]),
        "ms_median": statistics.median(latencies) if latencies else 0.0,
        "ms_p90": sorted(latencies)[int(len(latencies) * 0.9) - 1] if latencies else 0.0,
        "sweeps": [grade(runs, k) for k in ks],
    }
    if args.json:
        print(json.dumps(report, ensure_ascii=False, indent=1))
        return 0

    print(f"**{report['prompts']} prompts, {report['searches']} searches.** "
          f"Prompt got a fact in {report['covered']} of {report['prompts']}; "
          f"search p50 {report['ms_median']:.1f} ms, p90 {report['ms_p90']:.1f} ms.")
    print()
    print("| top-k | prompts with a useful fact | useful | related | noise | useful share | noise share | chars | ~tokens |")
    print("|---|---|---|---|---|---|---|---|---|")
    for row in report["sweeps"]:
        if not row["useful"] and not row["noise"]:
            print(f"| {row['k']} | — | — | — | — | — | — | {row['chars']} | {row['tokens']} |")
            continue
        print(f"| {row['k']} | {row['prompts_with_useful']}/{row['prompts']} | {row['useful']} | {row['related']} | "
              f"{row['noise']} | {row['useful_share']:.0%} | {row['noise_share']:.0%} | {row['chars']} | {row['tokens']} |")
    print()
    print(f"Labels are one rater's, from the fact text alone; without `labels` the table shows characters only.")
    return 0


# ---------------------------------------------------------------------------------------------------------- cases


class StandError(Exception):
    """A case file, an output path or a memcue process the stand refuses: the run stops without a result."""


def _text(where: str, value, empty_ok: bool = False) -> str:
    if not isinstance(value, str) or "\0" in value or (not empty_ok and not value.strip()):
        raise StandError(f"{where}: must be a non-empty string without NUL")
    return value


def _fields(where: str, obj, allowed: set[str], required: set[str]) -> dict:
    if not isinstance(obj, dict):
        raise StandError(f"{where}: must be an object")
    unknown, missing = set(obj) - allowed, required - set(obj)
    if unknown:
        raise StandError(f"{where}: unknown fields {sorted(unknown)}")
    if missing:
        raise StandError(f"{where}: missing fields {sorted(missing)}")
    return obj


def _name(where: str, value) -> str:
    if not isinstance(value, str) or not NAME.fullmatch(value):
        raise StandError(f"{where}: {value!r} must match {NAME.pattern}")
    return value


def load_cases(path: Path) -> dict:
    """Read and check a case file. Anything unexpected is refused: an unknown field may be a mistyped label."""
    try:
        data = json.loads(Path(path).read_text(encoding="utf-8"))
    except (OSError, ValueError) as e:
        raise StandError(f"{path}: {e}") from None
    _fields(str(path), data, CASE_FIELDS, CASE_FIELDS)
    if data["origin"] != "synthetic":
        raise StandError(f"{path}: origin must be 'synthetic': cases mode takes no real data")
    _text(f"{path}: labeling", data["labeling"])
    if type(data["complete_labels"]) is not bool:
        raise StandError(f"{path}: complete_labels must be true or false")

    repos = data["repos"]
    if not isinstance(repos, dict) or not repos:
        raise StandError(f"{path}: repos must be a non-empty object of name -> project key")
    for name, key in repos.items():
        _name(f"{path}: repository", name)
        if not isinstance(key, str) or not REPO_KEY.fullmatch(key) or ".." in key:
            raise StandError(f"{path}: repository {name}: key {key!r} must look like host/group/project")

    if not isinstance(data["facts"], list) or not data["facts"]:
        raise StandError(f"{path}: facts must be a non-empty list")
    keys: set[str] = set()
    for i, f in enumerate(data["facts"]):
        where = f"{path}: facts[{i}]"
        _fields(where, f, FACT_FIELDS, {"key", "repo", "kind", "text"})
        key = _name(f"{where}: key", f["key"])
        if key in keys:
            raise StandError(f"{where}: duplicate fact key {key}")
        keys.add(key)
        if not isinstance(f["repo"], str) or f["repo"] not in repos:
            raise StandError(f"{where}: repository {f['repo']!r} is not in repos")
        if not isinstance(f["kind"], str) or f["kind"] not in KINDS:
            raise StandError(f"{where}: kind must be one of {KINDS}")
        _text(f"{where}: text", f["text"])
        for opt in ("scope", "keywords"):
            if opt in f:
                _text(f"{where}: {opt}", f[opt])

    if not isinstance(data["queries"], list) or not data["queries"]:
        raise StandError(f"{path}: queries must be a non-empty list")
    ids: set[str] = set()
    texts: dict[str, str] = {}
    for i, q in enumerate(data["queries"]):
        where = f"{path}: queries[{i}]"
        _fields(where, q, QUERY_FIELDS, {"id", "class", "repo", "text"})
        qid = _name(f"{where}: id", q["id"])
        if qid in ids:
            raise StandError(f"{where}: duplicate query id {qid}")
        ids.add(qid)
        if not isinstance(q["class"], str) or q["class"] not in CLASSES:
            raise StandError(f"{where}: class must be one of {CLASSES}")
        if not isinstance(q["repo"], str) or q["repo"] not in repos:
            raise StandError(f"{where}: repository {q['repo']!r} is not in repos")
        norm = " ".join(_text(f"{where}: text", q["text"]).split()).casefold()
        if norm in texts:
            raise StandError(f"{where}: the same text as query {texts[norm]}: every query must be distinct")
        texts[norm] = qid
        seen: dict[str, str] = {}
        for label in LABELS:
            values = q.get(label, [])
            if not isinstance(values, list) or not all(isinstance(v, str) for v in values):
                raise StandError(f"{where}: {label} must be a list of fact keys")
            unknown = sorted(set(values) - keys)
            if unknown:
                raise StandError(f"{where}: {label} names unknown facts {unknown}")
            for v in values:
                if v in seen:
                    raise StandError(f"{where}: fact {v} is labelled both {seen[v]} and {label}")
                seen[v] = label
    return data


def check_arm_value(name: str, value: str) -> None:
    shape = ARM_FLAGS[name]
    if shape == "bool":
        ok = value in GO_BOOLS
    elif shape == "share":
        ok = bool(SHARE.fullmatch(value)) and float(value) <= 1
    elif shape == "day":
        try:
            ok = bool(DAY.fullmatch(value)) and bool(date.fromisoformat(value))
        except ValueError:
            ok = False
    else:
        items = value.split(",")
        ok = all(PLACE.fullmatch(i) for i in items) if shape == "places" else \
            set(items) <= set(KINDS) and len(set(items)) == len(items)
    if not ok:
        want = {"bool": "true or false", "share": "a share from 0 to 1", "day": "a day YYYY-MM-DD",
                "places": "comma-separated places such as project,global or repo:KEY",
                "kinds": "distinct kinds from " + ",".join(KINDS)}[shape]
        raise StandError(f"-{name} {value!r}: want {want}")


def check_arm_args(args: list[str]) -> None:
    """The arm's search flags, read the way memcue reads them: each is known, given once and has a value of its
    shape; a bool takes no separate value. A stray word would join the query text and change every query."""
    seen = set()
    i = 0
    while i < len(args):
        a = args[i]
        i += 1
        if a == "--":
            raise StandError("-- is set by the stand itself")
        m = ARM_FLAG.fullmatch(a)
        if not m:
            if a.startswith("-") and len(a) > 1:
                raise StandError(f"{a!r} is not a well-formed flag")
            raise StandError(f"{a!r} is not a flag: memcue would search for it as query text")
        name, value = m.group(1), m.group(2)
        if name in OWN_FLAGS:
            raise StandError(f"{a} is set by the stand itself")
        if name not in ARM_FLAGS:
            raise StandError(f"{a}: unknown flag, an arm takes -{', -'.join(ARM_FLAGS)}")
        if name in seen:
            raise StandError(f"-{name} is given twice")
        seen.add(name)
        if value is None and ARM_FLAGS[name] != "bool":
            if i == len(args):
                raise StandError(f"-{name} needs a value")
            value = args[i]
            i += 1
        if value is not None:
            check_arm_value(name, value)


def parse_arm(spec: str, default_binary: str) -> dict:
    """NAME[@BINARY][:ARGS]: 'baseline', 'wide:-min-score 0', 'v019@/tmp/memcue-0.19.0'."""
    head, _, rest = spec.partition(":")
    name, _, binary = head.partition("@")
    _name(f"arm {spec!r}", name)
    try:
        args = shlex.split(rest)
    except ValueError as e:
        raise StandError(f"arm {spec!r}: {e}") from None
    try:
        check_arm_args(args)
    except StandError as e:
        raise StandError(f"arm {spec!r}: {e}") from None
    found = shutil.which(binary or default_binary)
    if not found:
        raise StandError(f"arm {spec!r}: no memcue binary {binary or default_binary!r}")
    return {"name": name, "binary": str(Path(found).resolve()), "args": args}


def make_out(path: str) -> Path:
    """A new directory for the run, created here so that nothing in it predates the run. An existing path, a
    symlink (even a dangling one) or a path inside this repository is refused."""
    p = Path(path)
    if p.name in ("", ".", ".."):
        raise StandError(f"--out {path!r}: name a new directory")
    try:
        out = p.parent.resolve(strict=True) / p.name
    except OSError as e:
        raise StandError(f"--out {path!r}: {e}") from None
    if out == REPO_ROOT or REPO_ROOT in out.parents:
        raise StandError(f"--out {out} is inside the repository {REPO_ROOT}")
    try:
        os.mkdir(out, 0o700)
    except OSError as e:
        raise StandError(f"--out {out}: {e.strerror}: it must not exist") from None
    return out


def write_new(path: Path, text: str) -> None:
    """Write a file that must not exist yet. O_EXCL refuses any existing name, a symlink or a hard link included,
    and O_NOFOLLOW refuses a symlink in the last component."""
    try:
        fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    except OSError as e:
        raise StandError(f"refusing to write {path}: {e.strerror}") from None
    with os.fdopen(fd, "w", encoding="utf-8") as f:
        f.write(text)


def scratch_env(work: Path) -> dict:
    """An allowlist, not a denylist: nothing of the caller reaches memcue except PATH and the locale, so no
    MEMCUE_API_URL, key or base can. HOME, base, state and XDG paths are under work; claude on the PATH is a trap."""
    env = {k: os.environ[k] for k in ("PATH", "LANG", "LC_ALL", "TZ") if k in os.environ}
    env.update({
        "PATH": f"{work / 'trap'}{os.pathsep}{env.get('PATH', '/usr/bin:/bin')}",
        "HOME": str(work / "home"),
        "XDG_STATE_HOME": str(work / "xdg-state"),
        "XDG_DATA_HOME": str(work / "xdg-data"),
        "XDG_CONFIG_HOME": str(work / "xdg-config"),
        "MEMCUE_FACTS": str(work / "facts.db"),
        "MEMCUE_STATE_DIR": str(work / "state"),
        "MEMCUE_HOST": "stand-host",
        "MEMCUE_SEARCH_LOG": "0",
        "MEMCUE_DREAM_EVERY": "87600h",
        "GIT_CONFIG_GLOBAL": os.devnull,
        "GIT_CONFIG_NOSYSTEM": "1",
    })
    return env


def run_proc(argv: list[str], env: dict, cwd: Path, timeout: float) -> tuple[str, float]:
    started = time.perf_counter()
    try:
        proc = subprocess.run(argv, capture_output=True, text=True, env=env, cwd=cwd, timeout=timeout)
    except subprocess.TimeoutExpired:
        raise StandError(f"{Path(argv[0]).name} {argv[1]} timed out after {timeout:g} s") from None
    except OSError as e:
        raise StandError(f"cannot run {argv[0]}: {e.strerror}") from None
    ms = (time.perf_counter() - started) * 1000
    if proc.returncode != 0:
        raise StandError(f"{Path(argv[0]).name} {argv[1]} exited {proc.returncode}: {proc.stderr.strip()[:300]}")
    return proc.stdout, ms


def prepare_arm(case: dict, arm: dict, out: Path, timeout: float) -> tuple[Path, dict, dict[str, str]]:
    """A new directory with a new base holding the case's facts. Returns the directory, its environment and the
    fact key of every id memcue gave."""
    work = out / f"arm-{arm['name']}"
    os.mkdir(work, 0o700)
    for sub in ("home", "state", "trap", "repos"):
        os.mkdir(work / sub, 0o700)
    trap = work / "trap" / "claude"
    write_new(trap, f"#!/bin/sh\necho called >> {shlex.quote(str(work / 'trap' / 'called'))}\nexit 97\n")
    trap.chmod(0o700)
    env = scratch_env(work)
    repos = {}
    for name, key in case["repos"].items():
        repo = work / "repos" / name
        host, _, rest = key.partition("/")
        run_proc(["git", "init", "-q", str(repo)], env, work, timeout)
        run_proc(["git", "-C", str(repo), "remote", "add", "origin", f"git@{host}:{rest}.git"], env, work, timeout)
        repos[name] = repo
    keys: dict[str, str] = {}
    for f in case["facts"]:
        argv = [arm["binary"], "add", f"-kind={f['kind']}", "-source=user", f"-cwd={repos[f['repo']]}"]
        argv += [f"-{opt}={f[opt]}" for opt in ("scope", "keywords") if opt in f]
        stdout, _ = run_proc(argv + ["--", f["text"]], env, repos[f["repo"]], timeout)
        fid = stdout.strip()
        if not FACT_ID.fullmatch(fid):
            raise StandError(f"memcue add {f['key']}: expected a fact id, got {stdout.strip()[:80]!r}")
        if fid in keys:
            raise StandError(f"memcue add {f['key']}: id {fid} is already fact {keys[fid]}")
        keys[fid] = f["key"]
    if not (work / "facts.db").is_file():
        raise StandError(f"memcue add did not create {work / 'facts.db'}: the base went elsewhere")
    return work, env, keys


def search_facts(stdout: str) -> list:
    """The facts of one search -json answer. Since 0.17.0 (#453) it is {"facts": [...], "hints": [...]}; 0.8.0 to
    0.16 print the bare list. Anything else, null included, is refused rather than read as no facts."""
    try:
        answer = json.loads(stdout)
    except ValueError:
        raise StandError(f"not JSON: {stdout[:80]!r}") from None
    if isinstance(answer, dict):
        if "facts" not in answer:
            raise StandError(f"a JSON object without facts: {stdout[:80]!r}")
        answer = answer["facts"]
    if not isinstance(answer, list):
        raise StandError(f"facts must be a list, got {type(answer).__name__}: {stdout[:80]!r}")
    return answer


def case_search(arm: dict, env: dict, cwd: Path, text: str, n: int, keys: dict[str, str],
                timeout: float) -> tuple[list[str], list[int], float]:
    """One full search process: the fact keys in rank order, their lengths and the wall-clock milliseconds."""
    argv = [arm["binary"], "search", "-json", *arm["args"], "-n", str(n), f"-cwd={cwd}", "--", text]
    stdout, ms = run_proc(argv, env, cwd, timeout)
    try:
        facts = search_facts(stdout)
    except StandError as e:
        raise StandError(f"memcue search {text[:40]!r}: {e}") from None
    if len(facts) > n:
        raise StandError(f"memcue search {text[:40]!r}: facts must be a list of at most {n}")
    got, sizes = [], []
    for f in facts:
        if not isinstance(f, dict) or not isinstance(f.get("id"), str) or not isinstance(f.get("content"), str):
            raise StandError(f"memcue search {text[:40]!r}: a fact must be an object with string id and content, "
                             f"got {json.dumps(f)[:80]}")
        fid = f["id"]
        if fid not in keys:
            raise StandError(f"memcue search {text[:40]!r}: returned {fid!r}, a fact the case did not write")
        if keys[fid] in got:
            raise StandError(f"memcue search {text[:40]!r}: returned {fid} twice")
        got.append(keys[fid])
        sizes.append(len(f["content"]))
    return got, sizes, ms


def run_arm(case: dict, arm: dict, out: Path, n: int, repeat: int, timeout: float) -> dict:
    work, env, keys = prepare_arm(case, arm, out, timeout)
    version, _ = run_proc([arm["binary"], "version"], env, work, timeout)
    runs = []
    for q in case["queries"]:
        cwd = work / "repos" / q["repo"]
        tries = [case_search(arm, env, cwd, q["text"], n, keys, timeout) for _ in range(repeat)]
        runs.append({
            "id": q["id"], "class": q["class"], **{label: q.get(label, []) for label in LABELS},
            "got": tries[0][0], "sizes": tries[0][1], "ms": [t[2] for t in tries],
            "unstable": any(t[0] != tries[0][0] for t in tries),
        })
    if (work / "trap" / "called").exists():
        raise StandError(f"arm {arm['name']}: memcue ran claude, a model call")
    return {"arm": arm["name"], "binary": arm["binary"], "version": version.strip(), "args": arm["args"],
            "runs": runs}


def label_of(key: str, run: dict, complete: bool) -> str:
    """U useful, R related, M misleading, N noise; ? unknown when the labels do not cover every fact."""
    for label, mark in (("useful", "U"), ("related", "R"), ("misleading", "M")):
        if key in run[label]:
            return mark
    return "N" if complete else "?"


def percentile(values: list[float], p: float) -> float | None:
    if not values:
        return None
    s = sorted(values)  # nearest rank
    return s[min(len(s) - 1, max(0, math.ceil(p / 100 * len(s)) - 1))]


def grade_cases(runs: list[dict], k: int, complete: bool) -> dict:
    """The numbers for the top k of one arm. A query without a useful label has no relevant memory only when the
    labels are complete; otherwise whether the base could answer it is unknown."""
    counts = dict.fromkeys("URMN?", 0)
    answerable = covered = misled = no_memory = no_memory_given = memory_unknown = chars = 0
    for r in runs:
        marks = [label_of(key, r, complete) for key in r["got"][:k]]
        for m in marks:
            counts[m] += 1
        chars += sum(r["sizes"][:k])
        misled += "M" in marks
        if r["useful"]:
            answerable += 1
            covered += "U" in marks
        elif complete:
            no_memory += 1
            no_memory_given += bool(marks)
        else:
            memory_unknown += 1
    returned = sum(counts.values())
    judged = returned - counts["?"]
    ms = [m for r in runs for m in r["ms"]]
    return {
        "k": k, "queries": len(runs), "answerable": answerable, "useful_in_top_k": covered,
        "returned": returned, "useful": counts["U"], "related": counts["R"], "misleading": counts["M"],
        "noise": counts["N"], "unknown": counts["?"],
        "precision": counts["U"] / judged if judged else None,
        "noise_share": counts["N"] / judged if judged else None,
        "queries_with_misleading": misled, "no_memory": no_memory, "no_memory_given_facts": no_memory_given,
        "memory_unknown": memory_unknown, "unstable": sum(r["unstable"] for r in runs),
        "chars": chars, "tokens_est": round(chars / CHARS_PER_TOKEN),
        "invocations": len(ms), "ms_p50": percentile(ms, 50), "ms_p90": percentile(ms, 90),
    }


def compare(base: list[dict], cand: list[dict], k: int, complete: bool) -> dict:
    """Per query against the baseline: a useful fact gained or lost in the top k, a misleading one gained or lost."""
    by_id = {r["id"]: r for r in base}
    out = {"gained": [], "lost": [], "misleading_gained": [], "misleading_lost": [], "changed": []}
    for r in cand:
        b = by_id[r["id"]]
        have = {label_of(key, r, complete) for key in r["got"][:k]}
        had = {label_of(key, b, complete) for key in b["got"][:k]}
        if r["got"][:k] != b["got"][:k]:
            out["changed"].append(r["id"])
        if r["useful"] and ("U" in have) != ("U" in had):
            out["gained" if "U" in have else "lost"].append(r["id"])
        if ("M" in have) != ("M" in had):
            out["misleading_gained" if "M" in have else "misleading_lost"].append(r["id"])
    return out


def fmt(x, pct: bool = False) -> str:
    if x is None:
        return "—"
    return f"{x:.0%}" if pct else f"{x:.1f}"


def cases_report(results: dict) -> str:
    complete = results["complete_labels"]
    lines = [f"- `{results['cases']}` (sha256 {results['cases_sha256'][:12]}): {results['queries']} distinct queries, "
             f"{results['facts']} facts, origin {results['origin']}, labels "
             f"{'complete' if complete else 'partial'}. Labels: {results['labeling']}",
             f"- {results['repeat']} searches per query; tokens are characters / {CHARS_PER_TOKEN}, an estimate.",
             "",
             "| arm | class | k | queries | useful in top k | precision | noise | unknown | misleading returned "
             "(queries) | no-memory queries given facts | memory unknown | ~tokens | p50 ms | p90 ms |",
             "|---|---|---|---|---|---|---|---|---|---|---|---|---|---|"]
    for a in results["arms"]:
        for cls in ("all", *CLASSES):
            runs = [r for r in a["runs"] if cls == "all" or r["class"] == cls]
            if not runs:
                continue
            for k in results["ks"]:
                g = grade_cases(runs, k, complete)
                lines.append(
                    f"| {a['arm']} | {cls} | {k} | {g['queries']} | {g['useful_in_top_k']}/{g['answerable']} | "
                    f"{fmt(g['precision'], True)} | {fmt(g['noise_share'], True)} | {g['unknown']} | "
                    f"{g['misleading']} ({g['queries_with_misleading']}) | "
                    f"{g['no_memory_given_facts']}/{g['no_memory']} | {g['memory_unknown']} | {g['tokens_est']} | "
                    f"{fmt(g['ms_p50'])} | {fmt(g['ms_p90'])} |")
    lines.append("")
    base = results["arms"][0]
    k = max(results["ks"])
    for a in results["arms"]:
        lines.append(f"- {a['arm']}: `{a['version']}` {shlex.join(a['args']) or '(no extra flags)'}; "
                     f"queries whose result differed between repeats: "
                     f"{[r['id'] for r in a['runs'] if r['unstable']] or 'none'}.")
    for a in results["arms"][1:]:
        d = compare(base["runs"], a["runs"], k, complete)
        lines.append(f"- {a['arm']} against {base['arm']} at k={k}: top k changed for {len(d['changed'])} queries; "
                     f"useful gained {d['gained'] or 'none'}, lost {d['lost'] or 'none'}; "
                     f"misleading gained {d['misleading_gained'] or 'none'}, lost {d['misleading_lost'] or 'none'}.")
    lines.append(f"- Misses of {base['arm']} at k={k}: "
                 + (", ".join(f"{r['id']} (got {r['got'][:k] or 'nothing'})" for r in base["runs"]
                              if r["useful"] and not set(r["useful"]) & set(r["got"][:k])) or "none") + ".")
    lines.append(f"- Isolation: {ISOLATION}")
    return "\n".join(lines)


def run_cases(args: argparse.Namespace) -> int:
    case_path = Path(args.cases)
    case = load_cases(case_path)
    ks = args.sweeps or [1, 3, 5]
    if args.repeat < 1 or not (0 < args.timeout < math.inf):
        raise StandError("--repeat and --timeout must be positive")
    arms = [parse_arm(spec, args.binary) for spec in (args.arm or ["baseline"])]
    if len({a["name"] for a in arms}) != len(arms):
        raise StandError("arm names must differ")
    out = make_out(args.out)
    results = {
        "cases": case_path.name, "cases_sha256": hashlib.sha256(case_path.read_bytes()).hexdigest(),
        "origin": case["origin"], "labeling": case["labeling"], "complete_labels": case["complete_labels"],
        "facts": len(case["facts"]), "queries": len(case["queries"]), "ks": ks, "repeat": args.repeat,
        "arms": [run_arm(case, arm, out, max(ks), args.repeat, args.timeout) for arm in arms],
        "isolation": ISOLATION,
    }
    write_new(out / "results.json", json.dumps(results, ensure_ascii=False, indent=1))
    print(json.dumps(results, ensure_ascii=False, indent=1) if args.json else cases_report(results))
    return 0


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--prompts", help="prompts mode: JSON list of {prompt, labels?}")
    ap.add_argument("--base", help="prompts mode: facts.db of the base to search")
    ap.add_argument("--cases", help="cases mode: a synthetic case file")
    ap.add_argument("--out", help="cases mode: a new directory outside the repository for the bases and results.json")
    ap.add_argument("--arm", action="append", help="cases mode: NAME[@BINARY][:ARGS], repeatable; the first is "
                                                   "the baseline (default: baseline)")
    ap.add_argument("--timeout", type=float, default=30, help="cases mode: seconds per memcue process")
    ap.add_argument("--binary", default="memcue", help="memcue binary (default: memcue from PATH)")
    ap.add_argument("--cwd", default=".", help="the session directory the search is told about")
    ap.add_argument("--state", default="", help="state directory for the probe logs (default: a temp directory)")
    ap.add_argument("--sweeps", type=sweeps, help="top-k values to compare, e.g. 2,5,10 (default: 10, cases: 1,3,5)")
    ap.add_argument("--repeat", type=int, default=1, help="searches per prompt, the slowest one is reported")
    ap.add_argument("--json", action="store_true", help="print JSON instead of markdown")
    args = ap.parse_args(argv)

    if args.cases:
        if args.prompts or args.base or not args.out:
            ap.error("--cases takes --out and neither --prompts nor --base")
        try:
            return run_cases(args)
        except StandError as e:
            print(f"search_stand: {e}", file=sys.stderr)
            return 2
    if not args.prompts or not args.base:
        ap.error("give --prompts and --base, or --cases and --out")
    return run_prompts(args)


if __name__ == "__main__":
    sys.exit(main())
