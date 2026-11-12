"""memcue #742: one isolated `claude -p` call, the way internal/extract calls the model.

No tools, no MCP servers, no hooks, no session file and a temporary working directory, so the model sees only the
system prompt and the input it is given.
"""
import json
import subprocess
import tempfile


def claude(model, system, schema, text, timeout=600):
    with tempfile.TemporaryDirectory(prefix="paraphrase742-") as cwd:
        args = ["claude", "-p", "--model", model, "--tools", "", "--no-session-persistence",
                "--settings", '{"disableAllHooks": true}', "--mcp-config", '{"mcpServers":{}}',
                "--strict-mcp-config", "--system-prompt", system, "--json-schema", json.dumps(schema),
                "--output-format", "json"]
        r = subprocess.run(args, input=text, capture_output=True, text=True, cwd=cwd, timeout=timeout)
    env = json.loads(r.stdout) if r.stdout.strip().startswith("{") else {}
    if r.returncode != 0 or env.get("is_error"):
        raise RuntimeError(f"claude {model}: exit {r.returncode}: {env.get('result') or r.stderr.strip()[:300]}")
    payload = env.get("structured_output")
    if payload is None:
        payload = json.loads(env["result"])
    return payload, {"model": model, "cost_usd": env.get("total_cost_usd"), "duration_ms": env.get("duration_ms")}
