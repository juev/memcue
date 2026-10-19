"""memcue memory provider for Hermes Agent.

Runs the ``memcue`` binary (github.com/juev/memcue): before each turn ``memcue hook`` returns at most two stored facts
that share enough rare words with the user's message, or nothing; every write of the built-in memory tool is mirrored
into the base (add → ``memcue add``, replace → ``memcue correct``, remove → ``memcue retire``); the model gets tools to
search and fix facts. With an OpenAI-compatible endpoint configured, the messages of a session are also sent to
``memcue extract-messages`` in the background when the session ends or is compressed, and the facts a model finds in
them are added.

Config, all optional (``memory.memcue`` in config.yaml):
  binary: path to memcue (default: found on PATH)
  extract_url, extract_model, extract_key_env: endpoint, model and the environment variable holding its key for the
          session extraction (e.g. https://api.deepseek.com, deepseek-flash, DEEPSEEK_API_KEY); off while unset
  facts:  the fact base (default: $MEMCUE_FACTS, else $HERMES_HOME/memcue/facts.db; share one with Claude Code by
          pointing both at it)
"""

from __future__ import annotations

import datetime
import hashlib
import json
import logging
import os
import shutil
import subprocess
import threading
from pathlib import Path
from typing import Any, Optional

from agent.memory_provider import MemoryProvider
from tools.registry import tool_error

logger = logging.getLogger(__name__)

_RECALL_TIMEOUT = 5  # memcue answers in ~10 ms; anything slower is broken
_WRITE_TIMEOUT = 15
_ORIGIN = "hermes"


def _load_plugin_config() -> dict[str, Any]:
    try:
        from hermes_cli.config import load_config
        block = (load_config().get("memory") or {}).get("memcue") or {}
    except Exception:
        return {}
    return dict(block) if isinstance(block, dict) else {}


def _schema(name: str, description: str, props: dict[str, str], required: list[str]) -> dict:
    return {"name": name, "description": description, "parameters": {
        "type": "object", "properties": {k: {"type": "string", "description": v} for k, v in props.items()},
        "required": required}}


SEARCH_SCHEMA = _schema(
    "memcue_search", "Search long-term memory (memcue) for stored facts; returns each fact with its ID. "
    "Use it to find a fact before correcting or retiring it.", {"query": "Words of the fact."}, ["query"])
ADD_SCHEMA = _schema(
    "memcue_add", "Store one fact in long-term memory: one self-contained statement in one to three sentences, "
    "naming the system or project, with exact values. Only what the user said or a tool confirmed.",
    {"content": "The fact.", "keywords": "Comma-separated words someone would ask with, synonyms included."},
    ["content"])
CORRECT_SCHEMA = _schema(
    "memcue_correct", "Replace a stored fact with a corrected version (the history keeps the old one).",
    {"id": "Fact ID from memcue_search.", "content": "The corrected fact."}, ["id", "content"])
RETIRE_SCHEMA = _schema(
    "memcue_retire", "Stop recalling a stored fact that no longer holds (the history keeps it).",
    {"id": "Fact ID from memcue_search."}, ["id"])


