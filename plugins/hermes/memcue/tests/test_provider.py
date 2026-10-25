"""Offline tests of the memcue provider against the real memcue binary.

Run: python3 plugins/hermes/memcue/tests/test_provider.py (MEMCUE_BIN=/path/to/memcue, default: memcue on PATH).
Inside Hermes the real agent modules are used; elsewhere they are stubbed.
"""

import datetime
import hashlib
import http.server
import importlib.util
import inspect
import json
import re
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


def _shell_memcue_vars() -> list:
    """The memcue settings of the developer's shell. Any of them can
    change what the hook serves, so the suite runs without any of them (MEMCUE_BIN only names the binary)."""
    return [k for k in os.environ if k.startswith("MEMCUE_") and k != "MEMCUE_BIN"]


@unittest.skipUnless(BIN, "memcue binary not found (set MEMCUE_BIN)")
class ProviderTest(unittest.TestCase):
    def setUp(self):
        self.home = tempfile.mkdtemp()
        os.environ["XDG_STATE_HOME"] = str(Path(self.home) / "state")  # memcue's extract.log
        # The suite must never touch a live base: an inherited MEMCUE_FACTS wins over HERMES_HOME and let an earlier
        # run write test facts into a real base and migrate it to another schema. MEMCUE_USAGE_LOG from the container
        # would turn the usage log on behind the tests that assert it is off, and a recall setting changes what is served.
        self._kept_env = {k: os.environ.pop(k, None) for k in _shell_memcue_vars()}
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

    def test_a_new_base_drops_the_old_checkpoint_and_mirror(self):
        first = memcue_provider.MemcueMemoryProvider({"binary": BIN, "facts": str(Path(self.home) / "a.db")})
        first.initialize("s1", hermes_home=self.home)
        first.on_memory_write("add", "memory", "Факт первой базы.")
        self.assertTrue(first._written())
        Path(first._state, "sessions").mkdir(parents=True, exist_ok=True)
        Path(first._state, "sessions", "seed.json").write_text("[]", encoding="utf-8")

        second = memcue_provider.MemcueMemoryProvider({"binary": BIN, "facts": str(Path(self.home) / "b.db")})
        second.initialize("s1", hermes_home=self.home)
        self.assertEqual(second._written(), {})
        self.assertFalse(Path(second._state, "sessions").exists())
        self.assertFalse(Path(second._state, "pending").exists())
        self.assertEqual(Path(second._state, "base").read_text(encoding="utf-8").strip(),
                         str(Path(self.home) / "b.db"))

    def test_facts_path_expands_home(self):
        p = memcue_provider.MemcueMemoryProvider({"binary": BIN, "facts": "~/memcue/facts.db"})
        self.assertEqual(p._facts, str(Path(os.path.expanduser("~")) / "memcue" / "facts.db"))
        self.assertTrue(p.is_available())

    def test_relative_facts_path_is_unavailable(self):
        p = memcue_provider.MemcueMemoryProvider({"binary": BIN, "facts": "relative/facts.db"})
        self.assertFalse(p.is_available())
        self.assertIn("absolute", p.unavailable_reason())

    def test_an_older_binary_turns_the_card_and_extraction_off(self):
        stub = Path(self.home, "old-memcue")
        stub.write_text("#!/bin/sh\ncase \"$1\" in\n  version) printf '{\"version\":\"0.3.0\"}\\n' ;;\n"
                        "  hook) printf '{}\\n' ;;\nesac\nexit 0\n", encoding="utf-8")
        stub.chmod(0o755)
        p = memcue_provider.MemcueMemoryProvider({"binary": str(stub), "extract_url": "http://127.0.0.1:9/v1",
                                                  "extract_model": "m"})
        p.initialize("s1", hermes_home=self.home)
        self.assertFalse(p._binary_ok())
        self.assertEqual(p._card_text(), "")
        self.assertIsNone(p._extract_env())

    def test_the_current_binary_is_compatible(self):
        self.assertTrue(self.p._binary_ok())

    def test_a_failed_session_card_leaves_the_note_and_is_asked_again(self):
        counter = Path(self.home, "card-count")
        stub = Path(self.home, "card-fail-memcue")
        stub.write_text("#!/bin/sh\n"
                        "case \"$1\" in\n"
                        "  version) printf '{\"version\":\"0.4.0\"}\\n' ;;\n"
                        f"  session-card) echo x >> {counter}; echo locked >&2; exit 1 ;;\n"
                        "esac\nexit 0\n", encoding="utf-8")
        stub.chmod(0o755)
        base = str(Path(self.home) / "card.db")
        subprocess.run([BIN, "add", "-origin", "x 2026-10-19", "Правило карты."],
                       env=dict(os.environ, MEMCUE_FACTS=base), check=True, capture_output=True)
        p = memcue_provider.MemcueMemoryProvider({"binary": str(stub), "facts": base})
        p.initialize("s1", hermes_home=self.home)
        block = p.system_prompt_block()
        self.assertIn("memcue_search", block)  # the note stays without the card
        self.assertNotIn("memcue: rules and decisions", block)
        self.assertEqual(counter.read_text(encoding="utf-8").count("x"), 1)
        # Hermes asks again only when it rebuilds the system prompt; that rebuild asks for the card again.
        p.system_prompt_block()
        self.assertEqual(counter.read_text(encoding="utf-8").count("x"), 2)

    def test_the_environment_off_switch_wins_over_the_endpoint(self):
        p = memcue_provider.MemcueMemoryProvider({"binary": BIN, "extract_url": "http://127.0.0.1:9/v1",
                                                  "extract_model": "m"})
        p.initialize("s1", hermes_home=self.home)
        previous = os.environ.get("MEMCUE_EXTRACT")
        os.environ["MEMCUE_EXTRACT"] = "0"
        try:
            self.assertIsNone(p._extract_env())
        finally:
            if previous is None:
                os.environ.pop("MEMCUE_EXTRACT", None)
            else:
                os.environ["MEMCUE_EXTRACT"] = previous

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

    def test_mirrored_fact_waits_for_review(self):
        """A built-in memory note is mirrored as a candidate with its own origin, not as an approved fact."""
        self.p.on_memory_write("add", "memory", "Проект demo: база PostgreSQL 18 слушает порт 55432.")
        self.assertIn("hermes-memory", self.pending()[0])
        self.assertEqual(self.facts(), [])

    def test_no_fact_comes_with_a_message(self):
        """The model searches itself (#361): a matching approved fact stays out of the prefetch."""
        self.add_fact("Проект demo: база PostgreSQL 18 слушает порт 55432, пользователь demo_app.")
        self.assertNotIn("55432", self.p.prefetch("на каком порту слушает база postgres проекта demo?",
                                                  session_id="s1"))
        found = json.loads(self.p.handle_tool_call("memcue_search", {"query": "порт postgres demo"}))
        self.assertIn("55432", found["facts"][0]["content"])

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

    def search(self, query: str) -> str:
        """What memcue_search returns to the model for query."""
        return self.p.handle_tool_call("memcue_search", {"query": query})

    def add_fact(self, content: str, expires: str = "") -> None:
        """A fact written by another program, not by the built-in memory tool."""
        cmd = [str(BIN), "add", "-origin", "test"]
        if expires:
            cmd += ["-expires", expires]
        subprocess.run(cmd + ["--", content], check=True, capture_output=True,
                       env=dict(os.environ, MEMCUE_FACTS=self.p._facts))

    def test_session_card_is_in_the_system_prompt_not_in_the_prefetch(self):
        self.memcue("add", "-kind", "rule", "-scope", "global", "-source", "user", "--",
                    "Всегда отвечай пользователю по-русски.")
        block = self.p.system_prompt_block()
        self.assertIn("memcue_search", block)
        self.assertIn("memcue: rules and decisions", block)
        self.assertIn("Всегда отвечай", block)
        self.assertEqual(self.p.prefetch("любой вопрос", session_id="s1"), "")
        # A rebuild after a compression carries the card again, as a view of the base.
        self.p.on_pre_compress([])
        self.assertIn("Всегда отвечай", self.p.system_prompt_block())

    def test_a_cron_run_gets_no_card(self):
        self.memcue("add", "-kind", "rule", "-scope", "global", "-source", "user", "--",
                    "Всегда отвечай пользователю по-русски.")
        p = memcue_provider.MemcueMemoryProvider({"binary": BIN})
        p.initialize("s-cron", hermes_home=self.home, agent_context="cron")
        self.assertNotIn("Всегда отвечай", p.system_prompt_block())

    def test_session_card_can_be_turned_off(self):
        p = memcue_provider.MemcueMemoryProvider({"binary": BIN, "session_card": False})
        p.initialize("s-card-off", hermes_home=self.home, agent_context="primary")
        self.memcue("add", "-kind", "rule", "-scope", "global", "-source", "user", "--",
                    "Всегда отвечай пользователю по-русски.")
        self.assertNotIn("Всегда отвечай", p.system_prompt_block())

    def test_session_switch_rebinds_and_resets_state(self):
        self.p._generation = 3
        self.p._pending["s2"] = ([{"role": "user", "content": "x"}], 0.0)
        self.p._delivered["s2"] = [{"tokens": ["x"], "chars": 1}]
        self.p._inflight["s2"] = Counter({"fp": 1})
        self.p.on_session_switch("s2", reset=True)
        self.assertEqual(self.p._session_id, "s2")
        self.assertEqual(self.p._generation, 0)
        self.assertNotIn("s2", self.p._pending)
        self.assertNotIn("s2", self.p._inflight)
        self.assertNotIn("s2", self.p._delivered)

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

    def test_a_session_id_outside_the_accepted_set_is_hashed(self):
        scope = self.p._scope("matrix:room/1")
        self.assertRegex(scope, r"^[0-9a-f]{32}$")
        self.assertEqual(scope, self.p._scope("matrix:room/1"))

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

    def test_cron_run_dreams_but_writes_no_facts(self):
        p = memcue_provider.MemcueMemoryProvider({"binary": BIN})
        p.initialize("c1", hermes_home=self.home, agent_context="cron")
        self.assertEqual([s["name"] for s in p.get_tool_schemas()],
                         ["memcue_search", "memcue_dream_prepare", "memcue_dream_apply"])
        self.assertIn("error", json.loads(p.handle_tool_call("memcue_add", {"content": "x"})))
        self.assertIn("result", json.loads(p.handle_tool_call("memcue_dream_prepare", {"since": "all"})))

    def test_dream_tools_prepare_and_apply(self):
        ids = []
        for content in ("Workflow nightly-sync.yml в example/nightly-sync каждую ночь подписывает bot на все репозитории.",
                        "Репозиторий example/nightly-sync каждую ночь запускает nightly-sync.yml, он подписывает bot на все репозитории."):
            added = json.loads(self.p.handle_tool_call("memcue_add", {"content": content, "keywords": "nightly-sync, workflow"}))
            ids.append(added["result"].split()[0])
        packet = json.loads(self.p.handle_tool_call("memcue_dream_prepare", {"since": "all"}))["result"]
        self.assertIn("memcue dream -apply", packet)
        packet_id = re.search(r'"packet": "([^"]+)"', packet).group(1)
        keep, gone = sorted(ids)
        answer = json.dumps({"packet": packet_id, "actions": [
            {"op": "retire", "id": gone, "by": keep, "kind": "duplicate", "reason": "the same workflow"}]})
        applied = json.loads(self.p.handle_tool_call("memcue_dream_apply", {"answer": answer}))
        self.assertIn(f"retire {gone}", applied["result"])
        self.assertEqual([l.split()[0] for l in self.facts() if l.strip()], [keep])
        self.assertIn("error", json.loads(self.p.handle_tool_call("memcue_dream_apply", {"answer": answer})))
        self.assertIn("error", json.loads(self.p.handle_tool_call("memcue_dream_apply", {})))

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

    def test_search_takes_a_limit(self):
        for i in range(3):
            json.loads(self.p.handle_tool_call("memcue_add", {
                "content": f"Сервер server{i}: Caddy перечитывает Caddyfile после пересоздания контейнера.",
                "keywords": "caddy"}))
        found = json.loads(self.p.handle_tool_call("memcue_search", {"query": "caddy пересоздание", "limit": "2"}))
        self.assertEqual(len(found["facts"]), 2)
        self.assertIn("error", json.loads(self.p.handle_tool_call("memcue_search", {"query": "caddy", "limit": "x"})))

    def test_search_passes_the_controls(self):
        """in, kind, words and expired reach memcue search (#409); a wrong value comes back as an error."""
        rule = json.loads(self.p.handle_tool_call("memcue_add", {
            "content": "Caddy на server1 перечитывает Caddyfile после пересоздания.", "keywords": "caddy",
            "kind": "rule", "scope": "global"}))["result"].split()[0]
        json.loads(self.p.handle_tool_call("memcue_add", {
            "content": "Caddy на server2 падает без конфига.", "keywords": "caddy", "kind": "pitfall"}))
        def ids(**kw):
            found = json.loads(self.p.handle_tool_call("memcue_search", dict(query="caddy server1", **kw)))
            return [f["id"] for f in found["facts"]]
        self.assertEqual(ids(kind="rule", min_score="0"), [rule])
        self.assertEqual(ids(words="all"), [rule])
        self.assertEqual(ids(**{"in": "global"}, min_score="0"), [rule])
        self.assertEqual(len(ids(**{"in": "all"}, expired="true", min_score="0")), 2)
        for bad in ({"kind": "wish"}, {"in": "everywhere"}, {"words": "some"}):
            self.assertIn("error", json.loads(self.p.handle_tool_call("memcue_search", dict(query="caddy", **bad))))

    def test_search_takes_the_whole_base_in_a_repository(self):
        """memcue searches only the workspace's repository by default; a Hermes chat is not about one (#409)."""
        other = self.memcue("add", "-origin", "example.com/them/lib 2026-10-25", "-scope", "repo:example.com/them/lib",
                            "-keywords", "caddy", "Caddy на server3 держит конфиг в /etc/caddy.")[0].split()[0]
        self.p._workspace = str(Path(__file__).resolve().parents[4])  # this repository's checkout
        found = json.loads(self.p.handle_tool_call("memcue_search", {"query": "caddy server3"}))
        self.assertEqual([f["id"] for f in found["facts"]], [other])
        narrow = json.loads(self.p.handle_tool_call("memcue_search", {"query": "caddy server3", "in": "project"}))
        self.assertEqual(narrow["facts"], [])

    def test_search_defaults_to_the_configured_number(self):
        """Five facts by default and a config key overrides it (#372): ten were noise on a live base."""
        for i in range(7):
            json.loads(self.p.handle_tool_call("memcue_add", {
                "content": f"Сервер server{i}: Caddy перечитывает Caddyfile после пересоздания контейнера.",
                "keywords": "caddy"}))
        found = json.loads(self.p.handle_tool_call("memcue_search", {"query": "caddy пересоздание"}))
        self.assertEqual(len(found["facts"]), memcue_provider._SEARCH_FACTS)
        self.assertEqual(memcue_provider._SEARCH_FACTS, 5)
        few = memcue_provider.MemcueMemoryProvider({"binary": BIN, "facts": self.p._facts, "search_facts": "2"})
        found = json.loads(few.handle_tool_call("memcue_search", {"query": "caddy пересоздание"}))
        self.assertEqual(len(found["facts"]), 2)
        self.assertEqual(memcue_provider.MemcueMemoryProvider({"binary": BIN, "search_facts": "many"})._search_facts, 5)
        self.assertEqual(memcue_provider.MemcueMemoryProvider({"binary": BIN, "search_facts": "99"})._search_facts, 50)

    def test_correct_sets_keywords_and_expiry(self):
        added = json.loads(self.p.handle_tool_call("memcue_add", {
            "content": "Сервер server1: Caddy перечитывает Caddyfile после пересоздания контейнера.",
            "keywords": "caddy"}))
        fact_id = added["result"].split()[0]
        until = (datetime.date.today() + datetime.timedelta(days=3)).isoformat()
        corrected = json.loads(self.p.handle_tool_call("memcue_correct", {
            "id": fact_id, "content": "Сервер server1: Caddy перечитывает Caddyfile после пересоздания контейнера, "
                                      "теперь с новым ключом.", "keywords": "caddy, пересоздание", "expires": until}))
        self.assertIn("v2", corrected["result"])
        line = next(l for l in self.memcue("facts") if l.startswith(fact_id))
        self.assertIn(f"expires={until}", line)
        self.assertIn("пересоздание", line)

    def test_add_with_expiry_keeps_the_expiry(self):
        json.loads(self.p.handle_tool_call("memcue_add", {
            "content": "Напоминание: 21 октября проверить dream в memcue.", "keywords": "dream",
            "expires": "2026-10-21"}))
        self.assertIn("expires=2026-10-21", self.facts()[0])

    def test_usage_log_tells_used_facts_from_ignored_ones(self):
        os.environ["MEMCUE_USAGE_LOG"] = "1"
        self.addCleanup(os.environ.pop, "MEMCUE_USAGE_LOG", None)
        self.add_fact("Проект demo: база PostgreSQL 18 слушает порт 55432.")
        self.assertIn("55432", self.search("порт postgres demo"))
        self.p.sync_turn("на каком порту слушает база postgres проекта demo?", "База слушает 55432.",
                         session_id="s1")
        self.add_fact("Проект demo: очередь RabbitMQ на порту 5672.")
        self.assertIn("5672", self.search("порт rabbitmq demo"))
        self.p.sync_turn("какой порт у очереди RabbitMQ проекта demo?", "Не знаю.", session_id="s1")
        log = Path(self.home) / "state" / "memcue" / "usage.log"
        lines = [json.loads(line) for line in log.read_text(encoding="utf-8").splitlines() if line.strip()]
        self.assertEqual([(r["facts"], r["used"]) for r in lines], [(1, 1), (1, 0)])

    def test_usage_log_from_config_needs_no_environment(self):
        self.assertNotIn("MEMCUE_USAGE_LOG", os.environ)
        state = Path(self.home) / "own-state"
        p = memcue_provider.MemcueMemoryProvider({"binary": BIN, "usage_log": True, "state_dir": str(state)})
        p.initialize("s7", hermes_home=self.home, agent_context="primary")
        self.add_fact("Проект demo: база PostgreSQL 18 слушает порт 55432.")
        self.assertIn("55432", p.handle_tool_call("memcue_search", {"query": "порт postgres demo"}))
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
        p.handle_tool_call("memcue_search", {"query": "порт postgres demo"})
        p.sync_turn("…", "55432", session_id="s8")
        self.assertFalse((state / "memcue" / "usage.log").exists())

    def test_searches_keep_nothing_without_the_usage_log(self):
        """A subagent or cron run searches without a sync_turn; nothing may pile up for it."""
        self.add_fact("Проект demo: база PostgreSQL 18 слушает порт 55432.")
        for _ in range(3):
            self.search("порт postgres demo")
        self.assertEqual(self.p._delivered, {})

    def test_usage_log_is_off_without_the_flag(self):
        self.add_fact("Проект demo: база PostgreSQL 18 слушает порт 55432.")
        self.search("порт postgres demo")
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
        # The project is named by its remote, not by the directory (#343).
        (workspace / ".git" / "config").write_text('[remote "origin"]\n\turl = git@example.com:team/demo.git\n')
        # Hermes passes the session's directory as cwd; agent_workspace is the constant "hermes" (#347).
        p.initialize("sess-9", hermes_home=self.home, agent_workspace="hermes", cwd=str(workspace),
                     agent_context="primary")
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
        self.assertIn(" example.com/team/demo ", facts[0])
        self.assertEqual((seen["model"], seen["auth"]), ("m1", "Bearer k1"))
        self.assertNotIn("SYSTEM", seen["text"])

    def test_session_cwd_is_the_project_only_in_git(self):
        """The session's cwd names the project when it lies in a git repository; outside git (Hermes's own
        directories) memcue gets an empty -cwd, so it does not fall back to its own directory (#347). A session
        without a cwd takes the directory Hermes falls back to, TERMINAL_CWD here (#354)."""
        repo = Path(self.home) / "repo"
        (repo / ".git").mkdir(parents=True)
        (repo / "sub").mkdir()
        plain = Path(self.home) / "plain"
        plain.mkdir()
        old = os.environ.get("TERMINAL_CWD")
        self.addCleanup(lambda: os.environ.__setitem__("TERMINAL_CWD", old) if old is not None
                        else os.environ.pop("TERMINAL_CWD", None))
        for cwd, terminal, want in ((str(repo), str(plain), str(repo)), (str(repo / "sub"), "", str(repo / "sub")),
                                    (str(plain), str(repo), ""), ("hermes", "", ""), (None, str(plain), ""),
                                    (None, str(repo), str(repo))):
            os.environ["TERMINAL_CWD"] = terminal
            with self.subTest(cwd=cwd, terminal=terminal):
                calls = Path(self.home) / f"args-{abs(hash((cwd, terminal)))}"
                script = Path(self.home) / f"bin-{abs(hash((cwd, terminal)))}"
                script.write_text(f"#!/bin/sh\nprintf '%s\\n' \"$@\" >> {shlex.quote(str(calls))}\n"
                                  "cat >/dev/null\nexit 0\n", encoding="utf-8")
                script.chmod(0o755)
                p = memcue_provider.MemcueMemoryProvider({"binary": str(script), "extract_url": "http://x",
                                                           "extract_model": "m"})
                kwargs = {"hermes_home": self.home, "agent_workspace": "hermes", "agent_context": "primary"}
                if cwd is not None:
                    kwargs["cwd"] = cwd
                p.initialize("s1", **kwargs)
                done = threading.Event()
                p._extract_in_background([{"role": "user", "content": "x"}], "idle", "s1",
                                         on_done=lambda ok: done.set())
                self.assertTrue(done.wait(5))
                args = calls.read_text(encoding="utf-8").splitlines()
                i = args.index("-cwd")
                self.assertEqual(args[i + 1] if i + 1 < len(args) else "", want)

    def test_session_cwd_follows_hermes_when_it_resolves_one(self):
        """With agent.runtime_cwd present, a session without a cwd takes what Hermes resolves (#354)."""
        repo = Path(self.home) / "resolved"
        (repo / ".git").mkdir(parents=True)
        mod = types.ModuleType("agent.runtime_cwd")
        mod.resolve_agent_cwd = lambda: repo
        sys.modules["agent.runtime_cwd"] = mod
        self.addCleanup(sys.modules.pop, "agent.runtime_cwd", None)
        p = memcue_provider.MemcueMemoryProvider({"binary": BIN})
        p.initialize("s1", hermes_home=self.home, agent_context="primary")
        self.assertEqual(p._workspace, str(repo))

    def test_search_scopes_only_a_binary_that_knows_the_flags(self):
        """-cwd and -session appeared in 0.8.0 (#361); an older binary must get the search it understands (#368)."""
        for version, scoped in (("0.7.0", False), ("0.8.0", True), ("dev", True)):
            with self.subTest(version=version):
                calls = Path(self.home) / f"search-args-{version}"
                stub = Path(self.home) / f"search-memcue-{version}"
                stub.write_text("#!/bin/sh\n"
                                "case \"$1\" in\n"
                                f"  version) printf '{{\"version\":\"{version}\"}}\\n' ;;\n"
                                f"  search) printf '%s\\n' \"$@\" > {shlex.quote(str(calls))}; printf '[]\\n' ;;\n"
                                "esac\nexit 0\n", encoding="utf-8")
                stub.chmod(0o755)
                p = memcue_provider.MemcueMemoryProvider({"binary": str(stub)})
                p.initialize("s1", hermes_home=self.home, agent_context="primary")
                self.assertNotIn("error", json.loads(p.handle_tool_call("memcue_search", {"query": "порт"})))
                args = calls.read_text(encoding="utf-8").splitlines()
                self.assertEqual("-cwd" in args, scoped, args)
                self.assertEqual("-session" in args, scoped, args)
                self.assertEqual(args[-2:], ["--", "порт"])

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


