"""Offline tests of the memcue provider against the real memcue binary.

Run: python3 plugins/hermes/memcue/tests/test_provider.py (MEMCUE_BIN=/path/to/memcue, default: memcue on PATH).
Inside Hermes the real agent modules are used; elsewhere they are stubbed.
"""

import http.server
import importlib.util
import inspect
import json
import threading
import time
import os
import shlex
import shutil
import stat
import subprocess
import sys
import tempfile
import types
import unittest
from collections import Counter
from pathlib import Path

HERE = Path(__file__).resolve().parent.parent


def _stub_hermes():
    try:
        import agent.memory_provider  # noqa: F401
        import tools.registry  # noqa: F401
        return
    except ImportError:
        pass
    agent = types.ModuleType("agent")
    mp = types.ModuleType("agent.memory_provider")

    class MemoryProvider:
        pass

    mp.MemoryProvider = MemoryProvider
    tools = types.ModuleType("tools")
    reg = types.ModuleType("tools.registry")
    reg.tool_error = lambda msg: json.dumps({"error": msg})
    sys.modules.update({"agent": agent, "agent.memory_provider": mp, "tools": tools, "tools.registry": reg})


_stub_hermes()
_spec = importlib.util.spec_from_file_location("memcue_provider", HERE / "__init__.py")
memcue_provider = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(memcue_provider)

BIN = os.environ.get("MEMCUE_BIN") or shutil.which("memcue")


def _gives_a_fact_once(binary: str) -> bool:
    """Whether the binary keeps a fact from coming twice in a session (memcue 0.1.5+). Answered by trying, so a
    build from source, whose version is 'v0.1.5-dirty' or a bare hash, is judged like a release."""
    tmp = tempfile.mkdtemp()
    fact = "Проверка: проект demo слушает порт 55432."
    env = dict(os.environ, MEMCUE_FACTS=os.path.join(tmp, "facts.db"), MEMCUE_EXTRACT="0",
               XDG_STATE_HOME=os.path.join(tmp, "state"))
    env.pop("MEMCUE_RECALL_LOG", None)
    payload = json.dumps({"prompt": "на каком порту слушает проект demo?", "cwd": tmp, "session_id": "probe"})
    try:
        subprocess.run([binary, "add", "-origin", "test", "--", fact], env=env, check=True, capture_output=True)
        runs = [subprocess.run([binary, "hook"], input=payload, env=env, capture_output=True, text=True).stdout
                for _ in range(2)]
        return bool(runs[0].strip()) and not runs[1].strip()
    except (OSError, subprocess.SubprocessError):
        return False
    finally:
        shutil.rmtree(tmp, ignore_errors=True)


ONCE_PER_SESSION = _gives_a_fact_once(BIN) if BIN else False


