"""Offline tests of the memcue provider against the real memcue binary.

Run: python3 plugins/hermes/memcue/tests/test_provider.py (MEMCUE_BIN=/path/to/memcue, default: memcue on PATH).
Inside Hermes the real agent modules are used; elsewhere they are stubbed. MEMCUE_REQUIRE_HERMES=1 turns the stubs off:
the suite then fails to load without Hermes. The suite needs only this plugin directory, so it runs in an installed copy
as well as in the checkout.
"""

import contextlib
import datetime
import hashlib
import http.server
import importlib.util
import inspect
import json
import re
import threading
import os
import shlex
import shutil
import stat
import subprocess
import sys
import tempfile
import types
import unittest
import unittest.mock
from pathlib import Path

HERE = Path(__file__).resolve().parent.parent


def _stub_hermes():
    try:
        import agent.memory_provider  # noqa: F401
        import tools.registry  # noqa: F401
        return
    except ImportError:
        if os.environ.get("MEMCUE_REQUIRE_HERMES") == "1":
            raise
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


_BUILTIN_MEMORY_FLAGS = memcue_provider._builtin_memory_flags  # the real reader, for its own test


def _pin_builtin_memory(test: unittest.TestCase) -> None:
    """Whether Hermes injects MEMORY.md and USER.md is read from the config of the host Hermes (#504): left to it, a
    test is green where the built-in memory is on and red where it is off. A test pins Hermes's default, both on, and
    patches another combination itself."""
    patcher = unittest.mock.patch.object(memcue_provider, "_builtin_memory_flags", return_value=(True, True))
    patcher.start()
    test.addCleanup(patcher.stop)


def _builtin_memory_warnings(logs, home) -> int:
    """How many times the captured logs hold the warning about the built-in memory of this home, word for word: any
    other warning, a version or a mirror failure, is not it (#504)."""
    want = (f"WARNING:memcue_provider:memcue: the built-in memory notes ({Path(home) / 'memories'}) are still in the "
            "system prompt next to memcue; the model keeps its memory in memcue, so turn the built-in memory off in Hermes")
    return logs.output.count(want)


