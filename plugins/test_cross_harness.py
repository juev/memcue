"""Invariants across the harness plugins of this repository.

Run: python3 plugins/test_cross_harness.py. It reads the plugins' files from the checkout only, so it lives outside any
one plugin: an installed Hermes plugin is plugins/hermes/memcue alone and has none of the others (#526). A missing file
fails the run.
"""

import ast
import json
import os
import subprocess
import sys
import tempfile
import re
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


class HistoryFallbackTest(unittest.TestCase):
    def test_each_host_sends_an_empty_search_to_its_own_history(self):
        """#621: after an empty search, Claude Code runs memcue sessions over its transcripts, in the note and in the
        skill; Hermes has its own session_search; DSH reads its own journal through memcue_sessions (#630), in a line
        of its own beside the note every native host shares word for word."""
        go = (PLUGINS.parent / "cmd" / "memcue" / "session.go").read_text(encoding="utf-8")
        skill = (PLUGINS / "claude-code" / "skills" / "memcue" / "SKILL.md").read_text(encoding="utf-8")
        for text in (go, skill):
            self.assertIn("memcue sessions", text)
            self.assertIn("history, not a fact", text)
        hermes = (PLUGINS / "hermes" / "memcue" / "__init__.py").read_text(encoding="utf-8")
        self.assertIn("session_search", hermes)
        self.assertNotIn("memcue sessions", hermes)
        dsh = (PLUGINS / "deepseek-harness" / "src" / "index.mjs").read_text(encoding="utf-8")
        self.assertIn("memcue_sessions", dsh)
        self.assertNotIn("session_search", dsh)
        shared = re.search(r"const NOTE = `([^`]*)`", dsh)
        self.assertIsNotNone(shared)
        self.assertNotIn("memcue_sessions", shared.group(1))

    def test_the_history_fallback_names_no_host(self):
        """#764: the command reads every host's history, so the four texts that send an empty search there say "this
        machine's earlier sessions". A host list drifts with every reader added to internal/history, as it did in
        #753, #755 and #630."""
        texts = {
            "session.go": PLUGINS.parent / "cmd" / "memcue" / "session.go",
            "claude-code skill": PLUGINS / "claude-code" / "skills" / "memcue" / "SKILL.md",
            "QWEN.md.snippet": PLUGINS / "qwen-code" / "QWEN.md.snippet",
            "qwen-code skill": PLUGINS / "qwen-code" / "skills" / "memcue" / "SKILL.md",
        }
        for name, path in texts.items():
            with self.subTest(text=name):
                flat = " ".join(path.read_text(encoding="utf-8").split())
                marker = flat.index("with no index")
                sentence = flat[flat.rfind(". ", 0, marker) + 2:flat.find(". ", marker)]
                self.assertIn("this machine's earlier sessions", sentence)
                for host in ("Claude Code", "Qwen Code", "OpenCode", "DeepSeek Harness", "DSH"):
                    self.assertNotIn(host, sentence)


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
        return re.sub(r' -keywords "[^"\n]*"', "", path.read_text(encoding="utf-8").strip())

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
        return re.sub(r' -keywords "[^"\n]*"', "", path.read_text(encoding="utf-8").strip())

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