TRUE = shutil.which("true") or "/bin/true"  # /usr/bin/true on macOS


class DreamRunTest(unittest.TestCase):
    """The dream after an extraction (#401); the extraction and the dream process are replaced, so no binary is
    needed."""

    def setUp(self):
        self.home = tempfile.mkdtemp()
        self.started, self.procs = [], []
        self.p = self.provider({})

    def provider(self, extra):
        p = memcue_provider.MemcueMemoryProvider({"binary": TRUE, "extract_url": "http://x",
                                                  "extract_model": "m", "extract_threshold": 1000, **extra})
        p.initialize("s1", hermes_home=self.home, agent_context="primary")
        p._compat, p._version = True, "0.13.0"
        p._extract_in_background = lambda msgs, event, session="", on_done=None: on_done and on_done(self.ok)
        p._start_dream = self.start
        self.ok = True
        return p

    def start(self, cmd, env):
        self.started.append((cmd, env.get("MEMCUE_API_URL"), env.get("MEMCUE_MODEL")))
        proc = subprocess.Popen(["sleep", "5"] if len(self.started) == 1 else [TRUE])
        self.procs.append(proc)
        return proc

    def tearDown(self):
        self.p._stop.set()
        for proc in self.procs:
            if proc.poll() is None:
                proc.kill()
            proc.wait()
        shutil.rmtree(self.home, ignore_errors=True)

    def extract(self, p=None):
        p = p or self.p
        p.sync_turn("a" * 1200, "ok", session_id="s1", messages=[{"role": "user", "content": "a" * 1200}])

    def test_a_successful_extraction_starts_one_dream(self):
        self.extract()
        self.assertEqual(self.started, [([TRUE, "dream", "-run"], "http://x", "m")])
        # The first dream still runs: the next extraction does not start a second one.
        self.p.on_session_end([{"role": "user", "content": "b" * 1200}])
        self.assertEqual(len(self.started), 1)
        self.p._dreaming.kill()
        self.p._dreaming.wait()
        self.p.on_session_end([{"role": "user", "content": "c" * 1200}])
        self.assertEqual(len(self.started), 2)

    def test_a_failed_extraction_starts_no_dream(self):
        self.ok = False
        self.extract()
        self.assertEqual(self.started, [])

    def test_dream_false_starts_no_dream(self):
        p = self.provider({"dream": False})
        self.extract(p)
        p._stop.set()
        self.assertEqual(self.started, [])

    def test_an_older_binary_starts_no_dream(self):
        self.p._version = "0.12.0"
        self.extract()
        self.assertEqual(self.started, [])

    def test_a_subagent_starts_no_dream(self):
        p = memcue_provider.MemcueMemoryProvider(dict(self.p._config))
        p.initialize("s2", hermes_home=self.home, agent_context="subagent")
        p._compat, p._version, p._start_dream = True, "0.13.0", self.start
        p._dream_in_background()
        p._stop.set()
        self.assertEqual(self.started, [])


