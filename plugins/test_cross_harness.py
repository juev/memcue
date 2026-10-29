"""Invariants across the harness plugins of this repository.

Run: python3 plugins/test_cross_harness.py. It reads the plugins' files from the checkout only, so it lives outside any
one plugin: an installed Hermes plugin is plugins/hermes/memcue alone and has none of the others (#526). A missing file
fails the run.
"""

import ast
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


class ShelfTextTest(unittest.TestCase):
    """#567: every host carries the research-shelf text measured in #531 word for word, so the report still describes
    what ships. The Claude Code note is checked by a Go test against the same file."""

    def measured(self, name: str) -> str:
        path = PLUGINS.parent / "docs" / "research" / "scripts" / "confirm531" / "candidate" / name
        return path.read_text(encoding="utf-8").strip()

    def test_the_skill_carries_its_section(self):
        skill = (PLUGINS / "claude-code" / "skills" / "memcue" / "SKILL.md").read_text(encoding="utf-8")
        self.assertIn(self.measured("cc-skill.md"), skill)

    def test_hermes_and_dsh_carry_the_block(self):
        block = self.measured("hermes-block.md")
        source = (PLUGINS / "hermes" / "memcue" / "__init__.py").read_text(encoding="utf-8")
        assign = next(n for n in ast.parse(source).body if isinstance(n, ast.Assign)
                      and getattr(n.targets[0], "id", "") == "_SHELF_NOTE")
        self.assertEqual(ast.literal_eval(assign.value), block)
        self.assertIn("- " + block + "\n", (PLUGINS / "deepseek-harness" / "src" / "index.mjs").read_text(encoding="utf-8"))


class PitfallLineTest(unittest.TestCase):
    """#566: every host carries the pitfall line measured there word for word. The Claude Code note is checked by a Go
    test against the same file."""

    def measured(self, name: str) -> str:
        path = PLUGINS.parent / "docs" / "research" / "scripts" / "pitfall566" / "candidate" / name
        return path.read_text(encoding="utf-8").strip()

    def test_the_skill_carries_the_line(self):
        skill = (PLUGINS / "claude-code" / "skills" / "memcue" / "SKILL.md").read_text(encoding="utf-8")
        self.assertIn("\n\n" + self.measured("cc-skill.md") + "\n", skill)

    def test_hermes_and_dsh_carry_the_line(self):
        line = self.measured("hermes-block.md")
        source = (PLUGINS / "hermes" / "memcue" / "__init__.py").read_text(encoding="utf-8")
        assign = next(n for n in ast.parse(source).body if isinstance(n, ast.Assign)
                      and getattr(n.targets[0], "id", "") == "_FAILURE_NOTE")
        self.assertEqual(ast.literal_eval(assign.value), line)
        self.assertIn("- " + line + "\n", (PLUGINS / "deepseek-harness" / "src" / "index.mjs").read_text(encoding="utf-8"))


if __name__ == "__main__":
    unittest.main()
