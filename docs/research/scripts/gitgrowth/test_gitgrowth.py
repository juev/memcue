#!/usr/bin/env python3
"""Tests of the #530 research tooling: the result checker and the safety of the scripts.

    python3 -I docs/research/scripts/gitgrowth/test_gitgrowth.py
    GITGROWTH_E2E=1 python3 -I docs/research/scripts/gitgrowth/test_gitgrowth.py   # plus a quick run.sh, ~minutes

Everything runs in a new temporary directory. The safety tests build a sentinel repository and an armed git
environment (GIT_DIR, GIT_WORK_TREE, GIT_OBJECT_DIRECTORY, GIT_ALTERNATE_OBJECT_DIRECTORIES, GIT_INDEX_FILE,
GIT_TEMPLATE_DIR, GIT_CONFIG_PARAMETERS, GIT_CONFIG_COUNT, GIT_CONFIG_GLOBAL, HOME and XDG configs with hooks and an
fsmonitor that drop canary files). A control test first shows that this environment does reach a plain git; the
tools must then leave the sentinel byte for byte as it was and fire no canary.
"""

from __future__ import annotations

import hashlib
import json
import os
import shutil
import stat
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent
RESULTS = HERE / "results"
sys.path.insert(0, str(HERE))
import check  # noqa: E402
import gitmeasure as gm  # noqa: E402

CLEAN = {"GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": os.devnull, "GIT_AUTHOR_NAME": "t",
         "GIT_AUTHOR_EMAIL": "t@example.invalid", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@example.invalid"}


def plain_env(**extra: str) -> dict:
    env = {k: v for k, v in os.environ.items() if not k.startswith("GIT_")}
    env.update(CLEAN)
    env.update(extra)
    return env


def snapshot(root: Path, skip: tuple[Path, ...] = ()) -> dict[str, str]:
    """Relative path -> kind and content hash of everything under root, symlinks not followed."""
    out = {}
    for dirpath, dirnames, filenames in os.walk(root):
        d = Path(dirpath)
        dirnames[:] = [n for n in dirnames if d / n not in skip]
        for n in dirnames + filenames:
            p = d / n
            if p in skip:
                continue
            st = p.lstat()
            if stat.S_ISLNK(st.st_mode):
                v = "link " + os.readlink(p)
            elif stat.S_ISDIR(st.st_mode):
                v = "dir"
            else:
                v = "file " + hashlib.sha256(p.read_bytes()).hexdigest()
            out[str(p.relative_to(root))] = v
    return out


class Armed:
    """A sentinel repository and an environment that points git at it and arms hooks that leave canary files."""

    def __init__(self, root: Path):
        self.root = root
        self.sentinel = root / "sentinel"
        self.canaries = root / "canaries"
        root.mkdir()
        self.canaries.mkdir()
        self.sentinel.mkdir()
        env = plain_env()
        subprocess.run(["git", "init", "-q", "-b", "main", str(self.sentinel)], env=env, check=True)
        (self.sentinel / "keep.txt").write_text("sentinel\n")
        subprocess.run(["git", "-C", str(self.sentinel), "add", "keep.txt"], env=env, check=True)
        subprocess.run(["git", "-C", str(self.sentinel), "commit", "-q", "-m", "sentinel"], env=env, check=True)
        canary = root / "canary.sh"
        canary.write_text(f"#!/bin/sh\ntouch '{self.canaries}'/\"$(basename \"$0\")-$$\"\nexit 0\n")
        canary.chmod(0o755)
        hooks = root / "hooks"
        hooks.mkdir()
        for h in ("pre-commit", "post-commit", "pre-push", "post-checkout", "post-merge", "pre-receive",
                  "post-receive", "post-update", "reference-transaction", "pre-auto-gc"):
            (hooks / h).symlink_to(canary)
        template = root / "template"
        shutil.copytree(hooks, template / "hooks", symlinks=True)
        armed_cfg = f"[core]\n\thooksPath = {hooks}\n\tfsmonitor = {canary}\n"
        (root / "global.gitconfig").write_text(armed_cfg)
        home = root / "home"
        (home / ".config" / "git").mkdir(parents=True)
        (home / ".gitconfig").write_text(armed_cfg)
        (home / ".config" / "git" / "config").write_text(armed_cfg)
        git_dir = self.sentinel / ".git"
        self.env = dict(os.environ)
        self.env.update({
            "GIT_DIR": str(git_dir), "GIT_WORK_TREE": str(self.sentinel), "GIT_INDEX_FILE": str(git_dir / "index"),
            "GIT_OBJECT_DIRECTORY": str(git_dir / "objects"), "GIT_ALTERNATE_OBJECT_DIRECTORIES": str(git_dir / "objects"),
            "GIT_TEMPLATE_DIR": str(template), "GIT_CONFIG_GLOBAL": str(root / "global.gitconfig"),
            "GIT_CONFIG_PARAMETERS": f"'core.hookspath'='{hooks}' 'core.fsmonitor'='{canary}'",
            "GIT_CONFIG_COUNT": "1", "GIT_CONFIG_KEY_0": "core.hooksPath", "GIT_CONFIG_VALUE_0": str(hooks),
            "HOME": str(home), "XDG_CONFIG_HOME": str(home / ".config"),
        })
        self.before = snapshot(self.sentinel)

    def fired(self) -> list[str]:
        return sorted(p.name for p in self.canaries.iterdir())

    def sentinel_intact(self) -> bool:
        return snapshot(self.sentinel) == self.before


class TempCase(unittest.TestCase):
    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp(prefix="gitgrowth-test-"))
        self.addCleanup(shutil.rmtree, self.tmp)