@unittest.skipUnless(BIN, "memcue binary not found (set MEMCUE_BIN)")
class ProviderTest(unittest.TestCase):
    def setUp(self):
        # Every cleanup is registered before what it undoes, so a setUp that fails halfway leaves nothing behind: the
        # environment comes back as it was, a variable that was missing included (#504), and the home goes.
        env = unittest.mock.patch.dict(os.environ)
        env.start()
        self.addCleanup(env.stop)
        self.home = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, self.home, True)
        os.environ["XDG_STATE_HOME"] = str(Path(self.home) / "state")  # memcue's extract.log
        # The suite must never touch a live base: an inherited MEMCUE_FACTS wins over HERMES_HOME and let an earlier
        # run write test facts into a real base and migrate it to another schema. MEMCUE_USAGE_LOG from the container
        # would turn the usage log on behind the tests that assert it is off, and a recall setting changes what is served.
        for key in _shell_memcue_vars():
            del os.environ[key]
        _pin_builtin_memory(self)
        self.p = memcue_provider.MemcueMemoryProvider({"binary": BIN})
        self.p.initialize("s1", hermes_home=self.home, agent_context="primary")

    def facts(self):
        return self.memcue("facts")

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

    def test_the_move_to_text_keeps_the_mirror(self):
        """`memcue migrate -text` turns $HERMES_HOME/memcue/facts.db into the text history next to it; the provider
        opens the text from the next start on and keeps the mirror of the built-in memory, since it is the same base
        (#530)."""
        self.p.on_memory_write("add", "memory", "Факт до перехода.")
        written = self.p._written()
        self.assertTrue(written)
        self.memcue("migrate", "-text")

        moved = memcue_provider.MemcueMemoryProvider({"binary": BIN})
        moved.initialize("s2", hermes_home=self.home, agent_context="primary")
        self.assertEqual(moved._facts, str(Path(self.home) / "memcue" / "facts"))
        self.assertEqual(moved._written(), written)
        self.assertEqual(Path(moved._state, "base").read_text(encoding="utf-8").strip(), moved._facts)
        self.assertTrue(any("Факт до перехода." in line for line in self.memcue_at(moved._facts, "facts")))

    def memcue_at(self, facts, *args):
        out = subprocess.run([BIN, *args], env=dict(os.environ, MEMCUE_FACTS=facts),
                             capture_output=True, text=True, check=True).stdout
        return [line for line in out.splitlines() if line]

    def test_facts_path_expands_home(self):
        p = memcue_provider.MemcueMemoryProvider({"binary": BIN, "facts": "~/memcue/facts.db"})
        self.assertEqual(p._facts, str(Path(os.path.expanduser("~")) / "memcue" / "facts.db"))
        self.assertTrue(p.is_available())

    def test_relative_facts_path_is_unavailable(self):
        p = memcue_provider.MemcueMemoryProvider({"binary": BIN, "facts": "relative/facts.db"})
        self.assertFalse(p.is_available())
        self.assertIn("absolute", p.unavailable_reason())

    def test_an_older_binary_turns_the_card_off(self):
        stub = Path(self.home, "old-memcue")
        stub.write_text("#!/bin/sh\ncase \"$1\" in\n  version) printf '{\"version\":\"0.3.0\"}\\n' ;;\n"
                        "  hook) printf '{}\\n' ;;\nesac\nexit 0\n", encoding="utf-8")
        stub.chmod(0o755)
        p = memcue_provider.MemcueMemoryProvider({"binary": str(stub)})
        p.initialize("s1", hermes_home=self.home)
        self.assertFalse(p._binary_ok())
        self.assertEqual(p._card_text(), "")

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

    def test_a_mirror_is_approved_at_the_write_and_searchable(self):
        """A built-in memory note is mirrored as a live fact with its own origin (#440, #514): nothing waits for an
        approval, and the note is the user's own words."""
        self.p.on_memory_write("add", "memory", "Проект demo: база PostgreSQL 18 слушает порт 55432.")
        listed = self.facts()
        self.assertEqual(len(listed), 1)
        self.assertIn("55432", listed[0])
        found = json.loads(self.p.handle_tool_call("memcue_search", {"query": "порт postgres demo"}))
        self.assertIn("55432", found["facts"][0]["content"])
        self.assertIn("hermes-memory", "\n".join(self.memcue("dump", self.p._facts)))

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
        fact_id = self.written()[self.p._key("memory", old)]
        self.p.on_memory_write("replace", "memory", new, {"previous_content": old})
        dump = "\n".join(self.memcue("dump", self.p._facts))
        self.assertIn("55433", dump)
        self.assertIn(f"{fact_id} v2 active", "\n".join(self.memcue("dump", self.p._facts)))
        self.p.on_memory_write("remove", "memory", "", {"previous_content": new})
        self.assertEqual(self.facts(), [])

    def test_replace_of_a_retired_mirror_adds_a_new_fact(self):
        old = "Проект demo: база PostgreSQL 18 слушает порт 55432."
        new = "Проект demo: база PostgreSQL 18 слушает порт 55433."
        self.p.on_memory_write("add", "memory", old)
        fact_id = self.written()[self.p._key("memory", old)]
        self.memcue("retire", fact_id)
        # The note is edited after vet or a person retired its mirror: correct must not revive the retired fact.
        self.p.on_memory_write("replace", "memory", new, {"previous_content": old})
        dump = "\n".join(self.memcue("dump", self.p._facts))
        self.assertIn("55433", dump)
        new_id = self.written()[self.p._key("memory", new)]
        self.assertNotEqual(new_id, fact_id)
        self.assertEqual(self.written()[self.p._key("memory", new)], new_id)
        self.assertIn(f"{fact_id} v2 retired", "\n".join(self.memcue("dump", self.p._facts)))

    def written(self) -> dict:
        return json.loads((Path(self.p._state) / "written.json").read_text(encoding="utf-8"))

    def test_failed_replace_keeps_the_mapping_and_a_later_remove_retires_the_fact(self):
        old = "Проект demo: база PostgreSQL 18 слушает порт 55432."
        new = "Проект demo: база PostgreSQL 18 слушает порт 55433."
        self.p.on_memory_write("add", "memory", old)
        old_key = self.p._key("memory", old)
        fact_id = self.written()[old_key]
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
        dump = "\n".join(self.memcue("dump", self.p._facts))
        self.assertIn("55432", dump)
        self.assertNotIn("55433", dump)

        # The memory entry still says old, so a later remove reaches the fact through the kept mapping.
        self.p.on_memory_write("remove", "memory", "", {"previous_content": old})
        self.assertEqual(self.facts(), [])
        self.assertEqual(self.written(), {})

    def test_failed_remove_keeps_the_mapping(self):
        old = "Проект demo: база PostgreSQL 18 слушает порт 55432."
        self.p.on_memory_write("add", "memory", old)
        old_key = self.p._key("memory", old)
        fact_id = self.written()[old_key]

        real_run = self.p._run
        self.p._run = lambda args, **kwargs: (False, "memcue timed out after 15s")
        try:
            self.p.on_memory_write("remove", "memory", "", {"previous_content": old})
        finally:
            self.p._run = real_run

        self.assertEqual(self.written(), {old_key: fact_id})
        self.assertIn("55432", "\n".join(self.memcue("dump", self.p._facts)))  # still there to be retired later

    def test_a_mirror_add_carries_kind_and_scope(self):
        """The add of a mirror names the derived kind and scope (#488); a note without the host passes no -scope, so
        the binary keeps its own default instead of being told global."""
        calls = Path(self.home) / "mirror-args"
        stub = Path(self.home) / "mirror-memcue"
        stub.write_text("#!/bin/sh\n"
                        "case \"$1\" in\n"
                        "  version) printf '{\"version\":\"dev\"}\\n' ;;\n"
                        f"  add) printf '%s\\n' \"$@\" --- >> {shlex.quote(str(calls))}; echo f1 ;;\n"
                        "esac\nexit 0\n", encoding="utf-8")
        stub.chmod(0o755)
        p = memcue_provider.MemcueMemoryProvider({"binary": str(stub), "host": "builder"})
        p.initialize("s1", hermes_home=self.home, agent_context="primary")
        p.on_memory_write("add", "memory", "На builder Caddy слушает порт 8443.")
        p.on_memory_write("replace", "memory", "Сборка падает без CGO.", {"previous_content": "unknown"})
        hosted, plain = [c.strip("\n").split("\n") for c in calls.read_text(encoding="utf-8").split("---\n")
                         if c.strip()]
        self.assertEqual(hosted[hosted.index("-kind") + 1], "environment")
        self.assertEqual(hosted[hosted.index("-scope") + 1], "host:builder")
        self.assertEqual(hosted[hosted.index("-source") + 1], "memory")
        self.assertEqual(plain[plain.index("-kind") + 1], "pitfall")
        self.assertNotIn("-scope", plain)
        self.assertEqual(plain[-1], "Сборка падает без CGO.")

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

    def test_the_shelf_text_follows_the_note_and_precedes_the_card(self):
        """#567: the research-shelf text measured in #531 sits after the memory note and before the session card, where
        the confirming run had it."""
        self.memcue("add", "-kind", "rule", "-scope", "global", "-source", "user", "--",
                    "Всегда отвечай пользователю по-русски.")
        block = self.p.system_prompt_block()
        note = block.index("To fix a stored fact")
        shelf = block.index(memcue_provider._SHELF_NOTE)
        card = block.index("memcue: rules and decisions")
        self.assertLess(note, shelf)
        self.assertLess(shelf, card)

    def test_the_pitfall_line_follows_the_shelf_text_and_precedes_the_card(self):
        """#566: the pitfall line sits between the shelf text and the session card, where the measured run had it."""
        self.memcue("add", "-kind", "rule", "-scope", "global", "-source", "user", "--",
                    "Всегда отвечай пользователю по-русски.")
        block = self.p.system_prompt_block()
        shelf = block.index(memcue_provider._SHELF_NOTE)
        pitfall = block.index(memcue_provider._FAILURE_NOTE)
        card = block.index("memcue: rules and decisions")
        self.assertLess(shelf, pitfall)
        self.assertLess(pitfall, card)

    def test_the_note_and_the_schema_let_the_model_search_during_work(self):
        """#521: the model may search for a prior pitfall during work on a system and before it diagnoses an
        unexpected error, by its own choice; the guidance for questions about earlier sessions stays."""
        search = memcue_provider.SEARCH_SCHEMA["description"]
        for text in (self.p.system_prompt_block(), search):
            with self.subTest(text=text[:40]):
                self.assertIn("During work", text)
                self.assertIn("specific host, service or repository", text)
                self.assertIn("unexpected error", text)
                self.assertIn("short query naming the system and the problem", text)
                self.assertIn("against the current state", text)
                self.assertIn("before answering" if text is search else "Before you answer", text)
                self.assertIn("earlier sessions", text)
                for word in ("must", "always", "every tool"):
                    self.assertNotIn(word, text.lower())
        block = self.p.system_prompt_block()
        self.assertIn("several times with synonyms, the system name and both languages", block)
        self.assertIn("when you decide it may help", block)
        self.assertIn("when you decide it helps", search)
        # The note is deterministic: two builds give the same text. That alone does not show that Hermes reuses
        # its prompt cache.
        self.assertEqual(block, self.p.system_prompt_block())

    def test_no_search_runs_without_the_model(self):
        """#521 adds words only: no hook of the provider runs a search, a turn with an error included."""
        calls = Path(self.home, "calls")
        stub = Path(self.home, "logging-memcue")
        stub.write_text(f'#!/bin/sh\necho "$1" >> {shlex.quote(str(calls))}\nexec {shlex.quote(BIN)} "$@"\n',
                        encoding="utf-8")
        stub.chmod(0o755)
        p = memcue_provider.MemcueMemoryProvider({"binary": str(stub)})
        p.initialize("s1", hermes_home=self.home, agent_context="primary")
        self.memcue("add", "-kind", "pitfall", "--", "Хост vega-3: sshd слушает порт 2222, а не 22.")
        error = "ssh vega-3: connection refused on port 22, unexpected error"
        p.system_prompt_block()
        self.assertEqual(p.prefetch(error, session_id="s1"), "")
        p.sync_turn(error, "The command failed: " + error, session_id="s1",
                    messages=[{"role": "tool", "content": error}])
        p.on_pre_compress([{"role": "tool", "content": error}])
        p.system_prompt_block()
        called = calls.read_text(encoding="utf-8").split() if calls.exists() else []
        self.assertIn("session-card", called)  # the log sees the provider's calls
        self.assertNotIn("search", called)
        p.handle_tool_call("memcue_search", {"query": "vega-3 ssh"})  # the model's own call is the one that searches
        self.assertIn("search", calls.read_text(encoding="utf-8").split())

    def _notes_and_rules(self) -> list:
        """Memory notes and the card rules of #456 next to them: copies of a note, and rules in the same words that
        negate a note or add a condition to it. Returns the rules."""
        memories = Path(self.home) / "memories"
        memories.mkdir()
        (memories / "MEMORY.md").write_text(
            "Изменения скиллов агента пушить напрямую в ветку main репозитория скиллов, без отдельных веток и ревью.\n§\n"
            "Перед каждым коммитом гонять полный набор тестов проекта.\n", encoding="utf-8")
        (memories / "USER.md").write_text(
            "- Мержит PR сразу после зелёных проверок и не ждёт отдельного подтверждения.\n", encoding="utf-8")
        rules = ["Пользователь мержит PR сразу после зелёных проверок, отдельного подтверждения не ждёт.",
                 "Перед каждым коммитом гонять полный набор тестов проекта.",
                 "Изменения скиллов агента никогда не пушить напрямую в ветку main репозитория скиллов: только через "
                 "отдельную ветку и ревью.",
                 "Полный набор тестов гонять перед коммитом, только если правка задевает пакет store.",
                 "Сначала вся работа заводится задачами (issue), и только потом выполняется."]
        for content in rules:
            self.memcue("add", "-kind", "rule", "-scope", "global", "-source", "user", "--", content)
        return rules

    def test_the_card_keeps_what_the_memory_notes_say(self):
        """The plugin drops no card line (#456): a copy of a memory note, a rule in the same words that negates a note
        or adds a condition to it, and a rule the notes do not carry all reach the prompt, whether Hermes injects its
        memory or not. The block is the memory note, a blank line and the card exactly as memcue renders it."""
        rules = self._notes_and_rules()
        card = self.p._card_text()
        self.assertEqual(sum(line.startswith("- ") for line in card.splitlines()), len(rules))
        blocks = []
        for flags in ((True, True), (True, False), (False, True), (False, False)):
            with self.subTest(flags=flags), \
                    unittest.mock.patch.object(memcue_provider, "_builtin_memory_flags", return_value=flags):
                block = self.p.system_prompt_block()
                self.assertTrue(block.startswith("# Long-term memory (memcue)\n"))
                self.assertTrue(block.endswith("\n\n" + card))
                self.assertEqual(block.count("memcue: rules and decisions"), 1)
                for content in rules:
                    self.assertIn(content, block)
                blocks.append(block)
        self.assertEqual(len(set(blocks)), 1)

    def test_a_card_of_copies_only_is_a_card(self):
        """A card whose every line repeats a memory note still goes in whole (#456)."""
        memories = Path(self.home) / "memories"
        memories.mkdir()
        (memories / "USER.md").write_text(
            "- Мержит PR сразу после зелёных проверок и не ждёт отдельного подтверждения.\n", encoding="utf-8")
        self.memcue("add", "-kind", "rule", "-scope", "global", "-source", "user", "--",
                    "Пользователь мержит PR сразу после зелёных проверок, отдельного подтверждения не ждёт.")
        block = self.p.system_prompt_block()
        self.assertIn("memcue: rules and decisions", block)
        self.assertIn("Пользователь мержит PR сразу после зелёных проверок", block)

    def test_a_memory_hermes_turned_off_is_no_note(self):
        """memory.memory_enabled and memory.user_profile_enabled turn the built-in memory off and leave its files (#494):
        a file Hermes no longer injects is not warned about. The card is the same either way (#456)."""
        memories = Path(self.home) / "memories"
        memories.mkdir()
        (memories / "MEMORY.md").write_text(
            "- Требует, чтобы навыки содержали только процедуры: состояние среды — в памяти, замеры — в docs/research.\n",
            encoding="utf-8")
        self.memcue("add", "-kind", "rule", "-scope", "global", "-source", "user", "--",
                    "Пользователь требует, чтобы в навыках были только процедуры, а состояние среды лежало в памяти.")
        for flags, injected in (((True, True), True), ((False, True), False)):
            with self.subTest(flags=flags), \
                    unittest.mock.patch.object(memcue_provider, "_builtin_memory_flags", return_value=flags):
                self.assertIn("в навыках были только процедуры", self.p.system_prompt_block())
                p = memcue_provider.MemcueMemoryProvider({"binary": BIN})
                if injected:
                    with self.assertLogs("memcue_provider", level="WARNING") as logs:
                        p.initialize("s1", hermes_home=self.home)
                    self.assertEqual(_builtin_memory_warnings(logs, self.home), 1)
                else:
                    with self.assertNoLogs("memcue_provider", level="WARNING"):
                        p.initialize("s1", hermes_home=self.home)

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
        self.p._delivered["s2"] = [{"tokens": ["x"], "chars": 1}]
        self.p.on_session_switch("s2", reset=True)
        self.assertEqual(self.p._session_id, "s2")
        self.assertEqual(self.p._generation, 0)
        self.assertNotIn("s2", self.p._delivered)
        self.p.on_session_switch("s2", rewound=True)
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
        self.assertIn("пишет по-русски", "\n".join(self.memcue("dump", self.p._facts)))

    def test_content_starting_with_dash_is_stored(self):
        self.p.on_memory_write("add", "memory", "-v печатает версию memcue")
        self.assertIn("-v печатает", "\n".join(self.memcue("dump", self.p._facts)))

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

    def test_the_model_is_told_to_keep_its_memory(self):
        """Nothing is extracted any more (#494): the system prompt tells the model to store what it learns itself."""
        block = self.p.system_prompt_block()
        self.assertIn("You keep this memory yourself", block)
        self.assertIn("memcue_add", block)
        self.assertNotIn("copied into memcue", block)
        self.assertIn("at once, when you learn it", memcue_provider.ADD_SCHEMA["description"])

    def test_a_stated_rule_reaches_the_card(self):
        """A rule or decision is the user's words by default, and only those reach the session card (#494); another
        source is passed as given, and an unknown one is refused."""
        added = json.loads(self.p.handle_tool_call("memcue_add", {
            "content": "Всегда пиши коммиты по-английски.", "kind": "rule", "scope": "global"}))
        fact_id = added["result"].split()[0]
        line = next(l for l in self.memcue("dump", self.p._facts) if l.startswith(fact_id))
        self.assertIn("source=user", line)
        self.assertIn("Всегда пиши коммиты", self.p.system_prompt_block())
        added = json.loads(self.p.handle_tool_call("memcue_add", {
            "content": "Контейнер demo пишет логи в /var/log/demo.", "kind": "environment", "source": "tool"}))
        fact_id = added["result"].split()[0]
        line = next(l for l in self.memcue("dump", self.p._facts) if l.startswith(fact_id))
        self.assertIn("source=tool", line)
        self.assertIn("error", json.loads(self.p.handle_tool_call("memcue_add", {"content": "x", "source": "boss"})))

    def test_the_card_asks_for_the_dream_tools(self):
        """With no extraction, the card's hint is what starts a dream in Hermes (#494): the card asks memcue to name
        the dream tools, unless dream is off or the binary predates the flag."""
        for version, config, want in (("0.19.0", {}, True), ("0.18.1", {}, False), ("0.19.0", {"dream": False}, False)):
            with self.subTest(version=version, config=config):
                calls = Path(self.home) / f"card-args-{version}-{len(config)}"
                stub = Path(self.home) / f"card-memcue-{version}-{len(config)}"
                stub.write_text("#!/bin/sh\n"
                                f"case \"$1\" in\n  version) printf '{{\"version\":\"{version}\"}}\\n' ;;\n"
                                f"  session-card) printf '%s\\n' \"$@\" >> {shlex.quote(str(calls))} ;;\nesac\nexit 0\n",
                                encoding="utf-8")
                stub.chmod(0o755)
                base = Path(self.home) / "dream.db"
                base.write_text("", encoding="utf-8")
                p = memcue_provider.MemcueMemoryProvider(dict(config, binary=str(stub), facts=str(base)))
                p.initialize("s1", hermes_home=self.home)
                p.system_prompt_block()
                self.assertEqual("-dream-tools" in calls.read_text(encoding="utf-8").splitlines(), want)

    def test_the_built_in_memory_is_warned_about_once(self):
        """The built-in memory is a second memory (#494): while its notes are there the provider says so once per
        start, and says nothing without them."""
        p = memcue_provider.MemcueMemoryProvider({"binary": BIN})
        with self.assertNoLogs("memcue_provider", level="WARNING"):
            p.initialize("s1", hermes_home=self.home)
        Path(self.home, "memories").mkdir(exist_ok=True)
        Path(self.home, "memories", "MEMORY.md").write_text("Заметка встроенной памяти.\n", encoding="utf-8")
        p = memcue_provider.MemcueMemoryProvider({"binary": BIN})
        with self.assertLogs("memcue_provider", level="WARNING") as logs:
            p.initialize("s1", hermes_home=self.home)
            p.initialize("s1", hermes_home=self.home)
        self.assertEqual(_builtin_memory_warnings(logs, self.home), 1)

    def test_each_flag_turns_its_own_file_off(self):
        """memory.memory_enabled turns MEMORY.md off and memory.user_profile_enabled turns USER.md off (#494): the
        notes are those of the files Hermes injects, and the warning comes while any of them is there."""
        Path(self.home, "memories").mkdir()
        Path(self.home, "memories", "MEMORY.md").write_text("Заметка встроенной памяти.\n", encoding="utf-8")
        Path(self.home, "memories", "USER.md").write_text("Заметка о пользователе.\n", encoding="utf-8")
        cases = (((True, True), ["Заметка встроенной памяти.", "Заметка о пользователе."]),
                 ((True, False), ["Заметка встроенной памяти."]),
                 ((False, True), ["Заметка о пользователе."]),
                 ((False, False), []))
        for flags, notes in cases:
            with self.subTest(flags=flags), \
                    unittest.mock.patch.object(memcue_provider, "_builtin_memory_flags", return_value=flags):
                self.assertEqual(self.p._memory_notes(), notes)
                p = memcue_provider.MemcueMemoryProvider({"binary": BIN})
                if notes:
                    with self.assertLogs("memcue_provider", level="WARNING") as logs:
                        p.initialize("s1", hermes_home=self.home)
                    self.assertEqual(_builtin_memory_warnings(logs, self.home), 1)
                else:
                    with self.assertNoLogs("memcue_provider", level="WARNING"):
                        p.initialize("s1", hermes_home=self.home)

    def test_the_extraction_state_of_an_older_provider_goes(self):
        """sessions/ and pending/ held what the extraction saw; pending/ holds raw messages (#494)."""
        for name in ("sessions", "pending"):
            Path(self.p._state, name).mkdir(parents=True, exist_ok=True)
            Path(self.p._state, name, "x.json").write_text("[]", encoding="utf-8")
        self.p.initialize("s1", hermes_home=self.home)
        for name in ("sessions", "pending"):
            self.assertFalse(Path(self.p._state, name).exists(), name)

    def test_add_passes_shelf(self):
        """memcue_add puts a fact on the shelf: no search reads it but one that names the shelf (#433)."""
        added = json.loads(self.p.handle_tool_call("memcue_add", {
            "content": "Example API разрешает 60 запросов в минуту на ключ.", "keywords": "example, лимит",
            "kind": "environment", "shelf": "research"}))
        fact_id = added["result"].split()[0]
        line = next(l for l in self.memcue("dump", self.p._facts) if l.startswith(fact_id))
        self.assertIn("scope=shelf:research", line)
        empty = json.loads(self.p.handle_tool_call("memcue_search", {"query": "example лимит"}))
        self.assertEqual(empty["facts"], [])
        # The shelf hint of the binary reaches the model: it is the only way back to a shelved fact (#453).
        self.assertIn("-in shelf:research", empty.get("hint", ""))
        found = json.loads(self.p.handle_tool_call("memcue_search", {"query": "example лимит", "in": "shelf:research"}))
        self.assertEqual([(f["id"], f.get("scope")) for f in found["facts"]], [(fact_id, "shelf:research")])

    def test_search_documents_the_shelf(self):
        schema = next(s for s in self.p.get_tool_schemas() if s["name"] == "memcue_search")
        self.assertIn("shelf:research", schema["parameters"]["properties"]["in"]["description"])
        add = next(s for s in self.p.get_tool_schemas() if s["name"] == "memcue_add")
        self.assertIn("shelf", add["parameters"]["properties"])

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

    def test_search_refuses_a_zero_limit(self):
        """0 is refused like a negative limit, not read as the default (#477); an empty or false value is absent."""
        for i in range(6):
            json.loads(self.p.handle_tool_call("memcue_add", {
                "content": f"Сервер server{i}: Caddy перечитывает Caddyfile после пересоздания контейнера.",
                "keywords": "caddy"}))
        for limit in (0, "0", -3, "-3"):
            with self.subTest(limit=limit):
                found = json.loads(self.p.handle_tool_call("memcue_search", {"query": "caddy", "limit": limit}))
                self.assertEqual(found.get("error"), "limit must be a positive integer")
        found = json.loads(self.p.handle_tool_call("memcue_search", {"query": "caddy пересоздание", "limit": 3}))
        self.assertEqual(len(found["facts"]), 3)
        for args in ({}, {"limit": None}, {"limit": ""}, {"limit": False}):
            with self.subTest(args=args):
                found = json.loads(self.p.handle_tool_call("memcue_search", {"query": "caddy пересоздание", **args}))
                self.assertEqual(len(found["facts"]), memcue_provider._SEARCH_FACTS)

    def test_search_passes_the_controls(self):
        """in, kind, words and expired reach memcue search (#409); a wrong value comes back as an error."""
        rule = json.loads(self.p.handle_tool_call("memcue_add", {
            "content": "Caddy на server1 перечитывает Caddyfile после пересоздания.", "keywords": "caddy",
            "kind": "rule", "scope": "global"}))["result"].split()[0]
        json.loads(self.p.handle_tool_call("memcue_add", {
            # A scope of its own: without one the fact takes the working directory's project, or global outside a
            # repository (#442), and the -in global check below would depend on where the suite runs.
            "content": "Caddy на server2 падает без конфига.", "keywords": "caddy", "kind": "pitfall",
            "scope": "host:server2"}))
        def ids(**kw):
            found = json.loads(self.p.handle_tool_call("memcue_search", dict(query="caddy server1", **kw)))
            return [f["id"] for f in found["facts"]]
        self.assertEqual(ids(kind="rule", min_score="0"), [rule])
        self.assertEqual(len(ids(**{"in": "all"}, min_score=0)), 2)  # a number 0 widens too
        self.assertEqual(ids(words="all"), [rule])
        self.assertEqual(ids(**{"in": "global"}, min_score="0"), [rule])
        self.assertEqual(len(ids(**{"in": "all"}, expired="true", min_score="0")), 2)
        for bad in ({"kind": "wish"}, {"in": "everywhere"}, {"words": "some"}):
            self.assertIn("error", json.loads(self.p.handle_tool_call("memcue_search", dict(query="caddy", **bad))))

    def test_search_takes_the_whole_base_in_a_repository(self):
        """memcue searches only the workspace's repository by default; a Hermes chat is not about one (#409)."""
        other = self.memcue("add", "-origin", "example.com/them/lib 2026-10-25", "-scope", "repo:example.com/them/lib",
                            "-keywords", "caddy", "Caddy на server3 держит конфиг в /etc/caddy.")[0].split()[0]
        # A repository of its own, not the checkout the test may run in: an installed plugin lies in none (#526). Git
        # config outside it is off, so an insteadOf of the host cannot rewrite its remote; setUp restores the environment.
        os.environ.update(GIT_CONFIG_GLOBAL=os.devnull, GIT_CONFIG_SYSTEM=os.devnull, GIT_CONFIG_NOSYSTEM="1")
        repo = Path(self.home) / "app"
        (repo / ".git").mkdir(parents=True)
        (repo / ".git" / "config").write_text('[remote "origin"]\n\turl = https://example.com/us/app.git\n',
                                              encoding="utf-8")
        self.p._workspace = str(repo)
        found = json.loads(self.p.handle_tool_call("memcue_search", {"query": "caddy server3"}))
        self.assertEqual([f["id"] for f in found["facts"]], [other])
        narrow = json.loads(self.p.handle_tool_call("memcue_search", {"query": "caddy server3", "in": "project"}))
        self.assertEqual(narrow["facts"], [])
        # The workspace is a project to memcue: the narrow search finds a fact of this repository.
        own = self.memcue("add", "-origin", "example.com/us/app 2026-10-27", "-scope", "repo:example.com/us/app",
                          "-keywords", "nginx", "Nginx на server3 слушает порт 8443.")[0].split()[0]
        mine = json.loads(self.p.handle_tool_call("memcue_search", {"query": "nginx server3", "in": "project"}))
        self.assertEqual([f["id"] for f in mine["facts"]], [own])

    def test_search_answer_reads_both_shapes(self):
        """search -json is an object with facts and hints (#453); an older binary prints a bare array."""
        self.assertEqual(memcue_provider._search_answer('[{"id": "a"}]'), ([{"id": "a"}], []))
        self.assertEqual(memcue_provider._search_answer('{"facts": [], "hints": ["h", 3]}'), ([], ["h"]))
        with self.assertRaises(ValueError):
            memcue_provider._search_answer('{"result": "x"}')

    def test_search_hints_on_cjk(self):
        """A Chinese query gets the hint to search in the facts' languages, found or not; a Russian one does not (#410)."""
        json.loads(self.p.handle_tool_call("memcue_add", {
            "content": "Caddy на server1 перечитывает Caddyfile после пересоздания.", "keywords": "caddy"}))
        found = json.loads(self.p.handle_tool_call("memcue_search", {"query": "如何重启 caddy"}))
        self.assertEqual(len(found["facts"]), 1)
        self.assertIn("Russian and English", found["hint"])
        self.assertEqual(found["hint"].count("Russian and English"), 1)  # the binary's hint and the plugin's are one
        self.assertIn("hint", json.loads(self.p.handle_tool_call("memcue_search", {"query": "如何重启"})))
        self.assertNotIn("hint", json.loads(self.p.handle_tool_call("memcue_search", {"query": "caddy пересоздание"})))

    def test_search_hints_when_the_floor_holds_facts_back(self):
        """A single fact may be all the score floor allows: the answer says so and names the lever (#407)."""
        strong = json.loads(self.p.handle_tool_call("memcue_add", {
            "content": "Сервер server1: Caddy перечитывает Caddyfile после пересоздания контейнера.",
            "keywords": "caddy"}))["result"].split()[0]
        for i in range(4):
            json.loads(self.p.handle_tool_call("memcue_add", {
                "content": f"Заметка {i} о Caddy: сертификаты обновляются через ACME на стенде.",
                "keywords": "caddy"}))
        query = "caddy пересоздание контейнера server1"
        found = json.loads(self.p.handle_tool_call("memcue_search", {"query": query}))
        self.assertEqual([f["id"] for f in found["facts"]], [strong])
        self.assertIn("min_score 0", found["hint"])
        wide = json.loads(self.p.handle_tool_call("memcue_search", {"query": query, "min_score": "0"}))
        self.assertGreater(len(wide["facts"]), 1)  # the hint named what the floor held back
        self.assertNotIn("hint", wide)  # a search that already asks wide needs no hint
        nothing = json.loads(self.p.handle_tool_call("memcue_search", {"query": "квазар антигравитация"}))
        self.assertEqual(nothing["facts"], [])  # nothing below the floor to name
        self.assertNotIn("hint", nothing)

    def test_floor_hint_counts_the_tail_with_its_own_limit(self):
        """The wide call asks for min(4 * limit, 20) facts while the answer keeps the caller's -n, and the hint counts
        the facts the wide call returned beyond the served ones, not limit - served (#423)."""
        served = '{"id":"a","content":"x"}'
        tail = ",".join(f'{{"id":"t{i}","content":"x"}}' for i in range(6))
        for limit, wide_n in (("2", "8"), (None, "20"), ("10", "20")):
            with self.subTest(limit=limit):
                calls = Path(self.home) / f"floor-args-{limit}"
                count = Path(self.home) / f"floor-count-{limit}"
                stub = Path(self.home) / f"floor-memcue-{limit}"
                stub.write_text("#!/bin/sh\n"
                                "case \"$1\" in\n"
                                "  version) printf '{\"version\":\"dev\"}\\n' ;;\n"
                                f"  search) printf '%s\\n' \"$@\" --- >> {shlex.quote(str(calls))}\n"
                                f"    echo x >> {shlex.quote(str(count))}\n"
                                f"    if [ \"$(wc -l < {shlex.quote(str(count))})\" -eq 1 ]; then printf '[{served}]\\n'\n"
                                f"    else printf '[{served},{tail}]\\n'; fi ;;\n"
                                "esac\nexit 0\n", encoding="utf-8")
                stub.chmod(0o755)
                p = memcue_provider.MemcueMemoryProvider({"binary": str(stub)})
                p.initialize("s1", hermes_home=self.home, agent_context="primary")
                args = {"query": "caddy"} if limit is None else {"query": "caddy", "limit": limit}
                found = json.loads(p.handle_tool_call("memcue_search", args))
                self.assertEqual([f["id"] for f in found["facts"]], ["a"])
                self.assertTrue(found["hint"].startswith("6 more facts hold the words"), found["hint"])
                first, second = [c.strip("\n").split("\n") for c in
                                 calls.read_text(encoding="utf-8").split("---\n") if c.strip()]
                self.assertEqual(first[first.index("-n") + 1], limit or str(memcue_provider._SEARCH_FACTS))
                self.assertEqual(second[second.index("-n") + 1], wide_n)
                self.assertEqual(second[second.index("-min-score") + 1], "0")

    def test_a_shelf_search_names_its_floor_whatever_the_count(self):
        """A shelf fact under the floor went unsaid behind two others (#546): a search that names a shelf is told of
        the facts below its floor however many came back, an ordinary one only when one fact or none did, and an answer
        the limit ended gets no such hint, since what lies past it is not below the floor."""
        served = '{"id":"a","content":"x"},{"id":"b","content":"y"}'
        cases = (({"in": "shelf:research"}, True), ({"in": "all,shelf"}, True), ({"in": "all"}, False),
                 ({"in": "shelf:research", "limit": "2"}, False))
        for extra, hinted in cases:
            with self.subTest(**extra):
                tag = "-".join(f"{k}{v}" for k, v in extra.items()).replace(":", "").replace(",", "")
                count = Path(self.home) / f"shelf-count-{tag}"
                stub = Path(self.home) / f"shelf-memcue-{tag}"
                stub.write_text("#!/bin/sh\n"
                                "case \"$1\" in\n"
                                "  version) printf '{\"version\":\"dev\"}\\n' ;;\n"
                                f"  search) echo x >> {shlex.quote(str(count))}\n"
                                f"    if [ \"$(wc -l < {shlex.quote(str(count))})\" -eq 1 ]; then printf '[{served}]\\n'\n"
                                f"    else printf '[{served},{{\"id\":\"c\",\"content\":\"z\"}}]\\n'; fi ;;\n"
                                "esac\nexit 0\n", encoding="utf-8")
                stub.chmod(0o755)
                p = memcue_provider.MemcueMemoryProvider({"binary": str(stub)})
                p.initialize("s-shelf", hermes_home=self.home, agent_context="primary")
                found = json.loads(p.handle_tool_call("memcue_search", dict(query="quarrel", **extra)))
                self.assertEqual([f["id"] for f in found["facts"]], ["a", "b"])
                calls = len(count.read_text(encoding="utf-8").split())
                if hinted:
                    self.assertTrue(found["hint"].startswith("1 more facts hold the words but sit below the score "
                                                             "floor and are not in this answer"), found["hint"])
                    self.assertIn("min_score 0", found["hint"])
                    self.assertEqual(calls, 2)
                else:
                    self.assertNotIn("hint", found)
                    self.assertEqual(calls, 1)  # the ordinary answer pays for no second search

    def test_a_shelf_fact_below_the_floor_is_named_behind_two_others(self):
        """The case of #546 on the real binary: two shelf facts pass the floor, the third, that the project is
        archived, sits under it, and the hint says one more fact is there."""
        for content, keywords in (("Quarrel queue library keeps jobs in Postgres, MIT license.", "quarrel,queue,postgres"),
                                  ("Quarrel queue library needs Postgres 14 or newer.", "quarrel,queue,postgres"),
                                  ("Quarrel: the project was archived by its author in 2025.", "quarrel")):
            json.loads(self.p.handle_tool_call("memcue_add", {"content": content, "keywords": keywords,
                                                              "shelf": "research"}))
        found = json.loads(self.p.handle_tool_call("memcue_search", {"query": "quarrel queue postgres",
                                                                     "in": "shelf:research"}))
        self.assertEqual(len(found["facts"]), 2)
        self.assertTrue(found.get("hint", "").startswith("1 more facts hold the words but sit below the score floor"),
                        found)
        wide = json.loads(self.p.handle_tool_call("memcue_search", {"query": "quarrel queue postgres",
                                                                    "in": "shelf:research", "min_score": "0"}))
        self.assertIn("archived", " ".join(f["content"] for f in wide["facts"]))

    def test_the_search_description_says_what_a_hint_is(self):
        """The model reads a hint as a note about the search, not as a fact, and knows below_floor (#546)."""
        search = memcue_provider.SEARCH_SCHEMA["description"]
        self.assertIn("not a stored fact", search)
        self.assertIn("below_floor: true", search)
        self.assertIn("shelf search whatever the number of facts", search)

    def test_an_empty_answer_hands_over_the_first_facts_below_the_floor(self):
        """A count leaves the model nothing to read: when the floor leaves no fact and the wide arm has some, the first
        ones come with the answer, marked, and the hint says what they are (#439)."""
        stub = Path(self.home) / "empty-memcue"
        stub.write_text("#!/bin/sh\n"
                        "case \"$1\" in\n"
                        "  version) printf '{\"version\":\"dev\"}\\n' ;;\n"
                        "  search)\n"
                        "    case \"$*\" in\n"
                        "      *-min-score*0*) printf '[{\"id\":\"t1\",\"content\":\"tail one\"},"
                        "{\"id\":\"t2\",\"content\":\"tail two\"}]\\n' ;;\n"
                        "      *) printf '[]\\n' ;;\n"
                        "    esac ;;\n"
                        "esac\nexit 0\n", encoding="utf-8")
        stub.chmod(0o755)
        p = memcue_provider.MemcueMemoryProvider({"binary": str(stub)})
        p.initialize("s-empty", hermes_home=self.home, agent_context="primary")
        found = json.loads(p.handle_tool_call("memcue_search", {"query": "caddy"}))
        self.assertEqual([f["id"] for f in found["facts"]], ["t1", "t2"])
        self.assertTrue(all(f.get("below_floor") is True for f in found["facts"]), found["facts"])
        self.assertIn("2 facts hold the words but sit below the score floor", found["hint"])
        self.assertIn("below_floor: true", found["hint"])

    def test_search_defaults_to_the_configured_number(self):
        """Five facts by default and a config key overrides it (#372): ten were noise on a live base."""
        for i in range(7):
            json.loads(self.p.handle_tool_call("memcue_add", {
                "content": f"Сервер server{i}: Caddy перечитывает Caddyfile после пересоздания контейнера.",
                "keywords": "caddy"}))
        found = json.loads(self.p.handle_tool_call("memcue_search", {"query": "caddy пересоздание"}))
        self.assertEqual(len(found["facts"]), memcue_provider._SEARCH_FACTS)
        self.assertEqual(memcue_provider._SEARCH_FACTS, 5)
        # Raising the limit adds the tail, not better facts (#425): the schema names the default and that cost.
        limit = memcue_provider.SEARCH_SCHEMA["parameters"]["properties"]["limit"]["description"]
        self.assertIn("default 5", limit)
        self.assertIn("adds the tail", limit)
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
                p = memcue_provider.MemcueMemoryProvider({"binary": str(script)})
                kwargs = {"hermes_home": self.home, "agent_workspace": "hermes", "agent_context": "primary"}
                if cwd is not None:
                    kwargs["cwd"] = cwd
                p.initialize("s1", **kwargs)
                # The fact the model stores names the session's project as its origin, the way search scopes by it.
                self.assertNotIn("error", json.loads(p.handle_tool_call("memcue_add", {"content": "x"})))
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

    def test_host_config_is_forwarded_to_memcue(self):
        script = Path(self.home) / "host.sh"
        script.write_text("#!/bin/sh\nprintf '%s' \"$MEMCUE_HOST\"\n", encoding="utf-8")
        script.chmod(0o755)
        p = memcue_provider.MemcueMemoryProvider({"binary": str(script), "host": "mac"})
        p.initialize("s1", hermes_home=self.home, agent_context="primary")
        ok, out = p._run([])
        self.assertTrue(ok)
        self.assertEqual(out, "mac")

    def test_session_card_chars_is_forwarded_to_memcue(self):
        # The binary places the card's lines (#487): the plugin passes the budget on and cuts nothing itself.
        script = Path(self.home) / "budget.sh"
        script.write_text("#!/bin/sh\nprintf '%s' \"${MEMCUE_CARD_BUDGET-unset}\"\n", encoding="utf-8")
        script.chmod(0o755)
        env = dict(os.environ)
        env.pop("MEMCUE_CARD_BUDGET", None)
        with unittest.mock.patch.dict(os.environ, env, clear=True):
            for value, want in [("1200", "1200"), (None, "unset"), ("0", "unset"), ("-5", "unset"), ("x", "unset")]:
                config = {"binary": str(script)}
                if value is not None:
                    config["session_card_chars"] = value
                p = memcue_provider.MemcueMemoryProvider(config)
                p.initialize("s1", hermes_home=self.home, agent_context="primary")
                ok, out = p._run([])
                self.assertTrue(ok)
                self.assertEqual(out, want, value)

    def test_endpoint_config_is_forwarded_to_memcue(self):
        """The endpoint of a background dream (#569): the URL and the model as configured, the key from the named
        environment variable, and dream: false turns the background dream off."""
        script = Path(self.home) / "endpoint.sh"
        script.write_text("#!/bin/sh\nprintf '%s|%s|%s|%s' \"${MEMCUE_API_URL-unset}\" \"${MEMCUE_MODEL-unset}\" "
                          "\"${MEMCUE_API_KEY-unset}\" \"${MEMCUE_DREAM_BACKGROUND-unset}\"\n", encoding="utf-8")
        script.chmod(0o755)
        env = {k: v for k, v in os.environ.items() if not k.startswith("MEMCUE_")}
        env["DEMO_DREAM_KEY"] = "sk-demo"
        with unittest.mock.patch.dict(os.environ, env, clear=True):
            for config, want in [
                ({"api_url": "https://api.example.test", "model": "demo-flash", "api_key_env": "DEMO_DREAM_KEY"},
                 "https://api.example.test|demo-flash|sk-demo|unset"),
                ({}, "unset|unset|unset|unset"),
                ({"dream": False}, "unset|unset|unset|0"),
            ]:
                p = memcue_provider.MemcueMemoryProvider(dict(config, binary=str(script)))
                p.initialize("s1", hermes_home=self.home, agent_context="primary")
                ok, out = p._run([])
                self.assertTrue(ok)
                self.assertEqual(out, want, config)

    def test_missing_binary_is_unavailable(self):
        p = memcue_provider.MemcueMemoryProvider({"binary": "/nonexistent/memcue"})
        self.assertFalse(p.is_available())


