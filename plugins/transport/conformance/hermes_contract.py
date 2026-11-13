"""The Python host of the shared contract (#754): the Hermes provider's receipt check, owned-process kill and
release install answer the same decisions as the Node transport and the shell wrapper.

Run with the pinned Hermes interpreter (the provider imports the real abstract API):
    $(HERMES_PYTHON) -I plugins/transport/conformance/hermes_contract.py
"""
import hashlib
import importlib.util
import json
import os
import platform
import subprocess
import sys
import tarfile
import tempfile
import time
import unittest
from pathlib import Path
from unittest.mock import patch

PLUGINS = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("memcue_conformance_hermes", PLUGINS / "hermes" / "memcue" / "__init__.py")
m = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(m)

NONCE = "f" * 32


def receipt(out: str, err: str, status: str = "success", **changes) -> str:
    frame = {
        "version": 1, "nonce": NONCE, "status": status,
        "bytes": len(out.encode("utf-8")), "sha256": hashlib.sha256(out.encode("utf-8")).hexdigest(),
        "error_bytes": len(err.encode("utf-8")), "error_sha256": hashlib.sha256(err.encode("utf-8")).hexdigest(),
    }
    frame.update(changes)
    return err + "\nMEMCUE_OUTCOME_V1 " + json.dumps(frame) + "\n"


class ReceiptContract(unittest.TestCase):
    def test_complete_success_and_refusal(self):
        self.assertEqual(m.classify_outcome("01TEST\n", receipt("01TEST\n", ""), 0, NONCE), ("success", ""))
        self.assertEqual(m.classify_outcome("", receipt("", "memcue: fact changed\n", "refused"), 1, NONCE),
                         ("refused", "memcue: fact changed"))

    def test_every_malformed_variant_is_unknown(self):
        out, err = "01TEST\n", "diagnostic\n"
        variants = {
            "nonce": {"nonce": "0" * 32},
            "version": {"version": 2},
            "stdout_hash": {"sha256": "0" * 64},
            "stdout_bytes": {"bytes": 1},
            "stderr_hash": {"error_sha256": "0" * 64},
            "stderr_bytes": {"error_bytes": 1},
            "status": {"status": "unexpected"},
            "unknown_status": {"status": "unknown"},
        }
        for name, changes in variants.items():
            with self.subTest(variant=name):
                self.assertEqual(m.classify_outcome(out, receipt(out, err, **changes), 0, NONCE)[0], "unknown")
        # A refusal with a corrupted diagnostics hash cannot prove the refusal: unknown, as in the Node transport.
        self.assertEqual(m.classify_outcome(out, receipt(out, err, "refused", error_sha256="0" * 64), 1, NONCE)[0],
                         "unknown")
        self.assertEqual(m.classify_outcome(out, receipt(out, err, "refused"), 0, NONCE)[0], "unknown")
        self.assertEqual(m.classify_outcome(out, receipt(out, err, "success"), 1, NONCE)[0], "unknown")
        self.assertEqual(m.classify_outcome(out, receipt(out, err)[:-1], 0, NONCE)[0], "unknown", "no trailing newline")
        self.assertEqual(m.classify_outcome(out, "", 0, NONCE)[0], "unknown", "missing marker")
        self.assertEqual(m.classify_outcome(out, "\nMEMCUE_OUTCOME_V1 {broken}\n", 0, NONCE)[0], "unknown")
        for frame in ("null", "[]", "{}"):
            self.assertEqual(m.classify_outcome(out, f"\nMEMCUE_OUTCOME_V1 {frame}\n", 0, NONCE)[0], "unknown")


