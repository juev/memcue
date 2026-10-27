#!/usr/bin/env python3
"""Tests of the cases mode of search_stand.py (#510). A fake memcue stands in for process errors and answer shapes;
MEMCUE_BIN, when set, runs that binary over the synthetic case file in the stand's scratch environment, with no
sandbox. Nothing here opens a live base.

    MEMCUE_BIN=bin/memcue python3 -I docs/research/scripts/test_search_stand.py
"""

from __future__ import annotations

import contextlib
import copy
import io
import json
import os
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
import search_stand as stand  # noqa: E402

SYNTHETIC = HERE / "search_stand" / "synthetic.json"

CASE = {
    "origin": "synthetic",
    "labeling": "test labels",
    "complete_labels": True,
    "repos": {"app": "example.com/team/app"},
    "facts": [
        {"key": "a", "repo": "app", "kind": "rule", "text": "fact a"},
        {"key": "b", "repo": "app", "kind": "decision", "text": "fact b", "keywords": "b"},
        {"key": "c", "repo": "app", "kind": "environment", "scope": "global", "text": "fact c"},
    ],
    "queries": [
        {"id": "q1", "class": "short", "repo": "app", "text": "first", "useful": ["a"], "misleading": ["c"]},
        {"id": "q2", "class": "prompt", "repo": "app", "text": "second"},
    ],
}

# A fake memcue: add hands out ids F0, F1, ... and remembers them next to the base; search returns the first two.
FAKE = r'''#!{python}
import json, os, pathlib, subprocess, sys, time
MODE = {mode!r}
cmd = sys.argv[1]
base = pathlib.Path(os.environ["MEMCUE_FACTS"])
ids = base.parent / "ids"
if cmd == "version":
    print("fake 0")
    sys.exit(0)
if cmd == "add":
    if MODE == "env":
        (base.parent / "env.json").write_text(json.dumps(dict(os.environ)))
    if MODE == "add-fail":
        sys.stderr.write("boom")
        sys.exit(3)
    if MODE == "claude":
        subprocess.run(["claude", "-p", "hi"])
    if MODE != "no-base":
        base.touch()
    n = len(ids.read_text().split()) if ids.exists() else 0
    fid = "F1" if MODE == "add-dup" else "not an id!" if MODE == "add-junk" else f"F{{n}}"
    with ids.open("a") as f:
        f.write(fid + "\n")
    print(fid)
    sys.exit(0)
known = ids.read_text().split()
if MODE == "search-fail":
    sys.stderr.write("search broke")
    sys.exit(1)
if MODE == "search-junk":
    print("not json")
elif MODE == "search-alien":
    print(json.dumps({{"facts": [{{"id": "ZZZ", "content": "x"}}]}}))
elif MODE == "search-dup":
    print(json.dumps({{"facts": [{{"id": known[0], "content": "x"}}, {{"id": known[0], "content": "x"}}]}}))
elif MODE == "search-null":
    print(json.dumps({{"facts": None}}))
elif MODE == "search-bare-null":
    print("null")
elif MODE == "search-no-facts":
    print(json.dumps({{"hints": ["No stored facts match."]}}))
elif MODE == "search-scalar":
    print(json.dumps({{"facts": "F0"}}))
elif MODE == "search-id-only":
    print(json.dumps({{"facts": [{{"id": known[0]}}]}}))
elif MODE == "search-bad-id":
    print(json.dumps({{"facts": [{{"id": 0, "content": "x"}}]}}))
elif MODE == "search-array":
    print(json.dumps([{{"id": i, "content": "abcdefg"}} for i in known[:2]]))
elif MODE == "search-many":
    print(json.dumps({{"facts": [{{"id": i}} for i in known] * 3}}))
elif MODE == "search-slow":
    time.sleep(5)
else:
    print(json.dumps({{"facts": [{{"id": i, "content": "abcdefg"}} for i in known[:2]]}}))
'''


class Isolated(unittest.TestCase):
    """Every test runs with a scratch HOME and none of the caller's memcue variables, so live_bases names nothing
    real and a test cannot reach the user's base."""

    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp(prefix="stand-test-")).resolve()
        self.env = mock_env({"HOME": str(self.tmp / "home"), "MEMCUE_FACTS": None, "CLAUDE_CONFIG_DIR": None})
        self.env.__enter__()

    def tearDown(self):
        self.env.__exit__(None, None, None)

    def write_case(self, case: dict, name: str = "case.json") -> Path:
        path = self.tmp / name
        path.write_text(json.dumps(case), encoding="utf-8")
        return path

    def fake(self, mode: str = "") -> Path:
        path = self.tmp / f"memcue-{mode or 'ok'}"
        path.write_text(FAKE.format(python=sys.executable, mode=mode), encoding="utf-8")
        path.chmod(0o755)
        return path

    def stand(self, *argv: str) -> tuple[int, str, str]:
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            code = stand.main(list(argv))
        return code, out.getvalue(), err.getvalue()