@unittest.skipUnless(memcue_provider._release_target(), "no memcue release for this machine")
class InstallTest(unittest.TestCase):
    """The provider installs the binary of its release when none is configured; a local server stands in for GitHub."""

    def setUp(self):
        self.root = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, self.root, True)
        self.version = memcue_provider._plugin_version()
        build = Path(self.root) / "build"
        build.mkdir()
        (build / "memcue").write_text("#!/bin/sh\nexit 0\n")
        (build / "memcue").chmod(0o755)
        archive = f"memcue_{self.version}_{memcue_provider._release_target()}.tar.gz"
        subprocess.run(["tar", "-czf", str(Path(self.root) / archive), "-C", str(build), "memcue"], check=True)
        data = (Path(self.root) / archive).read_bytes()
        self.files = {f"/v{self.version}/{archive}": data,
                      f"/v{self.version}/checksums.txt": f"{hashlib.sha256(data).hexdigest()}  {archive}\n".encode()}
        self.hits = 0
        test = self

        class Handler(http.server.BaseHTTPRequestHandler):
            def do_GET(self):
                test.hits += 1
                body = test.files.get(self.path)
                self.send_response(200 if body else 404)
                self.end_headers()
                if body:
                    self.wfile.write(body)

            def log_message(self, *args):
                pass

        srv = http.server.HTTPServer(("127.0.0.1", 0), Handler)
        threading.Thread(target=srv.serve_forever, daemon=True).start()
        self.addCleanup(srv.server_close)
        self.addCleanup(srv.shutdown)
        self.home = str(Path(self.root) / "home")
        for key, value in {"MEMCUE_RELEASE_URL": f"http://127.0.0.1:{srv.server_address[1]}",
                           "PATH": "/usr/bin:/bin", "HERMES_HOME": self.home, "MEMCUE_AUTO_INSTALL": None,
                           "MEMCUE_FACTS": None}.items():
            old = os.environ.get(key)
            self.addCleanup(lambda k=key, v=old: os.environ.pop(k, None) if v is None else os.environ.update({k: v}))
            if value is None:
                os.environ.pop(key, None)
            else:
                os.environ[key] = value
        self.installed = Path(self.home) / "memcue" / "bin" / self.version / "memcue"

    def test_install_release_checks_the_checksum(self):
        memcue_provider._install_release(self.version, self.installed)
        self.assertTrue(os.access(self.installed, os.X_OK))
        self.installed.unlink()
        key = next(k for k in self.files if k.endswith("checksums.txt"))
        self.files[key] = b"0" * 64 + self.files[key][64:]
        with self.assertRaisesRegex(RuntimeError, "checksum"):
            memcue_provider._install_release(self.version, self.installed)
        self.assertFalse(self.installed.exists())
        self.assertFalse((self.installed.parent / ".lock").exists(), "the lock must be released")

    def test_the_provider_installs_its_release_without_a_binary(self):
        p = memcue_provider.MemcueMemoryProvider({})
        self.assertTrue(p.is_available(), "a provider that can install the binary is available")
        p.initialize("s1", hermes_home=self.home)
        p._installing.join(10)
        self.assertTrue(os.access(self.installed, os.X_OK))
        self.assertEqual(p._binary(), str(self.installed))
        again = memcue_provider.MemcueMemoryProvider({})
        again.initialize("s2", hermes_home=self.home)
        self.assertIsNone(again._installing, "an installed release must not be downloaded again")
        self.assertEqual(again._binary(), str(self.installed))

    def test_a_configured_binary_is_used_as_it_is(self):
        p = memcue_provider.MemcueMemoryProvider({"binary": str(Path(self.root) / "no-such-memcue")})
        p.initialize("s1", hermes_home=self.home)
        self.assertIsNone(p._installing)
        self.assertFalse(p.is_available())
        self.assertEqual(self.hits, 0)

    def test_auto_install_can_be_turned_off(self):
        os.environ["MEMCUE_AUTO_INSTALL"] = "0"
        p = memcue_provider.MemcueMemoryProvider({})
        self.assertFalse(p.is_available())
        p.initialize("s1", hermes_home=self.home)
        self.assertIsNone(p._installing)
        self.assertEqual(self.hits, 0)


if __name__ == "__main__":
    unittest.main()