class OwnedProcessContract(unittest.TestCase):
    @unittest.skipUnless(os.name == "posix", "POSIX process groups")
    def test_timeout_kills_the_invocation_group(self):
        with tempfile.TemporaryDirectory(prefix="memcue-conformance-") as tmp:
            pid_file = Path(tmp) / "pid"
            marker = Path(tmp) / "ran"
            binary = Path(tmp) / "memcue"
            binary.write_text(
                "#!" + sys.executable + "\n"
                "import os, pathlib, time\n"
                f"if os.fork() == 0:\n"
                f"    pathlib.Path({str(pid_file)!r}).write_text(str(os.getpid()))\n"
                f"    time.sleep(10)\n"
                f"    pathlib.Path({str(marker)!r}).write_text('survived')\n"
                f"    os._exit(0)\n"
                "time.sleep(10)\n")
            binary.chmod(0o700)
            with self.assertRaises(subprocess.TimeoutExpired):
                m.subprocess.run([str(binary)], capture_output=True, timeout=0.3)
            pid = int(pid_file.read_text())
            deadline = time.monotonic() + 3
            while time.monotonic() < deadline:
                try:
                    os.kill(pid, 0)
                    time.sleep(0.05)
                except (ProcessLookupError, PermissionError):
                    break
            else:
                self.fail("the owned descendant survived the timeout")
            self.assertFalse(marker.exists(), "the descendant ran to completion")


class RunContract(unittest.TestCase):
    def provider(self, binary: str):
        return m.MemcueMemoryProvider({"binary": binary, "facts": "", "dream": False})

    def test_an_unproved_mutation_is_unknown_and_not_retried(self):
        with tempfile.TemporaryDirectory(prefix="memcue-conformance-") as tmp:
            binary = Path(tmp) / "memcue"
            binary.write_text("#!/bin/sh\necho 'memcue: lost the receipt' >&2\nexit 1\n")
            binary.chmod(0o700)
            ok, out = self.provider(str(binary))._run(["add", "-source", "user", "--", "a fact"])
            self.assertFalse(ok)
            self.assertIn("UNKNOWN OUTCOME", out)
            self.assertIn("history", out)
            self.assertRegex(out, r"not.*replay|do not.*retry")

    def test_a_complete_receipt_commits(self):
        with tempfile.TemporaryDirectory(prefix="memcue-conformance-") as tmp:
            binary = Path(tmp) / "memcue"
            binary.write_text(
                "#!" + sys.executable + "\n"
                "import hashlib, json, os, sys\n"
                "out = '01TEST\\n'\n"
                "nonce = os.environ.get('MEMCUE_OUTCOME_NONCE')\n"
                "if nonce:\n"
                "    frame = {'version': 1, 'nonce': nonce, 'status': 'success', 'bytes': len(out.encode()),\n"
                "             'sha256': hashlib.sha256(out.encode()).hexdigest(), 'error_bytes': 0,\n"
                "             'error_sha256': hashlib.sha256(b'').hexdigest()}\n"
                "    sys.stderr.write('\\nMEMCUE_OUTCOME_V1 ' + json.dumps(frame) + '\\n')\n"
                "sys.stdout.write(out)\n")
            binary.chmod(0o700)
            ok, out = self.provider(str(binary))._run(["add", "-source", "user", "--", "a fact"])
            self.assertTrue(ok, out)
            self.assertEqual(out, "01TEST")

    def test_a_missing_binary_is_not_an_unknown_mutation(self):
        ok, out = self.provider("/nonexistent/memcue-conformance")._run(["add", "--", "a fact"])
        self.assertFalse(ok)
        self.assertNotIn("UNKNOWN OUTCOME", out)


