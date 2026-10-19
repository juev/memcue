"""Offline tests of the memcue provider against the real memcue binary.

Run: python3 plugins/hermes/memcue/tests/test_provider.py (MEMCUE_BIN=/path/to/memcue, default: memcue on PATH).
Inside Hermes the real agent modules are used; elsewhere they are stubbed.
"""

import http.server
import importlib.util
import json
import threading
import time
import os
import shutil
import subprocess
import sys
import tempfile
import types
import unittest
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


@unittest.skipUnless(BIN, "memcue binary not found (set MEMCUE_BIN)")
class ProviderTest(unittest.TestCase):
    def setUp(self):
        self.home = tempfile.mkdtemp()
        os.environ["XDG_STATE_HOME"] = str(Path(self.home) / "state")  # memcue's extract.log
        self.p = memcue_provider.MemcueMemoryProvider({"binary": BIN})
        self.p.initialize("s1", hermes_home=self.home, agent_context="primary")

    def tearDown(self):
        shutil.rmtree(self.home, ignore_errors=True)

    def facts(self):
        out = subprocess.run([BIN, "facts"], env=dict(os.environ, MEMCUE_FACTS=self.p._facts),
                             capture_output=True, text=True, check=True).stdout
        return [line for line in out.splitlines() if line]

    def test_base_lives_in_hermes_home(self):
        self.assertEqual(self.p._facts, str(Path(self.home) / "memcue" / "facts.db"))

    def test_no_base_recalls_nothing_and_creates_nothing(self):
        self.assertEqual(self.p.prefetch("какой порт у базы demo postgres"), "")
        self.assertFalse(Path(self.p._facts).exists())

    def test_mirrored_add_is_recalled(self):
        self.p.on_memory_write("add", "memory", "Проект demo: база PostgreSQL 18 слушает порт 55432, пользователь demo_app.")
        ctx = self.p.prefetch("на каком порту слушает база postgres проекта demo?")
        self.assertIn("55432", ctx)
        self.assertIn("learnt: hermes", ctx)

    def test_replace_corrects_the_same_fact_and_remove_retires_it(self):
        old = "Проект demo: база PostgreSQL 18 слушает порт 55432."
        new = "Проект demo: база PostgreSQL 18 слушает порт 55433."
        self.p.on_memory_write("add", "memory", old)
        self.p.on_memory_write("replace", "memory", new, {"previous_content": old})
        facts = self.facts()
        self.assertEqual(len(facts), 1)
        self.assertIn(" v2 ", facts[0])
        self.assertIn("55433", facts[0])
        self.p.on_memory_write("remove", "memory", "", {"previous_content": new})
        self.assertEqual(self.facts(), [])

    def test_replace_of_unknown_entry_adds(self):
        self.p.on_memory_write("replace", "user", "Пользователь пишет по-русски.", {"previous_content": "x"})
        self.assertEqual(len(self.facts()), 1)

    def test_content_starting_with_dash_is_stored(self):
        self.p.on_memory_write("add", "memory", "-v печатает версию memcue")
        self.assertIn("-v печатает", self.facts()[0])

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
        self.assertIn(fact_id, found["result"])
        corrected = json.loads(self.p.handle_tool_call("memcue_correct", {"id": fact_id, "content": "Новый текст."}))
        self.assertIn("v2", corrected["result"])
        json.loads(self.p.handle_tool_call("memcue_retire", {"id": fact_id}))
        self.assertEqual(self.facts(), [])
        self.assertIn("error", json.loads(self.p.handle_tool_call("memcue_add", {})))
        self.assertIn("error", json.loads(self.p.handle_tool_call("nope", {})))

    def test_session_end_extracts_through_the_endpoint(self):
        answer = json.dumps({"facts": [{"content": "Hermes на server1: модель по умолчанию deepseek-flash.",
                                        "keywords": ["модель", "model"]}], "dropped": ""}, ensure_ascii=False)
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
        p.initialize("sess-9", hermes_home=self.home, agent_context="primary")
        p.on_session_end([{"role": "system", "content": "prompt"},
                          {"role": "user", "content": "какая модель у hermes? " * 120},
                          {"role": "assistant", "content": "deepseek-flash"}])
        for _ in range(100):
            if Path(p._facts).exists() and self.facts_of(p):
                break
            time.sleep(0.1)
        facts = self.facts_of(p)
        self.assertEqual(len(facts), 1)
        self.assertIn("deepseek-flash", facts[0])
        self.assertIn(" hermes ", facts[0])
        self.assertEqual((seen["model"], seen["auth"]), ("m1", "Bearer k1"))
        self.assertNotIn("SYSTEM", seen["text"])

    def test_no_endpoint_no_extraction(self):
        self.p.on_session_end([{"role": "user", "content": "x " * 3000}])
        self.assertFalse(Path(self.p._facts).exists())

    def facts_of(self, p):
        out = subprocess.run([BIN, "facts"], env=dict(os.environ, MEMCUE_FACTS=p._facts),
                             capture_output=True, text=True).stdout
        return [line for line in out.splitlines() if line]

    def test_missing_binary_is_unavailable(self):
        p = memcue_provider.MemcueMemoryProvider({"binary": "/nonexistent/memcue"})
        self.assertFalse(p.is_available())


if __name__ == "__main__":
    unittest.main()