class CheckTest(TempCase):
    """check.py passes the published results and fails on each deliberately broken copy of them."""

    def copy_results(self) -> Path:
        d = self.tmp / "results"
        shutil.copytree(RESULTS, d)
        return d

    def run_check(self, d: Path) -> subprocess.CompletedProcess:
        return subprocess.run([sys.executable, "-I", str(HERE / "check.py"), str(d)], capture_output=True, text=True)

    def test_published_results_pass(self):
        r = self.run_check(RESULTS)
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)

    def test_invalid_fixtures_fail(self):
        def set_key(name, path, value):
            def mutate(d):
                p = d / f"{name}.json"
                j = json.loads(p.read_text())
                obj = j
                for k in path[:-1]:
                    obj = obj[k]
                obj[path[-1]] = value
                p.write_text(json.dumps(j))
            return mutate

        def remove(name):
            return lambda d: (d / f"{name}.json").unlink()

        def garble(name):
            return lambda d: (d / f"{name}.json").write_text("{not json")

        cases = {
            "roundtrip rows differ": set_key("roundtrip", ["rows_equal"], False),
            "roundtrip search differs": set_key("roundtrip", ["search_same_results"], "299/300"),
            "truncation accepted": set_key("roundtrip", ["truncated_refused"], False),
            "dump keeps evidence": set_key("roundtrip", ["memcue_dump_has_evidence"], "1078/1078"),
            "import left a broken target": set_key("interrupt", ["target_other_or_unreadable"], 1),
            "final import incomplete": set_key("interrupt", ["final_import_complete"], False),
            "snapshot export broken": set_key("race", ["snapshot_exports_ok"], 127),
            "orders differ": set_key("semantics", ["rounds", 0, "sqlite_both_orders_equal"], False),
            "index mismatch": set_key("semantics", ["rounds", 3, "index_matches_live_facts"], False),
            "stale duplicate gone": set_key("semantics", ["stale", "sqlite_rows"], 17),
            "gc failed": set_key("synthetic", ["variants", "text-single", "repacked", "gc", "exit"], 128),
            "missing file": remove("race"),
            "unparsable file": garble("interrupt"),
        }
        for name, mutate in cases.items():
            with self.subTest(name):
                d = self.copy_results()
                mutate(d)
                r = self.run_check(d)
                self.assertEqual(r.returncode, 1, f"{name}: {r.stdout}{r.stderr}")
                self.assertIn("FAIL", r.stdout)
                shutil.rmtree(d)

    def test_retire_reopen_finding(self):
        d = self.copy_results()
        p = d / "semantics.json"
        j = json.loads(p.read_text())
        for r in j["rounds"]:
            if r["scenario"] == "retire-then-correct":
                r["watched_fact"]["status"] = "retired"
        p.write_text(json.dumps(j))
        self.assertEqual(self.run_check(d).returncode, 1)

    def test_no_round_shows_remerge_duplicate(self):
        d = self.copy_results()
        p = d / "semantics.json"
        j = json.loads(p.read_text())
        for r in j["rounds"]:
            r["remerge_changes_nothing"] = True
        p.write_text(json.dumps(j))
        self.assertEqual(self.run_check(d).returncode, 1)