@contextlib.contextmanager
def mock_env(changes: dict):
    saved = {k: os.environ.get(k) for k in changes}
    for k, v in changes.items():
        if v is None:
            os.environ.pop(k, None)
        else:
            os.environ[k] = v
    try:
        yield
    finally:
        for k, v in saved.items():
            if v is None:
                os.environ.pop(k, None)
            else:
                os.environ[k] = v


class CaseFile(Isolated):
    def refused(self, mutate, message: str):
        case = copy.deepcopy(CASE)
        mutate(case)
        with self.assertRaises(stand.StandError) as e:
            stand.load_cases(self.write_case(case))
        self.assertIn(message, str(e.exception))

    def test_synthetic_file_loads(self):
        case = stand.load_cases(SYNTHETIC)
        self.assertEqual(len(case["queries"]), 46)
        self.assertTrue(case["complete_labels"])

    def test_good_case_loads(self):
        self.assertEqual(stand.load_cases(self.write_case(CASE))["queries"][0]["id"], "q1")

    def test_wrong_labels_are_refused(self):
        self.refused(lambda c: c["queries"][0].update(useful=["nope"]), "unknown facts ['nope']")
        self.refused(lambda c: c["queries"][0].update(related=["a"]), "labelled both useful and related")
        self.refused(lambda c: c["queries"][0].update(misleading="c"), "must be a list of fact keys")
        self.refused(lambda c: c["queries"][0].update(useful=[1]), "must be a list of fact keys")
        self.refused(lambda c: c["queries"][0].update(usefull=["a"]), "unknown fields ['usefull']")
        self.refused(lambda c: c["queries"][0].update({"class": "chat"}), "class must be one of")
        self.refused(lambda c: c["facts"][0].update(kind="note"), "kind must be one of")
        self.refused(lambda c: c.update(complete_labels="yes"), "complete_labels must be true or false")

    def test_ids_are_checked(self):
        for bad in ("../x", "a/b", "", ".", "Q1", "-x", "x" * 65, 7):
            self.refused(lambda c, bad=bad: c["queries"][0].update(id=bad), "must match")
        self.refused(lambda c: c["facts"][1].update(key="a"), "duplicate fact key a")
        self.refused(lambda c: c["queries"][1].update(id="q1"), "duplicate query id q1")
        self.refused(lambda c: c["repos"].update({"../up": "example.com/x/y"}), "must match")
        self.refused(lambda c: c["repos"].update(app="example.com/../etc"), "must look like host/group/project")
        self.refused(lambda c: c["repos"].update(app="-oProxyCommand=x/y"), "must look like host/group/project")
        self.refused(lambda c: c["queries"][0].update(repo="other"), "is not in repos")

    def test_queries_must_be_distinct(self):
        self.refused(lambda c: c["queries"][1].update(text="  FIRST "), "every query must be distinct")

    def test_real_or_unlabelled_files_are_refused(self):
        self.refused(lambda c: c.update(origin="real"), "origin must be 'synthetic'")
        self.refused(lambda c: c.pop("labeling"), "missing fields ['labeling']")
        self.refused(lambda c: c.update(base="/home/me/facts.db"), "unknown fields ['base']")
        self.refused(lambda c: c["queries"][0].update(text="a\0b"), "without NUL")