class CaptureReuseTest(unittest.TestCase):
    FILTER = (
        "A pitfall requires reproducible environmental, platform, tool, or repository behavior, "
        "a verified cause and fix or workaround, and relevance to a later session in a fresh checkout. "
        "Do not store typos or invocations resolved by the tool's own help; transient "
        "network/authentication or CI-runner state; a defect the current task just fixed; finished "
        "experiments, rejected candidates, release notes, or one-off observations; obsolete "
        "migration/install state; or unverified and context-free fragments. Design decisions, durable "
        "host configuration, and external research limitations remain capturable under their appropriate "
        "kind, scope, or shelf."
    )

    def test_every_capture_surface_requires_fresh_checkout_reuse(self):
        for path in (PLUGINS.parent / 'cmd' / 'memcue' / 'session.go',
                     PLUGINS / 'claude-code' / 'skills' / 'memcue' / 'SKILL.md',
                     PLUGINS / 'hermes' / 'memcue' / '__init__.py',
                     PLUGINS / 'deepseek-harness' / 'src' / 'index.mjs',
                     PLUGINS / 'pi' / 'src' / 'instructions.ts',
                     PLUGINS / 'opencode' / 'src' / 'instructions.ts',
                     PLUGINS / 'kimi-code' / 'skills' / 'memcue' / 'SKILL.md',
                     PLUGINS / 'kimi-code' / 'SYSTEM.md',
                     PLUGINS / 'codex' / 'skills' / 'memcue' / 'SKILL.md',
                     PLUGINS / 'codex' / 'policy.md',
                     PLUGINS / 'qwen-code' / 'skills' / 'memcue' / 'SKILL.md'):
            with self.subTest(host=path.name):
                flat = ' '.join(path.read_text(encoding='utf-8').split())
                self.assertIn(' '.join(self.FILTER.split()), flat)
        hermes = (PLUGINS / 'hermes' / 'memcue' / '__init__.py').read_text(encoding='utf-8')
        self.assertGreaterEqual(hermes.count('_PITFALL_FILTER_NOTE'), 2)

    def test_every_capture_surface_names_the_review_expiry(self):
        paths = (PLUGINS.parent / 'cmd' / 'memcue' / 'session.go',
                 PLUGINS / 'claude-code' / 'skills' / 'memcue' / 'SKILL.md',
                 PLUGINS / 'hermes' / 'memcue' / '__init__.py',
                 PLUGINS / 'deepseek-harness' / 'src' / 'index.mjs',
                 PLUGINS / 'pi' / 'src' / 'instructions.ts',
                 PLUGINS / 'opencode' / 'src' / 'instructions.ts',
                 PLUGINS / 'kimi-code' / 'skills' / 'memcue' / 'SKILL.md',
                 PLUGINS / 'kimi-code' / 'SYSTEM.md',
                 PLUGINS / 'codex' / 'skills' / 'memcue' / 'SKILL.md',
                 PLUGINS / 'codex' / 'policy.md',
                 PLUGINS / 'qwen-code' / 'skills' / 'memcue' / 'SKILL.md')
        for path in paths:
            with self.subTest(host=path.name):
                text = path.read_text(encoding='utf-8')
                self.assertIn('expire after 30 days', text)
                self.assertIn('deliberately durable knowledge', text)
        for path in (PLUGINS / 'pi' / 'src' / 'tools.ts', PLUGINS / 'opencode' / 'src' / 'tools.ts'):
            self.assertIn('noExpires', path.read_text(encoding='utf-8'))
        for path in (PLUGINS / 'hermes' / 'memcue' / '__init__.py', PLUGINS / 'deepseek-harness' / 'src' / 'index.mjs'):
            self.assertIn('no_expires', path.read_text(encoding='utf-8'))

    def test_native_add_descriptions_reject_one_off_pitfalls(self):
        constraint = (
            'For pitfalls, require a verified cause and fix plus fresh-checkout reuse; '
            'exclude one-off and already resolved incidents.'
        )
        for path in (PLUGINS / 'hermes' / 'memcue' / '__init__.py',
                     PLUGINS / 'deepseek-harness' / 'src' / 'index.mjs',
                     PLUGINS / 'pi' / 'src' / 'tools.ts',
                     PLUGINS / 'opencode' / 'src' / 'tools.ts'):
            with self.subTest(host=path.name):
                flat = ' '.join(path.read_text(encoding='utf-8').split())
                self.assertIn(constraint, flat)


class NoCardTest(unittest.TestCase):
    def test_every_host_searches_the_rules_instead_of_a_card(self):
        """#656: no host gives a card; every host tells the model to look up user rules and decisions.

        Hermes makes the lookup conditional (#787); the other hosts retain the original #647 instructions.
        """
        go = (PLUGINS.parent / "cmd" / "memcue" / "session.go").read_text(encoding="utf-8")
        hermes = (PLUGINS / "hermes" / "memcue" / "__init__.py").read_text(encoding="utf-8")
        hermes = re.sub(r'"\s*\n\s*"', "", hermes)  # the note is adjacent string literals; read it joined
        dsh = (PLUGINS / "deepseek-harness" / "src" / "index.mjs").read_text(encoding="utf-8")
        self.assertIn("-kind rule,decision \\\"<words of the task>\\\"", go)
        self.assertIn("with kind rule,decision and the words of the task", dsh)
        self.assertIn("search with kind rule,decision and terms specific to the task", hermes)
        self.assertIn("when prior context could change the answer or next action", hermes)
        self.assertIn("Skip memory lookup for self-contained tasks", hermes)
        self.assertNotIn("Before you start a task", hermes)
        for name, text in (("claude-code", go), ("hermes", hermes), ("dsh", dsh)):
            with self.subTest(host=name):
                self.assertIn("No card of rules", text)
                self.assertNotIn("session card", text)
                if name != "hermes":
                    self.assertIn("Before you start a task, and again when it turns to another system", text)
                    self.assertIn("Skip it when the user's message and the repository's files hold everything", text)