class PublishedResultsTest(unittest.TestCase):
    """The published JSON carries no fact ids, hashes or local paths."""

    def test_no_ids_hashes_or_paths(self):
        import re
        patterns = {"ULID": r"\b[0-9A-HJKMNP-TV-Z]{26}\b", "hex hash": r"\b[0-9a-f]{32,}\b",
                    "absolute path": r"\"/(home|opt|root|Users|tmp|var)/", "home path": r"~/"}
        for p in sorted(RESULTS.glob("*.json")):
            text = p.read_text()
            for name, rx in patterns.items():
                with self.subTest(file=p.name, pattern=name):
                    self.assertIsNone(re.search(rx, text), f"{p.name}: {name}")


class ArmedEnvTest(TempCase):
    def setUp(self):
        super().setUp()
        self.armed = Armed(self.tmp / "armed")

    def test_control_armed_env_reaches_plain_git(self):
        """Without sanitising, the armed environment does redirect git: the safety tests below can fail."""
        victim = self.tmp / "victim"
        env = dict(self.armed.env, GIT_AUTHOR_NAME="t", GIT_AUTHOR_EMAIL="t@example.invalid", GIT_COMMITTER_NAME="t",
                   GIT_COMMITTER_EMAIL="t@example.invalid")
        subprocess.run(["git", "init", "-q", str(victim)], env=env, capture_output=True)
        subprocess.run(["git", "-C", str(victim), "commit", "-q", "--allow-empty", "-m", "x"], env=env,
                       capture_output=True)
        self.assertFalse(self.armed.sentinel_intact(), "the armed GIT_INDEX_FILE/GIT_DIR did not reach git")
        self.assertTrue(self.armed.fired(), "no canary fired")

    def test_gitmeasure_ignores_armed_env(self):
        old = dict(os.environ)
        self.addCleanup(lambda: (os.environ.clear(), os.environ.update(old)))
        os.environ.clear()
        os.environ.update(self.armed.env)
        outer_tmp = self.tmp / "outer-tmp"
        outer_tmp.mkdir()
        os.environ["TMPDIR"] = str(outer_tmp)
        tempfile.tempdir = None
        self.addCleanup(setattr, tempfile, "tempdir", None)

        scratch = gm.new_dir(self.tmp / "scratch")
        repo = gm.init_repo(scratch / "r")
        for i in range(3):
            (repo / "f.txt").write_text(f"version {i}\n" * 50)
            gm.git(repo, "add", "f.txt")
            gm.git(repo, "commit", "-q", "-m", f"c{i}")
        work = gm.new_dir(scratch / "work")
        res = gm.measure(repo, work, scratch, [("gc", ["gc", "-q", "--prune=now"])])
        res["sync"] = gm.sync_replay(repo, work, scratch)

        self.assertEqual(res["commits"], 3)
        self.assertEqual(res["repacked"]["gc"]["exit"], 0)
        self.assertEqual(res["sync"]["pushes"], 3)
        self.assertTrue(self.armed.sentinel_intact(), "the sentinel repository changed")
        self.assertEqual(self.armed.fired(), [], "a hook or fsmonitor from the armed environment ran")
        self.assertEqual(list(outer_tmp.iterdir()), [], "temporary files were left behind")
        # the repository has only its own objects, not the sentinel's as alternates
        self.assertFalse((repo / ".git" / "objects" / "info" / "alternates").exists())

    def test_existing_paths_refused(self):
        existing = self.tmp / "existing"
        existing.mkdir()
        (existing / "sentinel.txt").write_text("keep\n")
        link = self.tmp / "link"
        link.symlink_to(existing)
        src = gm.init_repo(gm.new_dir(self.tmp / "s") / "r")
        for dst in (existing, link):
            with self.subTest(dst=dst.name):
                with self.assertRaises(FileExistsError):
                    gm.new_dir(dst)
                with self.assertRaises(FileExistsError):
                    gm.copy_repo(src, dst)
                with self.assertRaises(FileExistsError):
                    gm.init_repo(dst)
        self.assertEqual((existing / "sentinel.txt").read_text(), "keep\n")
        self.assertEqual(sorted(p.name for p in existing.iterdir()), ["sentinel.txt"])

    def test_synthetic_refuses_foreign_root_and_existing_out(self):
        foreign = self.tmp / "foreign"
        foreign.mkdir()
        (foreign / "sentinel.txt").write_text("keep\n")
        wl = self.tmp / "workload.json"
        wl.write_text("{}")
        r = subprocess.run([sys.executable, "-I", str(HERE / "synthetic.py"), "--root", str(foreign), "--workload",
                            str(wl), "--out", str(self.tmp / "out.json")], capture_output=True, text=True)
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("not a directory synth workload created", r.stderr)
        marked = self.tmp / "marked"
        marked.mkdir()
        (marked / ".gitgrowth-scratch").touch()
        out = self.tmp / "exists.json"
        out.write_text("keep\n")
        r = subprocess.run([sys.executable, "-I", str(HERE / "synthetic.py"), "--root", str(marked), "--workload",
                            str(wl), "--out", str(out)], capture_output=True, text=True)
        self.assertNotEqual(r.returncode, 0)
        self.assertEqual(out.read_text(), "keep\n")
        self.assertFalse((marked / "work").exists())
        self.assertEqual((foreign / "sentinel.txt").read_text(), "keep\n")