class Paths(Isolated):
    def test_out_must_be_new_and_outside_the_repository(self):
        existing = self.tmp / "exists"
        existing.mkdir()
        link = self.tmp / "link"
        link.symlink_to(existing)
        dangling = self.tmp / "dangling"
        dangling.symlink_to(self.tmp / "nowhere")
        for path in (existing, link, dangling, stand.REPO_ROOT / "run", self.tmp / "missing" / "run", "."):
            with self.assertRaises(stand.StandError, msg=path):
                stand.make_out(str(path))
        self.assertFalse((self.tmp / "nowhere").exists())
        self.assertFalse((stand.REPO_ROOT / "run").exists())
        out = stand.make_out(str(self.tmp / "new"))
        self.assertEqual(oct(out.stat().st_mode & 0o777), "0o700")

    def test_write_new_refuses_existing_names_and_links(self):
        target = self.tmp / "target"
        target.write_text("keep")
        sym = self.tmp / "sym"
        sym.symlink_to(target)
        hard = self.tmp / "hard"
        os.link(target, hard)
        for path in (target, sym, hard):
            with self.assertRaises(stand.StandError):
                stand.write_new(path, "overwritten")
        self.assertEqual(target.read_text(), "keep")

    def test_arm_flags_and_names(self):
        fake = str(self.fake())
        self.assertEqual(stand.parse_arm("wide:-min-score 0", fake)["args"], ["-min-score", "0"])
        for spec in ("x:-n 3", "x:--n=3", "x:-cwd=/", "x:-json", "x:-- -n", "../x", "x@/no/such/memcue", "x:'"):
            with self.assertRaises(stand.StandError, msg=spec):
                stand.parse_arm(spec, fake)

    def test_arm_takes_only_known_search_flags_with_their_values(self):
        fake = str(self.fake())
        for spec, args in (
            ("old@" + fake, []),
            ("x:--min-score=0.25 -in project,global -kind=rule,pitfall -since 2026-01-31 -expired -all-words=false",
             ["--min-score=0.25", "-in", "project,global", "-kind=rule,pitfall", "-since", "2026-01-31",
              "-expired", "-all-words=false"]),
            ("x:-in repo:example.com/team/app,shelf:ops,dir:/srv/app", ["-in", "repo:example.com/team/app,shelf:ops,dir:/srv/app"]),
        ):
            self.assertEqual(stand.parse_arm(spec, fake)["args"], args, spec)
        for spec, why in (
            ("bad:foo", "not a flag"),                          # a stray word becomes query text in memcue
            ("x:-min-score 0 foo", "not a flag"),
            ("x:-expired false", "not a flag"),                 # a bool flag does not take the next word
            ("x:-min-score", "needs a value"),
            ("x:-min-score=", "min-score"),
            ("x:-min-score -1", "min-score"),
            ("x:-min-score 1.5", "min-score"),
            ("x:-min-score nan", "min-score"),
            ("x:-min-score 0 --min-score=0.5", "twice"),
            ("x:-expired -expired=false", "twice"),
            ("x:-expired=maybe", "expired"),
            ("x:-kind rule,chat", "kind"),
            ("x:-kind rule,,pitfall", "kind"),
            ("x:-kind=rule,rule", "kind"),
            ("x:-in ''", "in"),
            ("x:-in project,", "in"),
            ("x:-in -n", "in"),
            ("x:-since 2026-02-30", "since"),
            ("x:-since yesterday", "since"),
            ("x:-limit 3", "unknown"),
            ("x:---expired", "flag"),
            ("x:-", "not a flag"),
        ):
            with self.assertRaises(stand.StandError, msg=spec) as e:
                stand.parse_arm(spec, fake)
            self.assertIn(why, str(e.exception), spec)

    def test_a_stray_arm_word_stops_the_run_before_any_process(self):
        out = self.tmp / "out"
        code, _, err = self.stand("--cases", str(self.write_case(CASE)), "--out", str(out),
                                  "--binary", str(self.fake()), "--arm", "baseline", "--arm", "bad:foo")
        self.assertEqual(code, 2)
        self.assertIn("'foo' is not a flag", err)
        self.assertFalse(out.exists())

    def test_existing_output_is_left_alone(self):
        out = self.tmp / "out"
        out.mkdir()
        (out / "results.json").write_text("old")
        code, _, err = self.stand("--cases", str(self.write_case(CASE)), "--out", str(out),
                                  "--binary", str(self.fake()))
        self.assertEqual(code, 2)
        self.assertIn("must not exist", err)
        self.assertEqual((out / "results.json").read_text(), "old")


