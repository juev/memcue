"""Invariants across the harness plugins of this repository.

Run: python3 plugins/test_cross_harness.py. It reads the plugins' files from the checkout only, so it lives outside any
one plugin: an installed Hermes plugin is plugins/hermes/memcue alone and has none of the others (#526). A missing file
fails the run.
"""

import unittest
from pathlib import Path

PLUGINS = Path(__file__).resolve().parent


class RecallDuringWorkTest(unittest.TestCase):
    def test_only_hermes_guides_recall_during_work(self):
        """#521 is the Hermes provider's instruction only: the DSH and Claude Code plugins do not carry it."""
        self.assertIn("During work", (PLUGINS / "hermes" / "memcue" / "__init__.py").read_text(encoding="utf-8"))
        for path in (PLUGINS / "deepseek-harness" / "src" / "index.mjs",
                     PLUGINS / "claude-code" / "skills" / "memcue" / "SKILL.md"):
            with self.subTest(path=path.name):
                text = path.read_text(encoding="utf-8")
                self.assertIn("memcue", text)
                self.assertNotIn("During work", text)
                self.assertNotIn("unexpected error", text)


class CaptureExclusionsTest(unittest.TestCase):
    def test_every_host_leaves_out_what_the_repository_shows(self):
        """#547: a document in the repository is what the repository shows, and every host tells its model not to store
        it. Hermes once left the repository out of the list and stored a pitfall its notes file already described."""
        for path in (PLUGINS / "hermes" / "memcue" / "__init__.py",
                     PLUGINS / "deepseek-harness" / "src" / "index.mjs",
                     PLUGINS / "claude-code" / "skills" / "memcue" / "SKILL.md",
                     PLUGINS.parent / "cmd" / "memcue" / "session.go"):
            with self.subTest(path=path.name):
                self.assertIn("what the repository", path.read_text(encoding="utf-8"))


if __name__ == "__main__":
    unittest.main()