class HostileRepoTest(TempCase):
    """A scratch root whose repository leads git elsewhere: a symlinked .git, a gitfile, alternates, a commondir,
    core.worktree, an include, a symlink inside .git, a hook. The sentinel repository outside the root has loose
    objects, so a gc that reached it would pack them and change its bytes; the tools must refuse before that."""

    HOSTILE = ("dotgit-symlink", "repo-symlink", "gitfile", "alternates", "commondir", "worktrees", "core-worktree",
               "include", "symlink-in-gitdir", "hook", "foreign-file-in-root")

    def setUp(self):
        super().setUp()
        self.sentinel = self.tmp / "sentinel"
        self.git_plain("init", "-q", "-b", "main", str(self.sentinel))
        (self.sentinel / "keep.txt").write_text("sentinel\n")
        self.git_plain("-C", str(self.sentinel), "add", "keep.txt")
        self.git_plain("-C", str(self.sentinel), "commit", "-q", "-m", "sentinel")
        self.loose = sorted(p.relative_to(self.sentinel) for p in (self.sentinel / ".git" / "objects").rglob("*")
                            if p.is_file())
        self.assertTrue(self.loose, "the sentinel must have loose objects for gc to change")
        self.before = snapshot(self.sentinel)

    def git_plain(self, *args: str) -> None:
        subprocess.run(["git", *args], env=plain_env(), check=True, capture_output=True)

    def root(self, case: str | None) -> Path:
        """A root like synth's: the marker, one honest repository, and one hostile entry named case (if any)."""
        root = self.tmp / f"root-{case}"
        root.mkdir()
        (root / ".gitgrowth-scratch").touch()
        self.repo(root / "honest")
        if case is None:
            return root
        evil = root / case
        sgit = self.sentinel / ".git"
        if case == "dotgit-symlink":
            evil.mkdir()
            (evil / ".git").symlink_to(sgit)
        elif case == "repo-symlink":
            evil.symlink_to(self.sentinel)
        elif case == "gitfile":
            evil.mkdir()
            (evil / ".git").write_text(f"gitdir: {sgit}\n")
        else:
            self.repo(evil)
            gd = evil / ".git"
            if case == "alternates":
                (gd / "objects" / "info" / "alternates").write_text(f"{sgit / 'objects'}\n")
            elif case == "commondir":
                (gd / "commondir").write_text(f"{sgit}\n")
            elif case == "worktrees":
                (gd / "worktrees" / "w").mkdir(parents=True)
                (gd / "worktrees" / "w" / "gitdir").write_text(f"{self.sentinel}/.git\n")
            elif case == "core-worktree":
                self.git_plain("-C", str(evil), "config", "core.worktree", str(self.sentinel))
            elif case == "include":
                inc = self.tmp / f"inc-{case}"
                inc.write_text(f"[core]\n\tworktree = {self.sentinel}\n")
                self.git_plain("-C", str(evil), "config", "include.path", str(inc))
            elif case == "symlink-in-gitdir":
                shutil.rmtree(gd / "refs" / "tags")
                (gd / "refs" / "tags").symlink_to(sgit / "refs" / "tags")
            elif case == "hook":
                (gd / "hooks" / "pre-push").write_text("#!/bin/sh\nexit 0\n")
            elif case == "foreign-file-in-root":
                shutil.rmtree(evil)
                evil.write_text("not a repository\n")
        return root

    def repo(self, path: Path) -> None:
        self.git_plain("init", "-q", "-b", "main", str(path))
        (path / "f.txt").write_text("x\n")
        self.git_plain("-C", str(path), "add", "f.txt")
        self.git_plain("-C", str(path), "commit", "-q", "-m", "c")

    def assert_sentinel_intact(self):
        self.assertEqual(snapshot(self.sentinel), self.before, "the sentinel repository changed")

    def test_control_gc_through_symlinked_dotgit_reaches_sentinel(self):
        """Without the check, gc on a repository whose .git is a symlink packs the sentinel's loose objects."""
        root = self.root("dotgit-symlink")
        subprocess.run(["git", "-C", str(root / "dotgit-symlink"), "gc", "-q", "--prune=now"], env=plain_env(),
                       check=True)
        self.assertNotEqual(snapshot(self.sentinel), self.before)

    def test_synthetic_refuses_hostile_root(self):
        wl = self.tmp / "workload.json"
        wl.write_text("{}")
        for case in self.HOSTILE:
            with self.subTest(case):
                root = self.root(case)
                out = self.tmp / f"out-{case}.json"
                r = subprocess.run([sys.executable, "-I", str(HERE / "synthetic.py"), "--root", str(root),
                                    "--workload", str(wl), "--out", str(out)], capture_output=True, text=True,
                                   env=plain_env(), timeout=60)
                self.assertNotEqual(r.returncode, 0, r.stdout + r.stderr)
                self.assertIn("refusing", r.stderr)
                self.assertNotIn("measuring", r.stdout)
                self.assertFalse(out.exists())
                self.assertFalse((root / "work").exists())
                self.assert_sentinel_intact()

    def test_gitmeasure_refuses_hostile_repo(self):
        for case in self.HOSTILE:
            if case == "foreign-file-in-root":
                continue
            with self.subTest(case):
                root = self.root(case)
                work = gm.new_dir(root / "work")
                with self.assertRaises(gm.UnsafeRepo):
                    gm.measure(root / case, work, root)
                with self.assertRaises(gm.UnsafeRepo):
                    gm.sync_replay(root / case, work, root)
                self.assertEqual(list(work.iterdir()), [])
                self.assert_sentinel_intact()

    def test_gitmeasure_refuses_repo_or_work_outside_scratch(self):
        root = self.root(None)
        work = gm.new_dir(root / "work")
        with self.assertRaises(gm.UnsafeRepo):
            gm.measure(self.sentinel, work, root)
        with self.assertRaises(gm.UnsafeRepo):
            gm.measure(root / "honest", gm.new_dir(self.tmp / "work-outside"), root)
        with self.assertRaises(gm.UnsafeRepo):
            gm.sync_replay(self.sentinel, work, root)
        self.assertEqual(list(work.iterdir()), [])
        self.assert_sentinel_intact()

    def test_honest_repo_passes(self):
        root = self.root(None)
        self.assertEqual(gm.check_repo(root / "honest", root), Path(os.path.realpath(root / "honest" / ".git")))


