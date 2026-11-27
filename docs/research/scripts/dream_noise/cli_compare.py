"""Compare full native and experimental dream commands on the two synthetic fixtures."""

import argparse
from http.server import BaseHTTPRequestHandler, HTTPServer
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import threading
import time

from apply_holdout import snapshot
import experiment as base
import flash_holdout
import pipeline
import pipeline_native
import replay


def compare(cases, binary, key, out, previous, native_binary):
    facts = [fact for case in cases for fact in case["facts"]]
    expected = [action for case in cases for action in case["expected"]]
    budget = base.Budget(out / "requests.json")
    arm = "setup"

    class Proxy(BaseHTTPRequestHandler):
        def do_POST(self):
            size = int(self.headers["Content-Length"])
            if self.path != "/chat/completions" or size > 200_000:
                self.send_error(400)
                return
            request = json.loads(self.rfile.read(size))
            if request.get("model") != base.FLASH_MODEL:
                self.send_error(400)
                return
            try:
                reply = budget.ask(f"{arm}-{len(budget.calls) + 1}", request,
                                   min(request.get("max_tokens", 18_000), 18_000), key)
            except ValueError:
                self.send_error(429, "experiment budget reached")
                return
            if reply is None:
                self.send_error(502, "upstream model request failed")
                return
            body = json.dumps(reply).encode("utf-8")
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def log_message(self, format, *args):
            pass

    results = {}
    with tempfile.TemporaryDirectory(prefix="memcue-cli-fixture-") as directory, HTTPServer(("127.0.0.1", 0), Proxy) as proxy:
        start = Path(directory) / "base"
        start.mkdir()
        id_map = pipeline_native._import_fixture(facts, binary, start)
        before = snapshot(start, id_map)
        if previous:
            if previous["error"] or previous["before"] != before:
                raise ValueError("saved native control failed or used a different fixture")
            results["native"] = previous
        env = {"MEMCUE_API_URL": f"http://127.0.0.1:{proxy.server_port}", "MEMCUE_MODEL": base.FLASH_MODEL}
        thread = threading.Thread(target=proxy.serve_forever)
        thread.start()
        try:
            for arm, executable in (("native", native_binary), ("flash", binary)):
                if arm in results:
                    continue
                root = start.parent / arm
                shutil.copytree(start, root)
                state = root / "state"
                state.mkdir(exist_ok=True)
                (state / "dream.at").write_text("2000-01-01T00:00:00Z", encoding="utf-8")
                started = time.monotonic()
                try:
                    replay.native(executable, root, "dream", "-run", "-j", "1", model_env=env, timeout=360)
                    error = None
                except subprocess.CalledProcessError as err:
                    error = err.stderr.strip()
                log = state / "dream.log"
                record = json.loads(log.read_text().splitlines()[-1]) if log.is_file() else {}
                actions = [
                    {**action, "id": id_map[action["id"]], "by": id_map.get(action.get("by"), "")}
                    for action in record.get("actions", [])
                ]
                results[arm] = {
                    "error": error, "seconds": round(time.monotonic() - started, 2),
                    "actions": actions, "score": pipeline.evaluate(expected, actions),
                    "splits": record.get("splits", []), "scope_moves": record.get("rescoped", []),
                    "refused": record.get("refused", []), "tokens": record.get("tokens"), "calls": record.get("calls", []),
                    "before": before, "after": snapshot(root, id_map),
                }
                (out / "report.json").write_text(json.dumps(results, indent=2) + "\n", encoding="utf-8")
        finally:
            proxy.shutdown()
            thread.join()
    return results


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--out", type=Path, required=True)
    control = parser.add_mutually_exclusive_group(required=True)
    control.add_argument("--native-binary", type=Path, help="memcue binary from before the Flash default")
    control.add_argument("--reuse-native", type=Path, help="prior output directory on the same two fixtures")
    args = parser.parse_args()
    key = os.getenv("DEEPSEEK_API_KEY")
    if not key:
        parser.error("provide DEEPSEEK_API_KEY")
    binary = str(args.binary.resolve(strict=True))
    native_binary = str(args.native_binary.resolve(strict=True)) if args.native_binary else None
    base.MAX_USD = 0.05  # Per fixture across both commands.
    args.out.mkdir(mode=0o700, exist_ok=False)
    failed = False
    for name, path in (("original", base.HERE / "pipeline_fixture.json"), ("holdout", flash_holdout.FIXTURE)):
        out = args.out / name
        out.mkdir()
        previous = None
        if args.reuse_native:
            previous = json.loads((args.reuse_native / name / "report.json").read_text())["native"]
        results = compare(pipeline.read_cases(path), binary, key, out, previous, native_binary)
        failed |= any(arm["error"] for arm in results.values())
    return int(failed)


if __name__ == "__main__":
    raise SystemExit(main())