class Processes(Isolated):
    def run_mode(self, mode: str, *extra: str) -> tuple[int, str, Path]:
        out = self.tmp / f"out-{mode or 'ok'}"
        code, _, err = self.stand("--cases", str(self.write_case(CASE)), "--out", str(out),
                                  "--binary", str(self.fake(mode)), *extra)
        return code, err, out

    def test_every_process_error_stops_the_run_without_results(self):
        for mode, message in [
            ("add-fail", "add exited 3: boom"),
            ("add-dup", "is already fact a"),
            ("add-junk", "expected a fact id"),
            ("no-base", "did not create"),
            ("search-fail", "search exited 1: search broke"),
            ("search-junk", "not JSON"),
            ("search-null", "facts must be a list, got NoneType"),
            ("search-bare-null", "facts must be a list, got NoneType"),
            ("search-no-facts", "a JSON object without facts"),
            ("search-scalar", "facts must be a list, got str"),
            ("search-id-only", "string id and content"),
            ("search-bad-id", "string id and content"),
            ("search-alien", "a fact the case did not write"),
            ("search-dup", "twice"),
            ("search-many", "at most 5"),
            ("claude", "memcue ran claude, a model call"),
        ]:
            code, err, out = self.run_mode(mode)
            self.assertEqual(code, 2, mode)
            self.assertIn(message, err, mode)
            self.assertFalse((out / "results.json").exists(), mode)

    def test_timeout_stops_the_run(self):
        code, err, _ = self.run_mode("search-slow", "--timeout", "1")
        self.assertEqual(code, 2)
        self.assertIn("timed out after 1 s", err)

    def test_memcue_sees_only_the_scratch_environment(self):
        with mock_env({"MEMCUE_API_URL": "https://example.invalid", "MEMCUE_API_KEY": "secret",
                       "ANTHROPIC_API_KEY": "secret"}):
            code, err, out = self.run_mode("env")
        self.assertEqual(code, 0, err)
        env = json.loads((out / "arm-baseline" / "env.json").read_text())
        self.assertFalse({"MEMCUE_API_URL", "MEMCUE_API_KEY", "ANTHROPIC_API_KEY"} & set(env))
        self.assertEqual(env["MEMCUE_FACTS"], str(out / "arm-baseline" / "facts.db"))
        self.assertEqual(env["HOME"], str(out / "arm-baseline" / "home"))
        self.assertTrue(env["PATH"].startswith(str(out / "arm-baseline" / "trap")))

    def test_a_good_run_writes_results_and_grades_them(self):
        code, err, out = self.run_mode("", "--arm", "baseline", "--arm", "other:-in all", "--repeat", "2")
        self.assertEqual(code, 0, err)
        results = json.loads((out / "results.json").read_text())
        self.assertEqual([a["arm"] for a in results["arms"]], ["baseline", "other"])
        self.assertEqual(results["arms"][1]["args"], ["-in", "all"])
        q1, q2 = results["arms"][0]["runs"]
        self.assertEqual((q1["got"], len(q1["ms"]), q1["unstable"]), (["a", "b"], 2, False))
        g = stand.grade_cases(results["arms"][0]["runs"], 5, True)
        self.assertEqual((g["useful_in_top_k"], g["answerable"], g["useful"], g["noise"], g["misleading"]),
                         (1, 1, 1, 3, 0))
        self.assertEqual((g["no_memory"], g["no_memory_given_facts"], g["invocations"]), (1, 1, 4))

    def test_answers_before_0_17_are_a_bare_list(self):
        code, err, out = self.run_mode("search-array")
        self.assertEqual(code, 0, err)
        q1, q2 = json.loads((out / "results.json").read_text())["arms"][0]["runs"]
        self.assertEqual((q1["got"], q1["sizes"], q2["got"]), (["a", "b"], [7, 7], ["a", "b"]))

    def test_search_facts_shapes(self):
        facts = [{"id": "F0", "content": "x"}]
        self.assertEqual(stand.search_facts(json.dumps({"facts": facts, "hints": ["h"]})), facts)
        self.assertEqual(stand.search_facts(json.dumps(facts)), facts)
        self.assertEqual(stand.search_facts('{"facts": []}'), [])
        self.assertEqual(stand.search_facts("[]"), [])
        for bad in ("null", '{"facts": null}', '{"hints": []}', '"F0"', "7", '{"facts": {"id": "F0"}}', "", "nope"):
            with self.assertRaises(stand.StandError, msg=bad):
                stand.search_facts(bad)