class InstallContract(unittest.TestCase):
    """The Hermes provider installs into its own home, but the shared decisions are the same: checksum, lock,
    stale lock, rename over a running binary, and the MEMCUE_AUTO_INSTALL=0 opt-out."""

    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory(prefix="memcue-conformance-install-")
        self.root = Path(self._tmp.name)
        self.version = "9.9.9"
        self.target = m._release_target()
        self.archive = f"memcue_{self.version}_{self.target}.tar.gz"
        self.release_dir = self.root / "release" / f"v{self.version}"
        self.release_dir.mkdir(parents=True)
        self.pack("#!/bin/sh\nprintf '{\"version\":\"9.9.9\"}\\n'\n")
        self.env = patch.dict(os.environ, {"MEMCUE_RELEASE_URL": self.release_dir.parent.as_uri(),
                                           "MEMCUE_AUTO_INSTALL": ""})
        self.env.start()

    def tearDown(self):
        self.env.stop()
        self._tmp.cleanup()

    def pack(self, script: str, bad_checksum: bool = False):
        build = self.root / "build"
        build.mkdir(exist_ok=True)
        (build / "memcue").write_text(script)
        (build / "memcue").chmod(0o755)
        with tarfile.open(self.release_dir / self.archive, "w:gz") as tar:
            tar.add(build / "memcue", arcname="memcue")
        data = (self.release_dir / self.archive).read_bytes()
        digest = hashlib.sha256(data).hexdigest()
        (self.release_dir / "checksums.txt").write_text(f"{'0' * 64 if bad_checksum else digest}  {self.archive}\n")

    @unittest.skipUnless(m._release_target(), "no release for this platform")
    def test_install_checks_the_checksum_and_renames_into_place(self):
        path = self.root / "bin" / "memcue"
        m._install_release(self.version, path)
        self.assertTrue(os.access(path, os.X_OK))
        self.assertIn("9.9.9", subprocess.run([str(path)], capture_output=True, text=True, check=True).stdout)
        self.assertFalse((path.parent / ".lock").exists(), "the lock must be released")

    @unittest.skipUnless(m._release_target(), "no release for this platform")
    def test_a_bad_checksum_installs_nothing_and_releases_the_lock(self):
        self.pack("#!/bin/sh\nexit 0\n", bad_checksum=True)
        path = self.root / "bin" / "memcue"
        with self.assertRaisesRegex(RuntimeError, "checksum"):
            m._install_release(self.version, path)
        self.assertFalse(path.exists())
        self.assertFalse((path.parent / ".lock").exists())

    @unittest.skipUnless(m._release_target(), "no release for this platform")
    def test_a_fresh_lock_blocks_and_a_stale_lock_is_taken_over(self):
        path = self.root / "bin" / "memcue"
        path.parent.mkdir(parents=True)
        lock = path.parent / ".lock"
        lock.mkdir()
        with self.assertRaisesRegex(RuntimeError, "another install"):
            m._install_release(self.version, path)
        old = time.time() - 11 * 60
        os.utime(lock, (old, old))
        m._install_release(self.version, path)
        self.assertTrue(path.exists())

    def test_the_opt_out_is_read_from_the_environment(self):
        with patch.dict(os.environ, {"MEMCUE_AUTO_INSTALL": "0"}):
            self.assertFalse(m._auto_install())
        with patch.dict(os.environ, {"MEMCUE_AUTO_INSTALL": ""}):
            self.assertEqual(m._auto_install(), m._release_target() is not None)

    @unittest.skipUnless(m._release_target() and os.name == "posix", "POSIX rename semantics")
    def test_the_rename_leaves_a_running_memcue_its_own_file(self):
        started, marker = self.root / "started", self.root / "ran"
        self.pack("#!/bin/sh\nif [ \"$1\" = version ]; then exit 1; fi\nprintf started > " + str(started)
                  + "\nsleep 1\nprintf ran > " + str(marker) + "\n")
        path = self.root / "bin" / "memcue"
        m._install_release(self.version, path)
        running = subprocess.Popen([str(path)], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        # Replace only once the old script is really executing: a process that has not exec'd yet picks up the new
        # file, and that is correct. The guarantee is about the running one.
        deadline = time.monotonic() + 5
        while not started.exists() and time.monotonic() < deadline:
            time.sleep(0.02)
        self.assertTrue(started.exists(), "the first binary never started")
        self.pack("#!/bin/sh\nprintf '{\"version\":\"9.9.9\"}\\n'\n")
        m._install_release(self.version, path)
        running.wait(timeout=10)
        self.assertTrue(marker.exists(), "the running binary lost its file")


if __name__ == "__main__":
    unittest.main()