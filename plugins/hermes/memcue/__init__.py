"""memcue memory provider for Hermes Agent.

Runs the ``memcue`` binary (github.com/juev/memcue): before each turn ``memcue hook`` returns at most two stored facts
that share enough rare words with the user's message, or nothing; each fact once per session (again after a
compression), and none for a cron turn; facts that only mirror a built-in memory note carry the origin
``hermes-memory`` and are left out, because that note rides with every turn anyway; every write of the built-in memory
tool is mirrored into the base (add → ``memcue add``, replace → ``memcue correct``, remove → ``memcue retire``); the
model gets tools to search and fix facts; a reply is checked for the identifiers of the facts it was given, and
``usage.log`` keeps that score when ``MEMCUE_USAGE_LOG=1``. With an OpenAI-compatible endpoint configured, the new
messages of a session go to ``memcue extract-messages`` in the background and the facts a model finds in them are
added: once enough new text gathers, after a pause in the conversation, before the context is compressed and when the
session ends. A chat of a messaging gateway can stay one session for days, so its end alone would come too late.

Config, all optional (``memory.memcue`` in config.yaml):
  binary: path to memcue (default: found on PATH)
  extract_url, extract_model, extract_key_env: endpoint, model and the environment variable holding its key for the
          session extraction (e.g. https://api.deepseek.com, deepseek-flash, DEEPSEEK_API_KEY); off while unset
  extract_threshold: characters of new messages that start an extraction (default 20000)
  extract_idle_minutes: minutes without a turn after which the new messages go (default 30)
  extract_include_tool_results: send tool output to the extraction too (default false: it is the bulk of a session's
          characters and rarely holds a durable fact)
  extract_max_message_chars: cut a message to this many characters before extraction (default 4000)
  min_coverage: share of the prompt's terms a fact must hold to be recalled (passed to memcue; default 0.15)
  suppress_mirrored: hide facts that only mirror the built-in memory notes, which are in the prompt anyway
          (default true; mirrored facts carry the origin ``hermes-memory``)
  facts:  the fact base (default: $MEMCUE_FACTS, else $HERMES_HOME/memcue/facts.db; share one with Claude Code by
          pointing both at it)
"""

from __future__ import annotations

import datetime
import hashlib
import json
import logging
import os
import re
import shutil
import subprocess
import threading
import time
from collections import Counter
from pathlib import Path
from typing import Any, Optional

from agent.memory_provider import MemoryProvider
from tools.registry import tool_error

logger = logging.getLogger(__name__)

_RECALL_TIMEOUT = 5  # memcue answers in ~10 ms; anything slower is broken
_WRITE_TIMEOUT = 15
_ORIGIN = "hermes"
_ORIGIN_MIRROR = "hermes-memory"  # a fact that only mirrors a built-in memory note; prefetch hides those
_THRESHOLD = 20000  # characters of new messages that make a run worth a model call
_IDLE_MINUTES = 30
_MIN_IDLE = 2000  # a pause sends no less; memcue skips smaller texts anyway
_TICK = 60  # seconds between checks for a pause
_KEEP_SEEN = 5000  # fingerprints of processed messages kept per session
_MAX_MESSAGE = 4000  # characters of one message the extraction sees
_WARN_EVERY = 600  # seconds between two "recall failed" warnings of a session
_IDENTIFIER = re.compile(r"[A-Za-z0-9_@./:-]{6,}")


def _fingerprint(m: dict[str, Any]) -> str:
    raw = json.dumps([m.get("role"), m.get("content"), m.get("tool_calls")], ensure_ascii=False, default=str,
                     sort_keys=True)
    return hashlib.sha256(raw.encode()).hexdigest()[:24]


def _size(m: dict[str, Any]) -> int:
    return len(json.dumps([m.get("content"), m.get("tool_calls")], ensure_ascii=False, default=str))