class Isolation(Isolated):
    """The stand claims only what it does: it neither opens a live base nor reports one unchanged."""

    def test_no_live_base_is_opened_or_claimed(self):
        live = self.tmp / "home" / ".claude" / "memory" / "facts.db"
        live.mkdir(parents=True)  # a directory: opening it to hash it would raise
        caller = self.tmp / "caller-facts.db"
        caller.write_bytes(b"caller base")
        forbidden = (str(live), str(caller))
        real_open, real_os_open = open, os.open

        def guard(opener):
            def wrapped(file, *a, **kw):
                if str(file) in forbidden:
                    raise AssertionError(f"the stand opened {file}")
                return opener(file, *a, **kw)
            return wrapped

        with mock_env({"MEMCUE_FACTS": str(caller), "CLAUDE_CONFIG_DIR": str(self.tmp / "home" / ".claude")}), \
                mock.patch("builtins.open", guard(real_open)), mock.patch("io.open", guard(real_open)), \
                mock.patch("os.open", guard(real_os_open)):
            code, report, err = self.stand("--cases", str(self.write_case(CASE)), "--out", str(self.tmp / "out"),
                                           "--binary", str(self.fake()))
        self.assertEqual(code, 0, err)
        results = json.loads((self.tmp / "out" / "results.json").read_text())
        self.assertNotIn("live_bases_unchanged", results)
        self.assertNotIn("unchanged", report.lower())
        self.assertIn("not sandboxed", results["isolation"])
        self.assertIn("not sandboxed", report)
        self.assertEqual(caller.read_bytes(), b"caller base")
        self.assertFalse(any(live.iterdir()))


class Malformed(Isolated):
    """A wrong type anywhere in the case file or a bad flag value ends with a diagnostic, never a traceback."""

    def test_wrong_types_in_the_case_file_are_refused(self):
        def paths(o, p=()):
            yield p
            items = o.items() if isinstance(o, dict) else enumerate(o) if isinstance(o, list) else ()
            for k, v in items:
                yield from paths(v, p + (k,))

        for path in paths(CASE):
            for bad in ([], {}, None, 1, True, [[]], [{}]):
                case = copy.deepcopy(CASE)
                if path:
                    target = case
                    for k in path[:-1]:
                        target = target[k]
                    target[path[-1]] = bad
                else:
                    case = bad
                try:
                    stand.load_cases(self.write_case(case))
                except stand.StandError:
                    pass

    def test_a_list_as_repository_ends_with_a_diagnostic(self):
        for mutate, message in [
            (lambda c: c["facts"][0].update(repo=[]), "facts[0]: repository [] is not in repos"),
            (lambda c: c["queries"][0].update(repo=[]), "queries[0]: repository [] is not in repos"),
            (lambda c: c["queries"][0].update(repo={}), "queries[0]: repository {} is not in repos"),
            (lambda c: c["facts"][0].update(kind=[]), "kind must be one of"),
            (lambda c: c["queries"][0].update({"class": []}), "class must be one of"),
        ]:
            case = copy.deepcopy(CASE)
            mutate(case)
            code, _, err = self.stand("--cases", str(self.write_case(case)), "--out", str(self.tmp / "out"),
                                      "--binary", str(self.fake()))
            self.assertEqual(code, 2, message)
            self.assertIn(message, err)
            self.assertNotIn("Traceback", err)
            self.assertFalse((self.tmp / "out").exists(), message)

    def test_bad_flag_values_end_with_a_diagnostic(self):
        case = str(self.write_case(CASE))
        for flag, value, message in [
            ("--sweeps", "x", "is not a comma-separated list of integers"),
            ("--sweeps", "1,x", "is not a comma-separated list of integers"),
            ("--sweeps", "0,3", "must be positive integers"),
            ("--sweeps", ",", "must be positive integers"),
        ]:
            for mode in (("--cases", case, "--out", str(self.tmp / "out")), ("--prompts", case, "--base", case)):
                err = io.StringIO()
                with contextlib.redirect_stderr(err), self.assertRaises(SystemExit) as e:
                    stand.main([*mode, "--binary", str(self.fake()), flag, value])
                self.assertEqual(e.exception.code, 2, (flag, value))
                self.assertIn(message, err.getvalue())
                self.assertNotIn("Traceback", err.getvalue())
        for value in ("nan", "inf", "0"):
            code, _, err = self.stand("--cases", case, "--out", str(self.tmp / "out"), "--binary",
                                      str(self.fake()), "--timeout", value)
            self.assertEqual(code, 2, value)
            self.assertIn("--timeout must be positive", err)
        self.assertFalse((self.tmp / "out").exists())