class RunShTest(TempCase):
    """run.sh refuses unsafe SCRATCH and RESULTS before it builds or writes anything."""

    def run_sh(self, scratch: Path, results: Path, env: dict | None = None) -> subprocess.CompletedProcess:
        return subprocess.run(["bash", str(HERE / "run.sh"), str(scratch), str(results)], capture_output=True,
                              text=True, env=env or plain_env(), timeout=60)

    def test_refusals(self):
        existing = self.tmp / "existing"
        existing.mkdir()
        (existing / "sentinel.txt").write_text("keep\n")
        link = self.tmp / "link"
        link.symlink_to(existing)
        dangling = self.tmp / "dangling"
        dangling.symlink_to(self.tmp / "nowhere")
        tree_link = self.tmp / "tree-link"
        tree_link.symlink_to(HERE)
        tree = HERE.parents[3]
        cases = {
            "existing scratch": (existing, self.tmp / "r1", "exists"),
            "symlink scratch": (link, self.tmp / "r2", "exists"),
            "dangling symlink scratch": (dangling, self.tmp / "r3", "exists"),
            "existing results": (self.tmp / "s4", existing, "exists"),
            "scratch inside the tree": (HERE / "scratch-x", self.tmp / "r5", "overlaps the source tree"),
            "scratch inside the tree via a symlinked parent": (tree_link / "scratch-x", self.tmp / "r6",
                                                               "overlaps the source tree"),
            "results inside the tree": (self.tmp / "s7", HERE / "results-x", "overlaps the source tree"),
            "results at the tree root": (self.tmp / "s8", tree / "results-x", "overlaps the source tree"),
            "results inside the tree via a symlinked parent": (self.tmp / "s9", tree_link / "results-x",
                                                               "overlaps the source tree"),
            "results inside scratch": (self.tmp / "s10", self.tmp / "s10" / "results", "parent"),
            "scratch inside results": (self.tmp / "r11" / "scratch", self.tmp / "r11", "parent"),
            "same path twice": (self.tmp / "s12", self.tmp / "s12", "must not contain each other"),
            "missing parent": (self.tmp / "no" / "such" / "dir", self.tmp / "r13", "parent"),
        }
        # If a refusal regresses, run.sh must not go on to build and write into the tree: go and python3 are stubs
        # that fail, and whatever run.sh created under the names above is removed again.
        stubs = self.tmp / "stubs"
        stubs.mkdir()
        for tool in ("go", "python3"):
            (stubs / tool).write_text("#!/bin/sh\nexit 97\n")
            (stubs / tool).chmod(0o755)
        env = plain_env(PATH=f"{stubs}:{os.environ['PATH']}")
        planted = (HERE / "scratch-x", HERE / "results-x", tree / "results-x")
        for p in planted:
            self.assertFalse(os.path.lexists(p), f"{p} exists before the test")
            self.addCleanup(lambda p=p: shutil.rmtree(p) if os.path.lexists(p) else None)
        before = snapshot(self.tmp)
        tree_before = sorted(p.name for p in HERE.iterdir())
        root_before = sorted(p.name for p in tree.iterdir())
        for name, (s, r, msg) in cases.items():
            with self.subTest(name):
                res = self.run_sh(s, r, env)
                self.assertNotEqual(res.returncode, 0, res.stderr)
                self.assertIn(msg, res.stderr)
                self.assertEqual(snapshot(self.tmp), before, f"{name}: run.sh changed the test directory")
                self.assertEqual(sorted(p.name for p in tree.iterdir()), root_before)
        self.assertEqual(sorted(p.name for p in HERE.iterdir()), tree_before)
        self.assertEqual((existing / "sentinel.txt").read_text(), "keep\n")

    def test_report_failure_fails_the_run(self):
        """run.sh exits non-zero when report.py fails. go, python3 and the built binaries are stubs here, so only
        run.sh's own control flow runs; the control with a passing report.py exits 0."""
        stubs = self.tmp / "stubs"
        stubs.mkdir()
        (stubs / "go").write_text('#!/bin/sh\nwhile [ $# -gt 0 ]; do [ "$1" = -o ] && out=$2; shift; done\n'
                                  'printf "#!/bin/sh\\nexit 0\\n" > "$out" && chmod +x "$out"\n')
        (stubs / "python3").write_text('#!/bin/sh\ncase "$*" in *report.py*) exit "${REPORT_EXIT:-0}" ;; esac\n'
                                       'exit 0\n')
        for f in stubs.iterdir():
            f.chmod(0o755)
        for code, want in (("0", 0), ("3", 1)):
            with self.subTest(report_exit=code):
                env = plain_env(PATH=f"{stubs}:{os.environ['PATH']}", REPORT_EXIT=code)
                res = self.run_sh(self.tmp / f"s{code}", self.tmp / f"r{code}", env)
                self.assertEqual(res.returncode, want, res.stderr)
                self.assertEqual("results in" in res.stderr, want == 0, res.stderr)