def _identifiers(text: str) -> list[str]:
    """Tokens of a fact that a reply would copy verbatim when it used the fact: paths, hashes, versions, ids. A date
    and an origin are not identifiers; the caller strips the ``(learnt: …)`` tail before asking."""
    out: list[str] = []
    for token in _IDENTIFIER.findall(text):
        if token in out:
            continue
        if any(c.isdigit() for c in token) or any(c in token for c in "/@_"):
            out.append(token)
    return out[:6]


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
    {"content": "The fact.", "keywords": "Comma-separated words someone would ask with, synonyms included.",
     "expires": "Optional last day the fact is worth recalling, YYYY-MM-DD (e.g. a one-off reminder); "
                "omit for a fact that stays true."},
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
        self._context = "primary"
        self._generation = 0  # grows with every compression: the facts of an earlier context may come again
        self._lock = threading.Lock()
        self._threshold = int(self._config.get("extract_threshold", _THRESHOLD))
        self._idle = float(self._config.get("extract_idle_minutes", _IDLE_MINUTES)) * 60
        self._tick = _TICK
        self._min_coverage = self._config.get("min_coverage")
        self._suppress_mirrored = bool(self._config.get("suppress_mirrored", True))
        self._include_tool_results = bool(self._config.get("extract_include_tool_results", False))
        self._max_message = int(self._config.get("extract_max_message_chars", _MAX_MESSAGE))
        self._pending: dict[str, tuple[list[dict[str, Any]], float]] = {}  # session -> new messages, last turn
        self._pending_lock = threading.Lock()
        self._delivered: dict[str, list[dict[str, Any]]] = {}  # session -> facts the last prefetch injected
        self._warned: dict[str, float] = {}  # session -> when its last recall failure was reported
        self._stop = threading.Event()
        self._watcher: Optional[threading.Thread] = None

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
        self._context = str(kwargs.get("agent_context") or "primary")

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
        if self._min_coverage is not None:
            env["MEMCUE_MIN_COVERAGE"] = str(self._min_coverage)
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

    def _scope(self, session: str) -> str:
        """The session memcue counts facts for: it gives a fact once per session. A gateway session can outlive
        several compressions, and the compressed context no longer holds the facts it already got, so the scope gets
        one generation per compression and those facts may come again, as they do in Claude Code. memcue takes a
        session id of [A-Za-z0-9._-] up to 128 characters; anything else is hashed."""
        if not session:
            return ""
        suffix = f".c{self._generation}" if self._generation else ""
        safe = re.sub(r"[^A-Za-z0-9._-]", "_", session)
        scope = safe + suffix
        if safe != session or scope in (".", "..") or len(scope) > 128:
            scope = hashlib.sha256(session.encode()).hexdigest()[:32] + suffix
        return scope

    def prefetch(self, query: str, *, session_id: str = "") -> str:
        if self._context == "cron":
            return ""  # a cron turn is a background task's notice: its wrapper matches facts, not a request of a user
        if not query or not query.strip() or not Path(self._facts).exists():
            return ""
        payload: dict[str, Any] = {"prompt": query, "cwd": self._workspace}
        scope = self._scope((session_id or self._session_id).strip())
        if scope:
            payload["session_id"] = scope
        ok, out = self._run(["hook"], stdin=json.dumps(payload), timeout=_RECALL_TIMEOUT)
        if not ok or not out:
            if not ok:
                self._warn_recall_failure(session_id or self._session_id, out)
            return ""
        try:
            text = json.loads(out)["hookSpecificOutput"]["additionalContext"].strip()
        except (ValueError, KeyError, TypeError):
            return ""
        kept = self._keep_useful_lines(text)
        self._remember_delivered(session_id or self._session_id, kept)
        return "\n".join(kept) + ("\n" if kept else "")

    def _warn_recall_failure(self, session: str, reason: str) -> None:
        """A failed recall is silent memory loss: say it once a session rather than on every turn."""
        now = time.monotonic()
        if now - self._warned.get(session, 0.0) >= _WARN_EVERY:
            self._warned[session] = now
            logger.warning("memcue recall failed (session %s): %s", session or "-", reason)

    def _keep_useful_lines(self, text: str) -> list[str]:
        """Drop facts that only repeat the built-in memory block: those notes ride with every turn anyway, and a
        gateway session mirrors them into the base (origin ``hermes-memory``), so recall would say them twice. Facts
        gone means the header goes too: an empty recall is no context at all."""
        lines = [line for line in text.splitlines() if line.strip()]
        if not lines:
            return []
        if self._suppress_mirrored:
            start = "(learnt: " + _ORIGIN_MIRROR
            lines = [line for line in lines if not line.lstrip().startswith("- ") or start not in line]
        return lines if any(line.lstrip().startswith("- ") for line in lines) else []

    def _remember_delivered(self, session: str, lines: list[str]) -> None:
        facts = []
        for line in lines:
            if not line.lstrip().startswith("- "):
                continue
            body = line.lstrip()[2:]
            body = re.sub(r"\s*\(learnt:[^)]*\)\s*$", "", body)
            tokens = _identifiers(body)
            if tokens:  # a fact without an identifier cannot be shown to be used
                facts.append({"tokens": tokens, "chars": len(body)})
        if facts:
            self._delivered[session] = facts
        else:
            self._delivered.pop(session, None)

    # -- session extraction -----------------------------------------------------

    def _extract_env(self) -> Optional[dict[str, str]]:
        url, model = str(self._config.get("extract_url") or ""), str(self._config.get("extract_model") or "")
        if not (url and model and self._writes):
            return None
        key = os.environ.get(str(self._config.get("extract_key_env") or ""), "")
        return dict(os.environ, MEMCUE_FACTS=self._facts, MEMCUE_API_URL=url, MEMCUE_MODEL=model, MEMCUE_API_KEY=key)

    def _extract_in_background(self, messages: list[dict[str, Any]], event: str, session: str = "") -> None:
        """Hand the messages to ``memcue extract-messages`` in its own process group and return: the model call takes
        a minute and must outlive neither the turn nor depend on it. The outcome goes to memcue's extract.log. The
        caller passes what ``_prepare`` kept."""
        env, binary = self._extract_env(), self._binary()
        if not env or not binary or not messages:
            return
        kept = messages
        if not kept:
            return
        data = json.dumps(kept, ensure_ascii=False, default=str).encode()
        cmd = [binary, "extract-messages", "-origin", self._origin(), "-session", session or self._session_id,
               "-event", event]
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

    # The messages of a session reach the model once each: a message is new until its fingerprint is in the
    # session's seen list ($HERMES_HOME/memcue/sessions/<session>.json). Compression rewrites the list Hermes holds,
    # so positions cannot tell what is new; fingerprints can.

    def _seen_path(self, session: str) -> Path:
        name = hashlib.sha256(session.encode()).hexdigest()[:32]  # a session id is not a safe file name
        return Path(self._state) / "sessions" / f"{name}.json"

    def _seen(self, session: str) -> list[str]:
        try:
            return json.loads(self._seen_path(session).read_text(encoding="utf-8"))
        except (OSError, ValueError):
            return []

    def _mark_seen(self, session: str, messages: list[dict[str, Any]]) -> None:
        seen = (self._seen(session) + [_fingerprint(m) for m in messages])[-_KEEP_SEEN:]
        path = self._seen_path(session)
        path.parent.mkdir(parents=True, exist_ok=True)
        tmp = path.with_suffix(".tmp")
        tmp.write_text(json.dumps(seen), encoding="utf-8")
        tmp.replace(path)

    def _new(self, session: str, messages: Optional[list[dict[str, Any]]]) -> list[dict[str, Any]]:
        """Messages not processed yet. Equal messages ("ok", "yes") share a fingerprint, so occurrences are counted:
        the third "ok" of a list is new when two were processed."""
        left = Counter(self._seen(session))
        new = []
        for m in messages or []:
            if not isinstance(m, dict) or m.get("role") == "system":
                continue
            fp = _fingerprint(m)
            if left[fp] > 0:
                left[fp] -= 1
            else:
                new.append(m)
        return new

    def _prepare(self, messages: list[dict[str, Any]]) -> list[dict[str, Any]]:
        """The messages an extraction sees: no system notes, no compaction summary, no tool output (the bulk of a
        session's characters, and rarely a durable fact of the user), each message cut to a readable length."""
        try:
            from agent.context_compressor import is_compaction_summary_message
        except ImportError:  # outside Hermes, or an older one
            def is_compaction_summary_message(_msg):
                return False
        kept: list[dict[str, Any]] = []
        for m in messages:
            if not isinstance(m, dict) or m.get("role") == "system" or is_compaction_summary_message(m):
                continue
            if m.get("role") == "tool" and not self._include_tool_results:
                continue
            content = m.get("content")
            if isinstance(content, str) and len(content) > self._max_message:
                m = dict(m, content=content[: self._max_message] + "…[truncated]")
            kept.append(m)
        return kept

    def _usage_path(self) -> Path:
        base = os.environ.get("XDG_STATE_HOME") or str(Path.home() / ".local" / "state")
        return Path(base) / "memcue" / "usage.log"

    def _note_usage(self, session: str, reply: str) -> None:
        """Judge the facts of the last prefetch by whether the reply copied one of their identifiers. It is a proxy:
        a fact can be read and not used, and a reply can use it without quoting a path. Off unless
        MEMCUE_USAGE_LOG=1, the way the recall log is off unless MEMCUE_RECALL_LOG=1."""
        facts = self._delivered.pop(session, None)
        if not facts:
            return
        matched = [t for f in facts for t in f["tokens"] if t in reply]
        if os.environ.get("MEMCUE_USAGE_LOG") != "1":
            return
        record = {"time": datetime.datetime.now(datetime.timezone.utc).replace(microsecond=0)
                  .isoformat().replace("+00:00", "Z"), "session": session, "facts": len(facts),
                  "used": sum(1 for f in facts if any(t in reply for t in f["tokens"])), "matched": matched[:6]}
        try:
            path = self._usage_path()
            path.parent.mkdir(parents=True, exist_ok=True)
            with self._lock:
                with path.open("a", encoding="utf-8") as f:
                    f.write(json.dumps(record, ensure_ascii=False) + "\n")
        except OSError as e:
            logger.debug("memcue usage log: %s", e)

    def _flush(self, session: str, event: str, messages: Optional[list[dict[str, Any]]] = None) -> None:
        """Send what is new in the session (in messages, or gathered by the turns) and mark it seen."""
        with self._pending_lock:
            gathered, _ = self._pending.pop(session, ([], 0.0))
            new = self._new(session, messages)
            if messages is not None and gathered:  # turns the given list may not hold
                have = Counter(_fingerprint(m) for m in messages if isinstance(m, dict))
                for m in gathered:
                    fp = _fingerprint(m)
                    if have[fp] > 0:
                        have[fp] -= 1
                    else:
                        new.append(m)
            elif messages is None:
                new = gathered
            if not new:
                return
            self._mark_seen(session, new)
        self._extract_in_background(self._prepare(new), event, session)

    def sync_turn(self, user_content: str, assistant_content: str, *, session_id: str = "",
                  messages: Optional[list[dict[str, Any]]] = None, **kwargs) -> None:
        """After each turn (Hermes runs it on its background worker): judge the facts of the last prefetch by the
        reply, then gather the session's new messages and send them once they hold enough text; a pause in the
        conversation sends a smaller batch (_watch)."""
        session = session_id or self._session_id
        self._note_usage(session, assistant_content or "")
        if self._extract_env() is None or not messages:
            return
        with self._pending_lock:
            new = self._new(session, messages)
            self._pending[session] = (new, time.monotonic())
        self._start_watcher()
        if sum(_size(m) for m in self._prepare(new)) >= self._threshold:
            self._flush(session, "threshold")

    def _start_watcher(self) -> None:
        if self._watcher is None or not self._watcher.is_alive():
            self._watcher = threading.Thread(target=self._watch, name="memcue-idle", daemon=True)
            self._watcher.start()

    def _watch(self) -> None:
        while not self._stop.wait(self._tick):
            now = time.monotonic()
            with self._pending_lock:
                due = [s for s, (new, last) in self._pending.items()
                       if now - last >= self._idle and sum(_size(m) for m in self._prepare(new)) >= _MIN_IDLE]
            for session in due:
                self._flush(session, "idle")

    def on_session_end(self, messages: list[dict[str, Any]]) -> None:
        if self._extract_env() is not None:
            self._flush(self._session_id, "session_end", messages)

    def on_pre_compress(self, messages: list[dict[str, Any]]) -> str:
        if self._extract_env() is not None:
            self._flush(self._session_id, "pre_compress", messages)
        self._generation += 1  # the context loses the facts it got: they may come once more
        return ""

    def shutdown(self) -> None:
        """Stop the pause watcher and send what the turns gathered, so a gateway restart loses nothing."""
        self._stop.set()
        with self._pending_lock:
            sessions = list(self._pending)
        for session in sessions:
            self._flush(session, "shutdown")

    def _origin(self, mirror: bool = False) -> str:
        """The origin of a fact: which producer wrote it and when. A mirrored built-in memory note says so, so recall
        can leave out what the memory block already carries."""
        name = _ORIGIN_MIRROR if mirror else _ORIGIN
        return f"{name} {datetime.date.today().isoformat()}"

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
                ok, out = self._run(["add", "-origin", self._origin(mirror=True), "--", content.strip()])
                if ok:
                    written[self._key(target, content)] = out.split()[0]
            elif action == "replace" and content.strip():
                if fact_id:
                    ok, out = self._run(["correct", "--", fact_id, content.strip()])
                    written.pop(self._key(target, previous), None)
                    if ok:
                        written[self._key(target, content)] = fact_id
                else:
                    ok, out = self._run(["add", "-origin", self._origin(mirror=True), "--", content.strip()])
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
            ok, out = self._run(["search", "-json", "--", arg("query")], timeout=_RECALL_TIMEOUT)
            if ok:
                try:
                    return json.dumps({"facts": json.loads(out)}, ensure_ascii=False)
                except ValueError:  # an older binary without -json prints text
                    return json.dumps({"result": out or "No stored facts match."}, ensure_ascii=False)
            return tool_error(out)
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
            if arg("expires"):
                cmd += ["-expires", arg("expires")]
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
