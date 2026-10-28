"""A stand-in web for the #531 stand: an MCP server over stdio with one tool, web_fetch, that serves fixtures/web.

https://<host>/<path> is fixtures/web/<host>/<path>, or <path>/index.md for a directory; anything else is a 404. It
reaches no network. Every call is appended to $STAND_WEB_LOG as a JSON line, so the record shows what was read.
"""

import json
import os
import sys
import time
from pathlib import Path
from urllib.parse import urlsplit

WEB = Path(os.environ.get("STAND_WEB_ROOT", "/opt/fixtures/web")).resolve()
LOG = os.environ.get("STAND_WEB_LOG", "")

TOOL = {
    "name": "web_fetch",
    "description": "Fetch a web page by its URL and return its text.",
    "inputSchema": {"type": "object", "properties": {"url": {"type": "string", "description": "The page URL."}},
                    "required": ["url"]},
}


def page(url: str) -> str | None:
    parts = urlsplit(url.strip())
    if parts.scheme not in ("http", "https") or not parts.hostname:
        return None
    path = (WEB / parts.hostname.lower() / parts.path.lstrip("/")).resolve()
    if WEB not in path.parents:  # no way out of the fixture tree, whatever the path holds
        return None
    if path.is_dir():
        path = path / "index.md"
    try:
        return path.read_text(encoding="utf-8")
    except (OSError, UnicodeDecodeError):
        return None


def log(url: str, found: bool) -> None:
    if LOG:
        with open(LOG, "a", encoding="utf-8") as f:
            f.write(json.dumps({"time": time.time(), "url": url, "found": found}) + "\n")


def reply(msg_id, result=None, error=None) -> None:
    out = {"jsonrpc": "2.0", "id": msg_id}
    if error is not None:
        out["error"] = error
    else:
        out["result"] = result
    sys.stdout.write(json.dumps(out) + "\n")
    sys.stdout.flush()


def main() -> None:
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            msg = json.loads(line)
        except ValueError:
            continue
        method, msg_id = msg.get("method"), msg.get("id")
        if msg_id is None:  # a notification needs no answer
            continue
        if method == "initialize":
            version = (msg.get("params") or {}).get("protocolVersion") or "2025-06-18"
            reply(msg_id, {"protocolVersion": version, "capabilities": {"tools": {}},
                           "serverInfo": {"name": "web", "version": "1.0"}})
        elif method == "tools/list":
            reply(msg_id, {"tools": [TOOL]})
        elif method == "tools/call":
            params = msg.get("params") or {}
            url = str((params.get("arguments") or {}).get("url") or "")
            text = page(url) if params.get("name") == "web_fetch" else None
            log(url, text is not None)
            if text is None:
                reply(msg_id, {"content": [{"type": "text", "text": f"404 Not Found: {url}"}], "isError": True})
            else:
                reply(msg_id, {"content": [{"type": "text", "text": text}]})
        elif method == "ping":
            reply(msg_id, {})
        else:
            reply(msg_id, error={"code": -32601, "message": f"method not found: {method}"})


if __name__ == "__main__":
    main()