@unittest.skipUnless(BIN, "memcue binary not found (set MEMCUE_BIN)")
class ProviderTest(unittest.TestCase):
    def setUp(self):
        self.home = tempfile.mkdtemp()
        os.environ["XDG_STATE_HOME"] = str(Path(self.home) / "state")  # memcue's extract.log
        # The suite must never touch a live base: an inherited MEMCUE_FACTS wins over HERMES_HOME and let an earlier
        # run write test facts into a real base and migrate it to another schema. MEMCUE_USAGE_LOG from the container
        # would turn the usage log on behind the tests that assert it is off.
        self._kept_env = {k: os.environ.pop(k, None) for k in ("MEMCUE_FACTS", "MEMCUE_USAGE_LOG")}
        self.addCleanup(self._restore_facts_env)
        self.p = memcue_provider.MemcueMemoryProvider({"binary": BIN})
        self.p.initialize("s1", hermes_home=self.home, agent_context="primary")

    def _restore_facts_env(self):
        for key, value in self._kept_env.items():
            if value is not None:
                os.environ[key] = value

    def tearDown(self):
        self.p._stop.set()
        if self.p._watcher is not None:
            self.p._watcher.join(timeout=2)
        shutil.rmtree(self.home, ignore_errors=True)

    def facts(self):
        return self.memcue("facts")

    def pending(self):
        """Facts that wait for review: what the mirror and the extraction write."""
        return self.memcue("candidates")

    def memcue(self, *args):
        out = subprocess.run([BIN, *args], env=dict(os.environ, MEMCUE_FACTS=self.p._facts),
                             capture_output=True, text=True, check=True).stdout
        return [line for line in out.splitlines() if line]

    def test_base_lives_in_hermes_home(self):
        self.assertEqual(self.p._facts, str(Path(self.home) / "memcue" / "facts.db"))

    def test_provider_matches_real_hermes_api(self):
        from agent.memory_provider import MemoryProvider
        if not hasattr(MemoryProvider, "on_session_switch"):
            self.skipTest("the Hermes MemoryProvider stub is in use")
        self.assertIsInstance(self.p, MemoryProvider)
        for name in ("initialize", "prefetch", "sync_turn", "on_memory_write", "on_session_end",
                     "on_session_switch", "on_pre_compress", "get_tool_schemas", "handle_tool_call",
                     "system_prompt_block", "shutdown", "backup_paths", "is_available",
                     "unavailable_reason"):
            self.assertTrue(callable(getattr(self.p, name, None)), name)
        params = inspect.signature(self.p.on_session_switch).parameters
        for name in ("new_session_id", "reset", "rewound"):
            self.assertIn(name, params)

    def test_no_base_recalls_nothing_and_creates_nothing(self):
        self.assertEqual(self.p.prefetch("какой порт у базы demo postgres"), "")
        self.assertFalse(Path(self.p._facts).exists())

    def test_mirrored_fact_is_kept_out_of_recall(self):
        """A built-in memory note rides with every turn anyway: repeating it from the base would say it twice."""
        self.p.on_memory_write("add", "memory", "Проект demo: база PostgreSQL 18 слушает порт 55432.")
        self.assertEqual(self.p.prefetch("на каком порту слушает база postgres проекта demo?"), "")
        self.assertIn("hermes-memory", self.pending()[0])
        self.assertEqual(self.facts(), [])

    def test_mirrored_facts_come_back_when_suppression_is_off(self):
        p = memcue_provider.MemcueMemoryProvider({"binary": BIN, "suppress_mirrored": False})
        p.initialize("s1", hermes_home=self.home, agent_context="primary")
        p.on_memory_write("add", "memory", "Проект demo: база PostgreSQL 18 слушает порт 55432.")
        query = "на каком порту слушает база postgres проекта demo?"
        self.assertEqual(p.prefetch(query), "")  # a candidate until the user approves it
        self.memcue("approve", self.pending()[0].split()[0])
        self.assertIn("55432", p.prefetch(query))

    def test_replace_corrects_the_same_fact_and_remove_retires_it(self):
        old = "Проект demo: база PostgreSQL 18 слушает порт 55432."
        new = "Проект demo: база PostgreSQL 18 слушает порт 55433."
        self.p.on_memory_write("add", "memory", old)
        self.p.on_memory_write("replace", "memory", new, {"previous_content": old})
        pending = self.pending()
        self.assertEqual(len(pending), 1)
        self.assertIn("55433", pending[0])
        fact_id = pending[0].split()[0]
        self.assertIn(f"{fact_id} v2 active", "\n".join(self.memcue("dump", self.p._facts)))
        self.p.on_memory_write("remove", "memory", "", {"previous_content": new})
        self.assertEqual(self.pending(), [])

    def test_replace_of_a_retired_mirror_adds_a_new_candidate(self):
        old = "Проект demo: база PostgreSQL 18 слушает порт 55432."
        new = "Проект demo: база PostgreSQL 18 слушает порт 55433."
        self.p.on_memory_write("add", "memory", old)
        fact_id = self.pending()[0].split()[0]
        self.memcue("retire", fact_id)
        self.assertEqual(self.pending(), [])
        # The note is edited after vet or a person retired its mirror: correct must not revive the retired fact.
        self.p.on_memory_write("replace", "memory", new, {"previous_content": old})
        pending = self.pending()
        self.assertEqual(len(pending), 1)
        self.assertIn("55433", pending[0])
        new_id = pending[0].split()[0]
        self.assertNotEqual(new_id, fact_id)
        self.assertEqual(self.written()[self.p._key("memory", new)], new_id)
        self.assertIn(f"{fact_id} v2 retired", "\n".join(self.memcue("dump", self.p._facts)))

    def written(self) -> dict:
        return json.loads((Path(self.p._state) / "written.json").read_text(encoding="utf-8"))

    def test_failed_replace_keeps_the_mapping_and_a_later_remove_retires_the_fact(self):
        old = "Проект demo: база PostgreSQL 18 слушает порт 55432."
        new = "Проект demo: база PostgreSQL 18 слушает порт 55433."
        self.p.on_memory_write("add", "memory", old)
        fact_id = self.pending()[0].split()[0]
        old_key = self.p._key("memory", old)
        self.assertEqual(self.written(), {old_key: fact_id})

        real_run = self.p._run
        self.p._run = lambda args, **kwargs: (False, "memcue timed out after 15s")
        try:
            self.p.on_memory_write("replace", "memory", new, {"previous_content": old})
        finally:
            self.p._run = real_run

        # The failed correct must not drop the only link between the entry and its fact.
        self.assertEqual(self.written(), {old_key: fact_id})
        self.assertNotIn(self.p._key("memory", new), self.written())
        self.assertIn("55432", self.pending()[0])
        self.assertNotIn("55433", self.pending()[0])

        # The memory entry still says old, so a later remove reaches the fact through the kept mapping.
        self.p.on_memory_write("remove", "memory", "", {"previous_content": old})
        self.assertEqual(self.pending(), [])
        self.assertEqual(self.written(), {})

    def test_failed_remove_keeps_the_mapping(self):
        old = "Проект demo: база PostgreSQL 18 слушает порт 55432."
        self.p.on_memory_write("add", "memory", old)
        fact_id = self.pending()[0].split()[0]
        old_key = self.p._key("memory", old)

        real_run = self.p._run
        self.p._run = lambda args, **kwargs: (False, "memcue timed out after 15s")
        try:
            self.p.on_memory_write("remove", "memory", "", {"previous_content": old})
        finally:
            self.p._run = real_run

        self.assertEqual(self.written(), {old_key: fact_id})
        self.assertEqual(len(self.pending()), 1)  # the fact is still there to be retired later

    def add_fact(self, content: str, expires: str = "") -> None:
        """A fact written by another program, not by the built-in memory tool."""
        cmd = [str(BIN), "add", "-origin", "test"]
        if expires:
            cmd += ["-expires", expires]
        subprocess.run(cmd + ["--", content], check=True, capture_output=True,
                       env=dict(os.environ, MEMCUE_FACTS=self.p._facts))

    @unittest.skipUnless(ONCE_PER_SESSION, "the memcue binary does not keep a fact from coming twice in a session")
    def test_a_fact_comes_once_per_session(self):
        self.add_fact("Проект demo: база PostgreSQL 18 слушает порт 55432, пользователь demo_app.")
        query = "на каком порту слушает база postgres проекта demo?"
        self.assertIn("55432", self.p.prefetch(query, session_id="s1"))
        self.assertEqual(self.p.prefetch(query, session_id="s1"), "")  # the same fact once per session
        self.assertIn("55432", self.p.prefetch(query, session_id="s2"))

    @unittest.skipUnless(ONCE_PER_SESSION, "the memcue binary does not keep a fact from coming twice in a session")
    def test_a_compressed_session_gets_the_fact_again(self):
        self.add_fact("Проект demo: база PostgreSQL 18 слушает порт 55432, пользователь demo_app.")
        query = "на каком порту слушает база postgres проекта demo?"
        self.assertIn("55432", self.p.prefetch(query, session_id="s1"))
        self.p.on_pre_compress([])  # the context loses what it got
        self.assertIn("55432", self.p.prefetch(query, session_id="s1"))

    def test_session_switch_rebinds_and_resets_state(self):
        self.p._generation = 3
        self.p._pending["s2"] = ([{"role": "user", "content": "x"}], 0.0)
        self.p._delivered["s2"] = [{"tokens": ["x"], "chars": 1}]
        self.p._inflight["s2"] = Counter({"fp": 1})
        self.p._warned["s2"] = 1.0
        self.p.on_session_switch("s2", reset=True)
        self.assertEqual(self.p._session_id, "s2")
        self.assertEqual(self.p._generation, 0)
        self.assertNotIn("s2", self.p._pending)
        self.assertNotIn("s2", self.p._inflight)
        self.assertNotIn("s2", self.p._delivered)
        self.assertNotIn("s2", self.p._warned)

    def test_rewound_session_forgets_seen_fingerprints(self):
        path = self.p._seen_path("s1")
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps(["old-fingerprint"]), encoding="utf-8")
        self.p._pending["s1"] = ([{"role": "user", "content": "x"}], 0.0)
        self.p.on_session_switch("s1", rewound=True)
        self.assertEqual(self.p._seen("s1"), [])
        self.assertNotIn("s1", self.p._pending)
        self.assertGreaterEqual(self.p._generation, 1)

    def test_a_cron_turn_gets_no_facts(self):
        self.add_fact("Проект demo: база PostgreSQL 18 слушает порт 55432.")
        p = memcue_provider.MemcueMemoryProvider({"binary": BIN})
        p.initialize("s3", hermes_home=self.home, agent_context="cron")
        self.assertEqual(p.prefetch("на каком порту слушает база postgres проекта demo?"), "")

    @unittest.skipUnless(ONCE_PER_SESSION, "the memcue binary does not keep a fact from coming twice in a session")
    def test_a_session_id_outside_the_accepted_set_is_hashed(self):
        self.add_fact("Проект demo: база PostgreSQL 18 слушает порт 55432, пользователь demo_app.")
        query = "на каком порту слушает база postgres проекта demo?"
        self.assertIn("55432", self.p.prefetch(query, session_id="matrix:room/1"))
        self.assertEqual(self.p.prefetch(query, session_id="matrix:room/1"), "")

    def test_replace_of_unknown_entry_adds(self):
        self.p.on_memory_write("replace", "user", "Пользователь пишет по-русски.", {"previous_content": "x"})
        self.assertEqual(len(self.pending()), 1)

    def test_content_starting_with_dash_is_stored(self):
        self.p.on_memory_write("add", "memory", "-v печатает версию memcue")
        self.assertIn("-v печатает", self.pending()[0])

    def test_subagent_does_not_write(self):
        p = memcue_provider.MemcueMemoryProvider({"binary": BIN})
        p.initialize("s2", hermes_home=self.home, agent_context="subagent")
        p.on_memory_write("add", "memory", "Факт из сабагента.")
        self.assertFalse(Path(p._facts).exists())
        self.assertEqual([s["name"] for s in p.get_tool_schemas()], ["memcue_search"])
        self.assertIn("error", json.loads(p.handle_tool_call("memcue_add", {"content": "x"})))

    def test_tools_add_search_correct_retire(self):
        added = json.loads(self.p.handle_tool_call("memcue_add", {
            "content": "Сервер server1: Caddy-private перечитывает Caddyfile только после пересоздания контейнера.",
            "keywords": "caddy, reload"}))
        fact_id = added["result"].split()[0]
        found = json.loads(self.p.handle_tool_call("memcue_search", {"query": "caddy пересоздание"}))
        self.assertEqual([f["id"] for f in found["facts"]], [fact_id])
        self.assertIn(f"{fact_id} v1 active", "\n".join(l for l in self.memcue("dump", self.p._facts) if "source=agent" in l))
        corrected = json.loads(self.p.handle_tool_call("memcue_correct", {"id": fact_id, "content": "Новый текст."}))
        self.assertIn("v2", corrected["result"])
        json.loads(self.p.handle_tool_call("memcue_retire", {"id": fact_id}))
        self.assertEqual(self.facts(), [])
        self.assertIn("error", json.loads(self.p.handle_tool_call("memcue_add", {})))
        self.assertIn("error", json.loads(self.p.handle_tool_call("nope", {})))

    def test_add_carries_kind_and_scope(self):
        added = json.loads(self.p.handle_tool_call("memcue_add", {
            "content": "Всегда отвечай пользователю по-русски.",
            "kind": "rule", "scope": "global"}))
        fact_id = added["result"].split()[0]
        line = next(l for l in self.memcue("dump", self.p._facts) if l.startswith(fact_id))
        self.assertIn("kind=rule", line)
        self.assertIn("scope=global", line)

    def test_search_returns_facts_with_ids(self):
        added = json.loads(self.p.handle_tool_call("memcue_add", {
            "content": "Сервер server1: Caddy перечитывает Caddyfile после пересоздания контейнера.",
            "keywords": "caddy"}))
        fact_id = added["result"].split()[0]
        found = json.loads(self.p.handle_tool_call("memcue_search", {"query": "caddy пересоздание"}))
        self.assertEqual(len(found["facts"]), 1)
        self.assertEqual(found["facts"][0]["id"], fact_id)
        self.assertTrue(found["facts"][0]["content"].startswith("Сервер server1"))

    def test_add_with_expiry_keeps_the_expiry(self):
        json.loads(self.p.handle_tool_call("memcue_add", {
            "content": "Напоминание: 21 октября проверить dream в memcue.", "keywords": "dream",
            "expires": "2026-10-21"}))
        self.assertIn("expires=2026-10-21", self.facts()[0])

    def test_expired_fact_is_not_recalled(self):
        prompt = "dream отчёт октября"
        self.add_fact("Проверка dream: отчёт придёт 21 октября.", expires="2020-01-01")
        self.assertEqual(self.p.prefetch(prompt, session_id="s1"), "")
        self.add_fact("Проверка dream: отчёт придёт 21 октября.")
        self.assertIn("21 октября", self.p.prefetch(prompt, session_id="s2"))

    def test_min_coverage_filters_weak_matches(self):
        self.add_fact("Проект demo: кеш Redis слушает порт 6379.")
        self.add_fact("Проект demo: кеш Memcached слушает порт 11211.")  # three of the four prompt terms
        query = "demo redis порт кеш"
        self.assertEqual(len(self.p.prefetch(query, session_id="s1").splitlines()), 3)  # header + two facts
        strict = memcue_provider.MemcueMemoryProvider({"binary": BIN, "min_coverage": 0.99})
        strict.initialize("s9", hermes_home=self.home, agent_context="primary")
        self.assertEqual(len(strict.prefetch(query, session_id="s9").splitlines()), 2)  # only the full match

    def test_usage_log_tells_used_facts_from_ignored_ones(self):
        os.environ["MEMCUE_USAGE_LOG"] = "1"
        self.addCleanup(os.environ.pop, "MEMCUE_USAGE_LOG", None)
        self.add_fact("Проект demo: база PostgreSQL 18 слушает порт 55432.")
        self.assertIn("55432", self.p.prefetch("на каком порту слушает база postgres проекта demo?", session_id="s1"))
        self.p.sync_turn("на каком порту слушает база postgres проекта demo?", "База слушает 55432.",
                         session_id="s1")
        self.add_fact("Проект demo: очередь RabbitMQ на порту 5672.")
        self.assertIn("5672", self.p.prefetch("какой порт у очереди RabbitMQ проекта demo?", session_id="s2"))
        self.p.sync_turn("какой порт у очереди RabbitMQ проекта demo?", "Не знаю.", session_id="s2")
        log = Path(self.home) / "state" / "memcue" / "usage.log"
        lines = [json.loads(line) for line in log.read_text(encoding="utf-8").splitlines() if line.strip()]
        self.assertEqual([(r["facts"], r["used"]) for r in lines], [(1, 1), (1, 0)])

    def test_usage_log_from_config_needs_no_environment(self):
        self.assertNotIn("MEMCUE_USAGE_LOG", os.environ)
        state = Path(self.home) / "own-state"
        p = memcue_provider.MemcueMemoryProvider({"binary": BIN, "usage_log": True, "state_dir": str(state)})
        p.initialize("s7", hermes_home=self.home, agent_context="primary")
        self.add_fact("Проект demo: база PostgreSQL 18 слушает порт 55432.")
        self.assertIn("55432", p.prefetch("на каком порту слушает база postgres проекта demo?", session_id="s7"))
        p.sync_turn("на каком порту слушает база postgres проекта demo?", "База слушает 55432.", session_id="s7")
        log = state / "memcue" / "usage.log"
        lines = log.read_text(encoding="utf-8").splitlines()
        self.assertEqual([(r["facts"], r["used"]) for r in map(json.loads, lines)], [(1, 1)])
        self.assertEqual(stat.S_IMODE(log.stat().st_mode), 0o600)
        self.assertEqual(stat.S_IMODE(log.parent.stat().st_mode), 0o700)
        self.assertFalse((Path(self.home) / "state" / "memcue" / "usage.log").exists())

    def test_usage_log_config_false_stays_off(self):
        state = Path(self.home) / "own-state"
        p = memcue_provider.MemcueMemoryProvider({"binary": BIN, "usage_log": "false", "state_dir": str(state)})
        p.initialize("s8", hermes_home=self.home, agent_context="primary")
        self.add_fact("Проект demo: база PostgreSQL 18 слушает порт 55432.")
        p.prefetch("на каком порту слушает база postgres проекта demo?", session_id="s8")
        p.sync_turn("…", "55432", session_id="s8")
        self.assertFalse((state / "memcue" / "usage.log").exists())

    def test_usage_log_is_off_without_the_flag(self):
        self.add_fact("Проект demo: база PostgreSQL 18 слушает порт 55432.")
        self.p.prefetch("на каком порту слушает база postgres проекта demo?", session_id="s1")
        self.p.sync_turn("…", "55432", session_id="s1")
        self.assertFalse((Path(self.home) / "state" / "memcue" / "usage.log").exists())

    def test_session_end_extracts_through_the_endpoint(self):
        answer = json.dumps({"facts": [{"content": "Hermes на server1: модель по умолчанию deepseek-flash.",
                                        "keywords": ["модель", "model"], "kind": "environment",
                                        "scope": "host:server1", "source": "user",
                                        "evidence": "на server1 у hermes модель по умолчанию deepseek-flash"}],
                          "dropped": ""}, ensure_ascii=False)
        seen = {}

        class Handler(http.server.BaseHTTPRequestHandler):
            def do_POST(self):
                body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
                seen.update(model=body["model"], auth=self.headers.get("Authorization"),
                            text=body["messages"][1]["content"])
                out = json.dumps({"choices": [{"message": {"content": answer}, "finish_reason": "stop"}],
                                  "usage": {"prompt_tokens": 10, "completion_tokens": 5}}).encode()
                self.send_response(200)
                self.send_header("Content-Length", str(len(out)))
                self.end_headers()
                self.wfile.write(out)

            def log_message(self, *a):
                pass

        srv = http.server.HTTPServer(("127.0.0.1", 0), Handler)
        threading.Thread(target=srv.serve_forever, daemon=True).start()
        self.addCleanup(srv.server_close)
        self.addCleanup(srv.shutdown)
        os.environ["TEST_MEMCUE_KEY"] = "k1"
        p = memcue_provider.MemcueMemoryProvider({"binary": BIN, "extract_url": f"http://127.0.0.1:{srv.server_port}",
                                                  "extract_model": "m1", "extract_key_env": "TEST_MEMCUE_KEY"})
        workspace = Path(self.home) / "demo"
        workspace.mkdir()
        (workspace / ".git").mkdir()
        p.initialize("sess-9", hermes_home=self.home, agent_workspace=str(workspace), agent_context="primary")
        p.on_session_end([{"role": "system", "content": "prompt"},
                          {"role": "user",
                           "content": "на server1 у hermes модель по умолчанию deepseek-flash. " * 60},
                          {"role": "assistant", "content": "запомнил"}])
        for _ in range(100):
            if Path(p._facts).exists() and self.facts_of(p):
                break
            time.sleep(0.1)
        facts = self.facts_of(p)
        self.assertEqual(len(facts), 1)
        self.assertIn("deepseek-flash", facts[0])
        self.assertIn(" demo ", facts[0])
        self.assertEqual((seen["model"], seen["auth"]), ("m1", "Bearer k1"))
        self.assertNotIn("SYSTEM", seen["text"])

    def test_final_flushes_pass_min_chars(self):
        """session_end, pre_compress and shutdown are the last chance for a tail; threshold and idle wait."""
        for event, final in (("session_end", True), ("pre_compress", True), ("shutdown", True),
                             ("threshold", False), ("idle", False)):
            with self.subTest(event=event):
                calls = Path(self.home) / f"args-{event}"
                script = Path(self.home) / f"bin-{event}"
                script.write_text(f"#!/bin/sh\nprintf '%s\\n' \"$@\" >> {shlex.quote(str(calls))}\n"
                                  "cat >/dev/null\nexit 0\n", encoding="utf-8")
                script.chmod(0o755)
                p = memcue_provider.MemcueMemoryProvider({"binary": str(script), "extract_url": "http://x",
                                                           "extract_model": "m"})
                p.initialize("s1", hermes_home=self.home, agent_context="primary")
                done = threading.Event()
                p._extract_in_background([{"role": "user", "content": "x"}], event, "s1",
                                         on_done=lambda ok: done.set())
                self.assertTrue(done.wait(5), event)
                args = calls.read_text(encoding="utf-8").splitlines()
                if final:
                    self.assertIn("-min-chars", args, event)
                    self.assertEqual(args[args.index("-min-chars") + 1], "1", event)
                else:
                    self.assertNotIn("-min-chars", args, event)

    def test_no_endpoint_no_extraction(self):
        self.p.on_session_end([{"role": "user", "content": "x " * 3000}])
        self.assertFalse(Path(self.p._facts).exists())

    def facts_of(self, p):
        """The facts an extraction wrote: candidates for review."""
        out = subprocess.run([BIN, "candidates"], env=dict(os.environ, MEMCUE_FACTS=p._facts),
                             capture_output=True, text=True).stdout
        return [line for line in out.splitlines() if line]

    def test_host_config_is_forwarded_to_memcue(self):
        script = Path(self.home) / "host.sh"
        script.write_text("#!/bin/sh\nprintf '%s' \"$MEMCUE_HOST\"\n", encoding="utf-8")
        script.chmod(0o755)
        p = memcue_provider.MemcueMemoryProvider({"binary": str(script), "host": "mac"})
        p.initialize("s1", hermes_home=self.home, agent_context="primary")
        ok, out = p._run([])
        self.assertTrue(ok)
        self.assertEqual(out, "mac")
        p = memcue_provider.MemcueMemoryProvider({"binary": str(script), "host": "mac", "extract_url": "http://x",
                                                   "extract_model": "m"})
        p.initialize("s1", hermes_home=self.home, agent_context="primary")
        self.assertEqual(p._extract_env()["MEMCUE_HOST"], "mac")

    def test_missing_binary_is_unavailable(self):
        p = memcue_provider.MemcueMemoryProvider({"binary": "/nonexistent/memcue"})
        self.assertFalse(p.is_available())