class MemcueMemoryProvider(MemoryProvider):
    """Long-term memory through the memcue binary."""

    def __init__(self, config: Optional[dict[str, Any]] = None):
        self._config = dict(config) if config is not None else _load_plugin_config()
        self._facts = str(self._config.get("facts") or os.environ.get("MEMCUE_FACTS") or "")
        self._state = ""
        self._workspace = ""
        self._session_id = ""
        self._writes = True
        self._lock = threading.Lock()

    @property
    def name(self) -> str:
        return "memcue"

    def _binary(self) -> Optional[str]:
        configured = str(self._config.get("binary") or "")
        if configured:
            return configured if os.access(configured, os.X_OK) else None
        return shutil.which("memcue")

    def is_available(self) -> bool:
        return self._binary() is not None

    def unavailable_reason(self) -> str:
        return "memcue binary not found: put it on PATH or set memory.memcue.binary"

    def initialize(self, session_id: str, **kwargs) -> None:
        home = Path(str(kwargs.get("hermes_home") or os.environ.get("HERMES_HOME") or Path.home() / ".hermes"))
        self._state = str(home / "memcue")
        Path(self._state).mkdir(parents=True, exist_ok=True)
        if not self._facts:
            self._facts = str(Path(self._state) / "facts.db")
        self._workspace = str(kwargs.get("agent_workspace") or "")
        self._session_id = session_id
        self._writes = kwargs.get("agent_context", "primary") == "primary"

    def system_prompt_block(self) -> str:
        return ("# Long-term memory (memcue)\n"
                "Stored facts that match a message arrive with it; they may be out of date and say when they were "
                "learnt. Your memory tool writes are copied into memcue. To fix a stored fact, find it with "
                "memcue_search, then memcue_correct or memcue_retire.")

    # -- memcue calls -------------------------------------------------------

    def _run(self, args: list[str], *, stdin: str = "", timeout: int = _WRITE_TIMEOUT) -> tuple[bool, str]:
        binary = self._binary()
        if not binary:
            return False, "memcue binary not found"
        env = dict(os.environ, MEMCUE_FACTS=self._facts, MEMCUE_EXTRACT="0")
        try:
            r = subprocess.run([binary] + args, input=stdin, capture_output=True, text=True, encoding="utf-8",
                               errors="replace", timeout=timeout, env=env, check=False)
        except subprocess.TimeoutExpired:
            return False, f"memcue timed out after {timeout}s"
        except OSError as e:
            return False, str(e)
        if r.returncode != 0:
            return False, (r.stderr or r.stdout).strip() or f"memcue exited {r.returncode}"
        return True, r.stdout.strip()

    def prefetch(self, query: str, *, session_id: str = "") -> str:
        if not query or not query.strip() or not Path(self._facts).exists():
            return ""
        ok, out = self._run(["hook"], stdin=json.dumps({"prompt": query, "cwd": self._workspace}),
                            timeout=_RECALL_TIMEOUT)
        if not ok or not out:
            if not ok:
                logger.debug("memcue recall failed: %s", out)
            return ""
        try:
            return json.loads(out)["hookSpecificOutput"]["additionalContext"].strip()
        except (ValueError, KeyError, TypeError):
            return ""

    # -- session extraction -----------------------------------------------------

    def _extract_env(self) -> Optional[dict[str, str]]:
        url, model = str(self._config.get("extract_url") or ""), str(self._config.get("extract_model") or "")
        if not (url and model and self._writes):
            return None
        key = os.environ.get(str(self._config.get("extract_key_env") or ""), "")
        return dict(os.environ, MEMCUE_FACTS=self._facts, MEMCUE_API_URL=url, MEMCUE_MODEL=model, MEMCUE_API_KEY=key)

    def _extract_in_background(self, messages: list[dict[str, Any]], event: str) -> None:
        """Hand the messages to ``memcue extract-messages`` in its own process group and return: the model call takes
        a minute and must outlive neither the turn nor depend on it. The outcome goes to memcue's extract.log."""
        env, binary = self._extract_env(), self._binary()
        if not env or not binary or not messages:
            return
        try:
            from agent.context_compressor import is_compaction_summary_message
        except ImportError:  # outside Hermes, or an older one
            def is_compaction_summary_message(_msg):
                return False
        kept = [m for m in messages if isinstance(m, dict) and not is_compaction_summary_message(m)]
        if not kept:
            return
        data = json.dumps(kept, ensure_ascii=False, default=str).encode()
        cmd = [binary, "extract-messages", "-origin", self._origin(), "-session", self._session_id, "-event", event]
        try:
            proc = subprocess.Popen(cmd, stdin=subprocess.PIPE, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                                    env=env, start_new_session=True)
        except OSError as e:
            logger.warning("memcue extraction did not start: %s", e)
            return

        def feed() -> None:
            try:
                proc.stdin.write(data)
                proc.stdin.close()
                proc.wait()
            except OSError as e:
                logger.warning("memcue extraction: %s", e)

        threading.Thread(target=feed, name="memcue-extract", daemon=True).start()

    def on_session_end(self, messages: list[dict[str, Any]]) -> None:
        self._extract_in_background(messages, "session_end")

    def on_pre_compress(self, messages: list[dict[str, Any]]) -> str:
        self._extract_in_background(messages, "pre_compress")
        return ""

    def _origin(self) -> str:
        return f"{_ORIGIN} {datetime.date.today().isoformat()}"

    # -- mirror of the built-in memory tool -----------------------------------

    def _written_path(self) -> Path:
        return Path(self._state) / "written.json"

    def _written(self) -> dict[str, str]:
        try:
            return json.loads(self._written_path().read_text(encoding="utf-8"))
        except (OSError, ValueError):
            return {}

    def _save_written(self, written: dict[str, str]) -> None:
        tmp = self._written_path().with_suffix(".tmp")
        tmp.write_text(json.dumps(written, ensure_ascii=False, indent=1), encoding="utf-8")
        tmp.replace(self._written_path())

    @staticmethod
    def _key(target: str, content: str) -> str:
        return hashlib.sha256(f"{target}\n{content.strip()}".encode()).hexdigest()

    def on_memory_write(self, action: str, target: str, content: str,
                        metadata: Optional[dict[str, Any]] = None) -> None:
        """Keep the base in step with the built-in memory. Which fact an entry became is remembered in
        $HERMES_HOME/memcue/written.json, so a later replace or remove of that entry reaches the same fact."""
        if not self._writes or not self._state:
            return
        previous = str((metadata or {}).get("previous_content") or "")
        with self._lock:
            written = self._written()
            fact_id = written.get(self._key(target, previous)) if previous else None
            if action == "add" and content.strip():
                ok, out = self._run(["add", "-origin", self._origin(), "--", content.strip()])
                if ok:
                    written[self._key(target, content)] = out.split()[0]
            elif action == "replace" and content.strip():
                if fact_id:
                    ok, out = self._run(["correct", "--", fact_id, content.strip()])
                    written.pop(self._key(target, previous), None)
                    if ok:
                        written[self._key(target, content)] = fact_id
                else:
                    ok, out = self._run(["add", "-origin", self._origin(), "--", content.strip()])
                    if ok:
                        written[self._key(target, content)] = out.split()[0]
            elif action == "remove" and fact_id:
                ok, out = self._run(["retire", "--", fact_id])
                written.pop(self._key(target, previous), None)
            else:
                return
            if not ok:
                logger.warning("memcue mirror of %s failed: %s", action, out)
            self._save_written(written)

    # -- tools ------------------------------------------------------------------

    def get_tool_schemas(self) -> list[dict[str, Any]]:
        if not self._writes:
            return [SEARCH_SCHEMA]
        return [SEARCH_SCHEMA, ADD_SCHEMA, CORRECT_SCHEMA, RETIRE_SCHEMA]

    def handle_tool_call(self, tool_name: str, args: dict[str, Any], **kwargs) -> str:
        def arg(name: str) -> str:
            return str(args.get(name) or "").strip()

        if tool_name == "memcue_search":
            if not arg("query"):
                return tool_error("query is required")
            ok, out = self._run(["search", "--", arg("query")], timeout=_RECALL_TIMEOUT)
            return json.dumps({"result": out or "No stored facts match."}) if ok else tool_error(out)
        if tool_name not in ("memcue_add", "memcue_correct", "memcue_retire"):
            return tool_error(f"Unknown tool: {tool_name}")
        if not self._writes:
            return tool_error("memory writes are off in this context")
        if tool_name == "memcue_add":
            if not arg("content"):
                return tool_error("content is required")
            cmd = ["add", "-origin", self._origin()]
            if arg("keywords"):
                cmd += ["-keywords", arg("keywords")]
            ok, out = self._run(cmd + ["--", arg("content")])
        elif tool_name == "memcue_correct":
            if not arg("id") or not arg("content"):
                return tool_error("id and content are required")
            ok, out = self._run(["correct", "--", arg("id"), arg("content")])
        else:
            if not arg("id"):
                return tool_error("id is required")
            ok, out = self._run(["retire", "--", arg("id")])
        return json.dumps({"result": out}) if ok else tool_error(out)

    def backup_paths(self) -> list[str]:
        """The base when it lives outside HERMES_HOME (a shared one); inside, the home backup holds it."""
        return [self._facts] if self._facts else []


def register(ctx) -> None:
    ctx.register_memory_provider(MemcueMemoryProvider())