class PiNoteTest(unittest.TestCase):
    def test_pi_carries_the_dsh_note_word_for_word(self):
        """#733: the Pi plugin has the same tool names as DSH, so it carries the DSH note as it is; every block the
        tests above check in the DSH note (the shelf, the pitfall line, no card, what the repository shows) is in it."""
        def note(path: Path) -> str:
            match = re.search(r"const NOTE = `([^`]*)`", path.read_text(encoding="utf-8"))
            self.assertIsNotNone(match, path)
            return match.group(1)

        pi = note(PLUGINS / "pi" / "src" / "instructions.ts")
        self.assertEqual(pi, note(PLUGINS / "deepseek-harness" / "src" / "index.mjs"))
        self.assertIn("what the repository", pi)
        self.assertNotIn("memcue_sessions", pi)


class OpenCodeNoteTest(unittest.TestCase):
    def test_opencode_carries_the_pi_note_word_for_word(self):
        """#752: the OpenCode plugin has the same tools and the same write rules as the Pi plugin, so both notes are
        the Pi plugin's text as it is; the instruction part is what the model reads every request."""
        def constant(path: Path, name: str) -> str:
            match = re.search(r"export const " + name + r" = `([^`]*)`", path.read_text(encoding="utf-8"))
            self.assertIsNotNone(match, path)
            return match.group(1)

        pi = PLUGINS / "pi" / "src" / "instructions.ts"
        opencode = PLUGINS / "opencode" / "src" / "instructions.ts"
        self.assertEqual(constant(opencode, "NOTE"), constant(pi, "NOTE"))
        self.assertEqual(constant(opencode, "OPENCODE_NOTE"), constant(pi, "PI_NOTE"))
        self.assertIn("what the repository", constant(opencode, "NOTE"))
        self.assertNotIn("memcue_sessions", constant(opencode, "NOTE"))


class EvidenceBeforeChangeTest(unittest.TestCase):
    EVIDENCE = ("Correct or retire a stored fact only when the user says it is wrong or a command or a file of this "
                "session shows it: a newer fact that says otherwise is not that evidence on its own. When nothing in "
                "the session settles it, answer from the facts and their days, name the conflict, and ask before you "
                "correct or retire one.")

    def test_every_host_asks_for_evidence_before_it_changes_a_fact(self):
        """#771: asked only where the CI runs, the model retired the older of two conflicting facts in 5 of 10 sessions
        on its day alone. Every text a host's model reads about correct and retire says, word for word, what counts as
        evidence and that a newer fact is not."""
        texts = {
            "session.go": PLUGINS.parent / "cmd" / "memcue" / "session.go",
            "claude-code skill": PLUGINS / "claude-code" / "skills" / "memcue" / "SKILL.md",
            "hermes": PLUGINS / "hermes" / "memcue" / "__init__.py",
            "dsh": PLUGINS / "deepseek-harness" / "src" / "index.mjs",
            "pi": PLUGINS / "pi" / "src" / "instructions.ts",
            "opencode": PLUGINS / "opencode" / "src" / "instructions.ts",
            "QWEN.md.snippet": PLUGINS / "qwen-code" / "QWEN.md.snippet",
            "qwen-code skill": PLUGINS / "qwen-code" / "skills" / "memcue" / "SKILL.md",
            "AGENTS.md.snippet": PLUGINS / "codex" / "AGENTS.md.snippet",
            "codex skill": PLUGINS / "codex" / "skills" / "memcue" / "SKILL.md",
        }
        for name, path in texts.items():
            with self.subTest(text=name):
                text = path.read_text(encoding="utf-8")
                if name == "hermes":
                    text = re.sub(r'"\s*\n\s*"', "", text)  # the note is adjacent string literals; read it joined
                self.assertIn(self.EVIDENCE, " ".join(text.split()))


class OpenCodePolicyTest(unittest.TestCase):
    def test_opencode_memory_is_opt_in_for_children_without_harness_dependencies(self):
        """Execute the delivered configuration policy with Node built-ins only; no OpenCode SDK, binary or model."""
        config = (PLUGINS / "opencode/src/config.ts").as_uri()
        script = f"""
import {{readConfig, isEnabled}} from {json.dumps(config)};
const defaults = readConfig(undefined);
const configured = readConfig({{subagents: {{defaultEnabled: true,
  agents: {{worker: true, 'maintainability-reviewer': false}}}}}});
console.log(JSON.stringify({{
  main: isEnabled(defaults, {{isChild: false}}),
  child: isEnabled(defaults, {{isChild: true}}),
  worker: isEnabled(configured, {{isChild: true, agent: 'worker'}}),
  reviewer: isEnabled(configured, {{isChild: true, agent: 'maintainability-reviewer'}}),
  ambientMain: isEnabled(configured, {{isChild: true}}),
  switchedOff: isEnabled(configured, {{isChild: true, agent: 'worker', sessionDisabled: true}}),
  inheritedRole: isEnabled(defaults, {{isChild: true, agent: 'constructor'}}),
  globalOff: isEnabled(readConfig({{enabled: false}}), {{isChild: false}})
}}));
"""
        result = subprocess.run(["node", "--experimental-strip-types", "--input-type=module", "-e", script],
                                text=True, capture_output=True, timeout=20)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(result.stdout), {
            "main": True, "child": False, "worker": True, "reviewer": False,
            "ambientMain": False, "switchedOff": False, "inheritedRole": False, "globalOff": False,
        })