class AccumulationTest(unittest.TestCase):
    """Turn-based extraction; the model call is replaced, so no binary is needed."""

    def setUp(self):
        self.home = tempfile.mkdtemp()
        self.p = memcue_provider.MemcueMemoryProvider({"binary": "/bin/true", "extract_url": "http://x",
                                                       "extract_model": "m", "extract_threshold": 1000,
                                                       "extract_idle_minutes": 0})
        self.p.initialize("s1", hermes_home=self.home, agent_context="primary")
        self.p._tick = 0.05
        self.calls = []
        self.p._extract_in_background = self.succeed
        self.msgs = [{"role": "system", "content": "prompt"}]

    def succeed(self, msgs, event, session="", on_done=None):
        self.calls.append((event, session, [m["content"] for m in msgs]))
        if on_done:
            on_done(True)

    def tearDown(self):
        self.p._stop.set()
        shutil.rmtree(self.home, ignore_errors=True)

    def turn(self, user, answer="ok"):
        self.msgs += [{"role": "user", "content": user}, {"role": "assistant", "content": answer}]
        self.p.sync_turn(user, answer, session_id="s1", messages=list(self.msgs))

    def test_threshold_sends_only_new_messages_once(self):
        self.p._idle = 3600
        self.turn("a" * 300)
        self.assertEqual(self.calls, [])
        self.turn("b" * 800)
        self.assertEqual(len(self.calls), 1)
        self.assertEqual(self.calls[0][0:2], ("threshold", "s1"))
        self.assertEqual(self.calls[0][2], ["a" * 300, "ok", "b" * 800, "ok"])
        self.turn("c" * 1200)
        self.assertEqual(self.calls[1][2], ["c" * 1200, "ok"])

    def test_compressed_list_sends_nothing_twice(self):
        self.p._idle = 3600
        self.turn("a" * 1200)
        # Compression replaces the head with a summary and keeps the last turn; only the new turn is new.
        self.msgs = [{"role": "user", "content": "summary of a"}, *self.msgs[-2:]]
        self.turn("d" * 1200)
        self.assertEqual([c[2] for c in self.calls], [["a" * 1200, "ok"], ["summary of a", "d" * 1200, "ok"]])

    def test_pause_sends_a_smaller_batch(self):
        self.turn("e" * 2100)  # under the threshold of the run below
        self.p._threshold = 10000
        self.calls.clear()
        self.turn("f" * 2100)
        for _ in range(100):
            if self.calls:
                break
            time.sleep(0.02)
        self.assertEqual(self.calls[-1][0], "idle")
        self.assertIn("f" * 2100, self.calls[-1][2])

    def test_session_end_and_shutdown_send_the_rest(self):
        self.p._idle = 3600
        self.turn("g" * 100)
        self.p.on_session_end(list(self.msgs))
        self.assertEqual(self.calls, [("session_end", "s1", ["g" * 100, "ok"])])
        self.turn("h" * 100)
        self.p.shutdown()
        self.assertEqual(self.calls[-1], ("shutdown", "s1", ["h" * 100, "ok"]))

    def test_shutdown_waits_for_inflight_extraction(self):
        done = threading.Event()

        def slow():
            time.sleep(0.2)
            done.set()

        feed = threading.Thread(target=slow, daemon=True)
        with self.p._feeds_lock:
            self.p._feeds.append(feed)
        feed.start()
        started = time.monotonic()
        self.p.shutdown()
        self.assertTrue(done.is_set())
        self.assertGreaterEqual(time.monotonic() - started, 0.15)

    def test_session_end_after_switch_flushes_new_session(self):
        self.p._idle = 3600
        self.turn("a" * 1200)
        self.p.on_session_end(list(self.msgs))
        self.assertEqual([call[1] for call in self.calls], ["s1"])
        self.p.on_session_switch("s2", reset=True)
        self.msgs = [{"role": "user", "content": "b" * 1200}]
        self.p.sync_turn("b" * 1200, "ok", session_id="s2", messages=list(self.msgs))
        self.p.on_session_end(list(self.msgs))
        self.assertEqual([call[1] for call in self.calls], ["s1", "s2"])

    def test_seen_survives_a_restart(self):
        self.p._idle = 3600
        self.turn("i" * 1200)
        p2 = memcue_provider.MemcueMemoryProvider(dict(self.p._config))
        p2.initialize("s1", hermes_home=self.home, agent_context="primary")
        sent = []

        def capture(msgs, event, session="", on_done=None):
            sent.append([m["content"] for m in msgs])
            if on_done:
                on_done(True)

        p2._extract_in_background = capture
        p2.on_session_end(list(self.msgs))
        self.assertEqual(sent, [])
        p2._stop.set()

    def test_tool_output_is_cut_and_extracted(self):
        """A tool-sourced fact may quote only tool output, so the extraction has to see it — but cut short."""
        self.p._idle = 3600
        self.p._threshold = 10
        self.msgs += [{"role": "user", "content": "a" * 600}, {"role": "tool", "content": "x" * 60000}]
        self.p.sync_turn("a" * 600, "ok", session_id="s1", messages=list(self.msgs))
        tool_texts = [t for call in self.calls for t in call[2] if t.startswith("x")]
        self.assertTrue(tool_texts, self.calls)
        self.assertTrue(all(len(t) <= self.p._tool_result_chars + 20 for t in tool_texts), tool_texts)

    def test_tool_output_can_be_turned_off(self):
        p = memcue_provider.MemcueMemoryProvider({"binary": BIN, "extract_tool_result_chars": 0})
        p.initialize("s1", hermes_home=self.home, agent_context="primary")
        self.assertFalse(p._keep({"role": "tool", "content": "x" * 100}))
        self.assertTrue(p._keep({"role": "user", "content": "x"}))

    def test_long_messages_are_cut_before_extraction(self):
        self.p._idle = 3600
        self.p._max_message = 50
        self.p._threshold = 10
        self.turn("y" * 400)
        self.assertEqual(len(self.calls), 1)
        self.assertTrue(all(len(text) <= 70 for text in self.calls[0][2]), self.calls[0][2])

    def test_failed_extraction_is_retried_not_marked_seen(self):
        self.p._idle = 3600
        attempts = []

        def fail(msgs, event, session="", on_done=None):
            attempts.append([m["content"] for m in msgs])
            if on_done:
                on_done(False)

        self.p._extract_in_background = fail
        self.turn("a" * 1200)
        self.assertEqual(len(attempts), 1)
        self.assertEqual(self.p._seen("s1"), [])  # the failure must not acknowledge anything

        # The next successful run takes the same messages from the pending batch and only then marks them seen.
        self.p._extract_in_background = self.succeed
        self.p._flush("s1", "idle")
        self.assertEqual(len(attempts), 1)
        self.assertEqual(len(self.calls), 1)
        self.assertIn("a" * 1200, self.calls[0][2])
        seen = self.p._seen("s1")
        self.assertIn(memcue_provider._fingerprint({"role": "user", "content": "a" * 1200}), seen)

    def test_pending_batch_survives_a_restart(self):
        """A crash between the launch and the acknowledgement leaves the batch on disk; the next process retries it."""
        self.p._idle = 3600
        captured = []

        def crash(msgs, event, session="", on_done=None):
            captured.append([m["content"] for m in msgs])  # the process dies before _extracted runs

        self.p._extract_in_background = crash
        self.turn("z" * 1200)
        self.assertEqual(len(captured), 1)
        pending = list((Path(self.p._state) / "pending").glob("*.json"))
        self.assertEqual(len(pending), 1)
        saved = json.loads(pending[0].read_text(encoding="utf-8"))
        self.assertEqual([m["content"] for m in saved], ["z" * 1200, "ok"])

        p2 = memcue_provider.MemcueMemoryProvider(dict(self.p._config))
        p2.initialize("s1", hermes_home=self.home, agent_context="primary")
        self.assertIn("s1", p2._pending)
        sent = []

        def succeed(msgs, event, session="", on_done=None):
            sent.append([m["content"] for m in msgs])
            if on_done:
                on_done(True)

        p2._extract_in_background = succeed
        p2.on_session_end([])
        self.assertEqual([text for call in sent for text in call], ["z" * 1200, "ok"])
        self.assertEqual(list((Path(self.p._state) / "pending").glob("*.json")), [])
        p2._stop.set()

    def test_subagent_gathers_nothing(self):
        p = memcue_provider.MemcueMemoryProvider(dict(self.p._config))
        p.initialize("s2", hermes_home=self.home, agent_context="subagent")
        p.sync_turn("x" * 5000, "ok", session_id="s2", messages=[{"role": "user", "content": "x" * 5000}])
        self.assertEqual(p._pending, {})


if __name__ == "__main__":
    unittest.main()