@unittest.skipUnless(os.environ.get("GITGROWTH_E2E") == "1", "set GITGROWTH_E2E=1 to run run.sh end to end")
class RunShEndToEndTest(TempCase):
    """A quick run.sh under the armed environment: the checks pass, the sentinel stays, no canary fires, and nothing
    is written outside SCRATCH and RESULTS."""

    def test_quick_run_under_armed_env(self):
        armed = Armed(self.tmp / "armed")
        go_env = {k: subprocess.run(["go", "env", k], capture_output=True, text=True, check=True).stdout.strip()
                  for k in ("GOCACHE", "GOMODCACHE", "GOPATH")}
        env = dict(armed.env, GITGROWTH_QUICK="1", **go_env)
        outer_tmp = self.tmp / "outer-tmp"
        outer_tmp.mkdir()
        env["TMPDIR"] = str(outer_tmp)
        scratch, results = self.tmp / "scratch", self.tmp / "results"
        before = snapshot(self.tmp)
        tree_status = subprocess.run(["git", "-C", str(HERE), "status", "--porcelain"], env=plain_env(),
                                     capture_output=True, text=True, check=True).stdout
        log = self.tmp.parent / f"{self.tmp.name}.log"
        with open(log, "w") as f:
            r = subprocess.run(["bash", str(HERE / "run.sh"), str(scratch), str(results)], stdout=f,
                               stderr=subprocess.STDOUT, env=env, timeout=1800)
        self.addCleanup(log.unlink)
        self.assertEqual(r.returncode, 0, log.read_text()[-4000:])
        self.assertIn("all assertions hold", log.read_text())
        self.assertTrue(armed.sentinel_intact(), "the sentinel repository changed")
        self.assertEqual(armed.fired(), [], "a hook or fsmonitor from the armed environment ran")
        after = snapshot(self.tmp, skip=(scratch, results))
        changed = {k for k in before.keys() | after.keys() if before.get(k) != after.get(k)}
        # The armed HOME is also the toolchain's: version-manager shims and Go telemetry keep their state there. Those
        # paths only; run.sh itself must write nothing outside SCRATCH and RESULTS.
        toolchain = ("armed/home/.cache", "armed/home/.local", "armed/home/.config/go")
        foreign = sorted(k for k in changed if k not in before and not k.startswith(toolchain))
        self.assertEqual(foreign, [], "run.sh wrote outside its directories")
        self.assertEqual(sorted(k for k in changed if k in before), [], "run.sh changed existing files")
        self.assertEqual(list(outer_tmp.iterdir()), [])
        for p in (scratch, results):
            self.assertEqual(stat.S_IMODE(p.stat().st_mode), 0o700, p)
        self.assertEqual(subprocess.run(["git", "-C", str(HERE), "status", "--porcelain"], env=plain_env(),
                                        capture_output=True, text=True, check=True).stdout, tree_status)

        # synth refuses an existing -out and an existing -result, and import refuses a base outside its scratch.
        synth = str(scratch / "bin" / "synth")
        foreign = self.tmp / "foreign"
        foreign.mkdir()
        (foreign / "facts.db").write_text("not a base\n")
        jsonl = foreign / "in.jsonl"
        jsonl.write_text("")
        base = str(scratch / "synth" / "sqlite-every" / "memory" / "facts.db")
        refusals = {
            "-out must be a new directory": [synth, "race", "-db", base, "-out", str(foreign), "-result", str(self.tmp / "x.json"),
                              "-for", "1s"],
            "-result must be a new file": [synth, "workload", "-out", str(self.tmp / "w"), "-result", str(foreign / "facts.db"),
                                 "-initial", "1", "-commits", "0"],
            "is not in a directory synth created": [synth, "import", "-in", str(jsonl), "-db", str(foreign / "facts.db")],
        }
        for name, cmd in refusals.items():
            with self.subTest(name):
                res = subprocess.run(cmd, capture_output=True, text=True, env=plain_env())
                self.assertNotEqual(res.returncode, 0, name)
                self.assertIn(name, res.stderr)
        self.assertEqual(sorted(p.name for p in foreign.iterdir()), ["facts.db", "in.jsonl"])
        self.assertEqual((foreign / "facts.db").read_text(), "not a base\n")
        self.assertFalse((self.tmp / "x.json").exists())


if __name__ == "__main__":
    unittest.main()