class NoteLabelsTest(unittest.TestCase):
    def test_labels_of_a_mirrored_note(self):
        """A mirrored note gets a kind from its wording and a scope from the host it names (#488)."""
        cases = (
            ("user", "Пользователь пишет по-русски.", "builder", "rule", "global"),  # a stated preference
            ("memory", "На builder Caddy слушает порт 8443.", "builder", "environment", "host:builder"),
            ("memory", "BUILDER: docker compose up fails without .env", "builder", "pitfall", "host:builder"),
            ("memory", "Никогда не пушить в main напрямую.", "builder", "rule", ""),  # a prohibition
            ("memory", "Сначала git fetch, потом rebase.", "builder", "rule", ""),  # a required order
            ("memory", "Сборка падает на Go 1.21 без CGO.", "builder", "pitfall", ""),  # a failure
            ("memory", "Хук pre-commit не работает в worktree.", "builder", "pitfall", ""),  # failure, not "не "
            ("memory", "Проект demo: база PostgreSQL 18 слушает порт 55432.", "builder", "environment", ""),
            ("memory", "На omega стоит headscale.", "builder", "environment", ""),  # names no host of this provider
            ("memory", "Сервер buildernet в другой сети.", "builder", "environment", ""),  # the host inside a longer word
            ("memory", "На builder Caddy слушает порт 8443.", "", "environment", ""),  # no host configured
            ("memory", "Мне ближе commonly used tooling.", "builder", "environment", ""),  # markers inside words
        )
        for target, content, host, kind, scope in cases:
            with self.subTest(content=content, host=host):
                p = memcue_provider.MemcueMemoryProvider({"host": host})
                self.assertEqual(p._note_labels(target, content), (kind, scope))