class Metrics(unittest.TestCase):
    RUNS = [
        {"id": "q1", "class": "short", "useful": ["a"], "related": ["r"], "misleading": ["m"],
         "got": ["x", "a", "m", "r"], "sizes": [32, 32, 32, 32], "ms": [1.0, 3.0], "unstable": False},
        {"id": "q2", "class": "short", "useful": ["b"], "related": [], "misleading": [],
         "got": ["x"], "sizes": [10], "ms": [2.0, 2.0], "unstable": True},
        {"id": "q3", "class": "prompt", "useful": [], "related": [], "misleading": [],
         "got": [], "sizes": [], "ms": [5.0, 4.0], "unstable": False},
    ]

    def test_complete_labels(self):
        g = stand.grade_cases(self.RUNS, 2, True)
        self.assertEqual((g["useful_in_top_k"], g["answerable"]), (1, 2))
        self.assertEqual((g["useful"], g["noise"], g["unknown"], g["returned"]), (1, 2, 0, 3))
        self.assertAlmostEqual(g["precision"], 1 / 3)
        self.assertEqual((g["no_memory"], g["no_memory_given_facts"], g["memory_unknown"]), (1, 0, 0))
        self.assertEqual((g["chars"], g["tokens_est"], g["unstable"]), (74, 23, 1))
        self.assertEqual((g["ms_p50"], g["ms_p90"]), (2.0, 5.0))
        g4 = stand.grade_cases(self.RUNS, 4, True)
        self.assertEqual((g4["misleading"], g4["queries_with_misleading"], g4["related"]), (1, 1, 1))

    def test_partial_labels_count_unknown_not_noise(self):
        g = stand.grade_cases(self.RUNS, 2, False)
        self.assertEqual((g["noise"], g["unknown"]), (0, 2))
        self.assertEqual(g["precision"], 1.0)
        self.assertEqual((g["no_memory"], g["memory_unknown"]), (0, 1))

    def test_nothing_returned_has_no_precision(self):
        g = stand.grade_cases([self.RUNS[2]], 5, True)
        self.assertIsNone(g["precision"])
        self.assertEqual(stand.fmt(g["precision"], True), "—")

    def test_compare_reports_gains_losses_and_misleading(self):
        cand = copy.deepcopy(self.RUNS)
        cand[0]["got"] = ["x", "r"]          # loses a and the misleading m
        cand[1]["got"] = ["b"]               # gains b
        d = stand.compare(self.RUNS, cand, 3, True)
        self.assertEqual(d, {"gained": ["q2"], "lost": ["q1"], "misleading_gained": [],
                             "misleading_lost": ["q1"], "changed": ["q1", "q2"]})


@unittest.skipUnless(os.environ.get("MEMCUE_BIN"), "MEMCUE_BIN is not set")
class RealBinary(Isolated):
    def test_synthetic_run_is_reproducible_on_fresh_bases(self):
        binary = str(Path(os.environ["MEMCUE_BIN"]).resolve())
        got = []
        for run in ("one", "two"):
            out = self.tmp / run
            code, report, err = self.stand("--cases", str(SYNTHETIC), "--out", str(out), "--binary", binary,
                                           "--arm", "baseline", "--arm", "wide:-min-score 0")
            self.assertEqual(code, 0, err)
            self.assertIn("| baseline | all | 5 | 46 |", report)
            results = json.loads((out / "results.json").read_text())
            got.append({(a["arm"], r["id"]): r["got"] for a in results["arms"] for r in a["runs"]})
        self.assertEqual(got[0], got[1])
        self.assertTrue(any(got[0][("baseline", q)] for q in ("s01", "s02")))

    def test_arm_flags_reach_the_real_binary_as_flags(self):
        binary = str(Path(os.environ["MEMCUE_BIN"]).resolve())
        out = self.tmp / "stray"
        code, _, err = self.stand("--cases", str(SYNTHETIC), "--out", str(out), "--binary", binary,
                                  "--arm", "baseline", "--arm", "bad:foo")
        self.assertEqual((code, out.exists()), (2, False), err)
        # Every accepted shape is one memcue parses as a flag: the query text stays the same, so an arm that sets
        # the defaults explicitly finds exactly what the baseline finds.
        out = self.tmp / "shapes"
        code, _, err = self.stand(
            "--cases", str(SYNTHETIC), "--out", str(out), "--binary", binary, "--arm", "baseline",
            "--arm", "same:-min-score 0.6 -expired=false --all-words=false -since 1970-01-01",
            "--arm", "wide:--min-score=0", "--arm", "wide2:-min-score 0 -kind " + ",".join(stand.KINDS))
        self.assertEqual(code, 0, err)
        arms = {a["arm"]: {r["id"]: r["got"] for r in a["runs"]}
                for a in json.loads((out / "results.json").read_text())["arms"]}
        self.assertEqual(arms["same"], arms["baseline"])
        self.assertEqual(arms["wide2"], arms["wide"])
        self.assertNotEqual(arms["wide"], arms["baseline"])


if __name__ == "__main__":
    unittest.main()