class CodexDeliveryTest(unittest.TestCase):
    def test_codex_delivers_pull_policy_without_other_hosts_history_or_card(self):
        """Exercise the installed entry point, not just an unused policy string."""
        with tempfile.TemporaryDirectory(prefix="codex-contract-") as tmp:
            home = Path(tmp)
            binary = home / "memcue"
            binary.write_text("#!/bin/sh\nexit 0\n")
            binary.chmod(0o700)
            config = home / "memcue.json"
            config.write_text(json.dumps({"binary":str(binary), "facts":str(home/"facts"),
                                          "state_dir":str(home/"state"), "host":"synthetic-host"}))
            env = {k:v for k,v in os.environ.items() if not k.startswith(("MEMCUE_", "CLAUDE_"))}
            env.update(HOME=tmp, CODEX_HOME=str(home/"codex"))
            payload = {"hook_event_name":"SessionStart", "source":"startup", "cwd":tmp,
                       "session_id":"synthetic-session", "model":"synthetic-model",
                       "permission_mode":"default", "transcript_path":None}
            result = subprocess.run([sys.executable, "-I", str(PLUGINS/"codex/scripts/memcue.py"),
                                     "--config", str(config), "hook"], input=json.dumps(payload),
                                    text=True, capture_output=True, env=env, timeout=20)
            self.assertEqual(result.returncode,0,result.stderr)
            delivered = json.loads(result.stdout)
            self.assertNotIn("systemMessage",delivered)
            self.assertEqual(delivered["hookSpecificOutput"]["hookEventName"],"SessionStart")
            text = delivered["hookSpecificOutput"]["additionalContext"]
            for term in ("No card of rules", "Before you start a task", "another system", "-kind rule,decision",
                         "-in global", "-in shelf:research", "paths or procedures already documented in the repository", "Search first", "English",
                         "legacy-language", "corrected fact content", "verbatim quotations", "user's requested language"):
                self.assertIn(term,text)
            for forbidden in ("memcue sessions", "session_search", "built-in memory", "claude -p"):
                self.assertNotIn(forbidden,text)


class PiRolePolicyTest(unittest.TestCase):
    def test_pi_memory_is_opt_in_for_children_without_harness_dependencies(self):
        """Execute the delivered policy with Node built-ins only; no Pi SDK or model is needed."""
        config = (PLUGINS / "pi/src/config.ts").as_uri()
        script = f"""
import {{readConfig, isEnabled}} from {json.dumps(config)};
const defaults = readConfig({{}});
const configured = readConfig({{memcue: {{subagents: {{defaultEnabled: true,
  agents: {{worker: true, 'maintainability-reviewer': false}}}}}}}});
console.log(JSON.stringify({{
  main: isEnabled(defaults, {{isChild: false}}),
  child: isEnabled(defaults, {{isChild: true}}),
  worker: isEnabled(configured, {{isChild: true, agent: 'worker'}}),
  reviewer: isEnabled(configured, {{isChild: true, agent: 'maintainability-reviewer'}}),
  ambientMain: isEnabled(configured, {{isChild: true}}),
  switchedOff: isEnabled(configured, {{isChild: true, agent: 'worker', sessionDisabled: true}}),
  inheritedRole: isEnabled(defaults, {{isChild: true, agent: 'constructor'}}),
  globalOff: isEnabled(readConfig({{memcue: {{enabled: false}}}}), {{isChild: false}})
}}));
"""
        result = subprocess.run(["node", "--experimental-strip-types", "--input-type=module", "-e", script],
                                text=True, capture_output=True, timeout=20)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(result.stdout), {
            "main": True, "child": False, "worker": True, "reviewer": False,
            "ambientMain": False, "switchedOff": False, "inheritedRole": False, "globalOff": False,
        })


if __name__ == "__main__":
    unittest.main()