class BuiltinMemoryFlagsTest(unittest.TestCase):
    """The flags of the built-in memory come from Hermes, and no test reads them from the host (#504)."""

    def test_the_flags_are_those_hermes_reads(self):
        for flags in ((True, True), (True, False), (False, True), (False, False)):
            memory_tool = types.ModuleType("tools.memory_tool")
            memory_tool.get_builtin_memory_store_flags = lambda flags=flags: flags
            with self.subTest(flags=flags), unittest.mock.patch.dict(sys.modules, {"tools.memory_tool": memory_tool}):
                self.assertEqual(_BUILTIN_MEMORY_FLAGS(), flags)

    def test_both_flags_are_on_when_hermes_cannot_tell(self):
        def broken():
            raise RuntimeError("config.yaml is unreadable")

        failing = types.ModuleType("tools.memory_tool")
        failing.get_builtin_memory_store_flags = broken
        for name, module in (("no memory_tool", None), ("a failing reader", failing)):
            with self.subTest(name), unittest.mock.patch.dict(sys.modules, {"tools.memory_tool": module}):
                self.assertEqual(_BUILTIN_MEMORY_FLAGS(), (True, True))

    @unittest.skipUnless(BIN, "memcue binary not found (set MEMCUE_BIN)")
    def test_a_provider_test_pins_the_flags_and_gives_them_back(self):
        """A provider test leaves the flags, the environment and no home behind, a setUp that fails halfway too (#504):
        XDG_STATE_HOME pointed at the deleted home of the last test, and a variable that was missing came back set."""
        env = unittest.mock.patch.dict(os.environ)
        env.start()
        self.addCleanup(env.stop)
        hermes_reader = memcue_provider._builtin_memory_flags
        for xdg in ("/srv/state-of-the-host", None):
            for fails in (False, True):
                with self.subTest(xdg=xdg, fails=fails):
                    os.environ.pop("XDG_STATE_HOME", None)
                    if xdg is not None:
                        os.environ["XDG_STATE_HOME"] = xdg
                    os.environ["MEMCUE_RECALL_TEST"] = "of the shell"
                    before = dict(os.environ)
                    test = ProviderTest("test_base_lives_in_hermes_home")
                    initialize = unittest.mock.patch.object(memcue_provider.MemcueMemoryProvider, "initialize",
                                                            side_effect=RuntimeError("setUp failed")) \
                        if fails else contextlib.nullcontext()
                    try:
                        with initialize:
                            if fails:
                                with self.assertRaisesRegex(RuntimeError, "setUp failed"):
                                    test.setUp()
                            else:
                                test.setUp()
                        self.assertIsNot(memcue_provider._builtin_memory_flags, hermes_reader)
                        self.assertEqual(memcue_provider._builtin_memory_flags(), (True, True))
                        self.assertEqual(os.environ["XDG_STATE_HOME"], str(Path(test.home) / "state"))
                        self.assertNotIn("MEMCUE_RECALL_TEST", os.environ)
                    finally:
                        test.doCleanups()
                    self.assertIs(memcue_provider._builtin_memory_flags, hermes_reader)
                    self.assertIs(memcue_provider._builtin_memory_flags, _BUILTIN_MEMORY_FLAGS)
                    self.assertEqual(dict(os.environ), before)
                    self.assertEqual(os.environ.get("XDG_STATE_HOME"), xdg)
                    self.assertFalse(Path(test.home).exists())


TRUE = shutil.which("true") or "/bin/true"  # /usr/bin/true on macOS


class ManifestTest(unittest.TestCase):
    """The provider finds and installs the binary itself, so Hermes learns whether it is ready from is_available()."""

    def test_the_manifest_declares_no_external_dependencies(self):
        # The dashboard runs `check` with its own PATH and `install` through bash: a binary set in
        # memory.memcue.binary failed the check and marked a working provider unavailable (#536).
        text = (HERE / "plugin.yaml").read_text(encoding="utf-8")
        self.assertNotRegex(text, r"(?m)^external_dependencies\s*:")
        self.assertTrue(memcue_provider._plugin_version())

    def test_a_configured_binary_off_path_is_available(self):
        with tempfile.TemporaryDirectory() as root, unittest.mock.patch.dict(
                os.environ, {"PATH": "/usr/bin:/bin", "MEMCUE_AUTO_INSTALL": "0", "HERMES_HOME": root}):
            binary = Path(root) / "opt" / "bin" / "memcue"
            binary.parent.mkdir(parents=True)
            binary.write_text("#!/bin/sh\nexit 0\n")
            binary.chmod(0o755)
            self.assertIsNone(shutil.which("memcue"))
            self.assertTrue(memcue_provider.MemcueMemoryProvider({"binary": str(binary)}).is_available())
            self.assertFalse(memcue_provider.MemcueMemoryProvider({}).is_available())


@unittest.skipUnless(memcue_provider._release_target(), "no memcue release for this machine")
class InstallTest(unittest.TestCase):
    """The provider installs the binary of its release when none is configured; a local server stands in for GitHub."""

    def setUp(self):
        self.root = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, self.root, True)
        _pin_builtin_memory(self)
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