"""Build the base and candidate plugins of the #531 stand into a docker build context.

Run: python3 -I make_variants.py REPO CONTEXT. REPO is the memcue checkout, CONTEXT a new directory. The base plugins
are copies of the checkout's; the candidate ones add the text of confirm531/candidate/ the way a shipped change
would: Claude Code gets a fourth SessionStart hook with the note and the skill gets its section, Hermes gets the block
after its memory note in the system prompt. Each insertion point must be found exactly once, or the build stops.
"""

import json
import shutil
import sys
from pathlib import Path

HOOK = '''import json, pathlib
note = pathlib.Path(__file__).with_name("candidate-note.md").read_text(encoding="utf-8").strip()
print(json.dumps({"hookSpecificOutput": {"hookEventName": "SessionStart", "additionalContext": note}}))
'''
HERMES_ANCHOR = '+ ("\\n\\n" + card if card else ""))'
HERMES_INSERT = '+ "\\n\\n" + _STAND_CANDIDATE + ("\\n\\n" + card if card else ""))'
HERMES_DEF = ('\n_STAND_CANDIDATE = Path(__file__).with_name("candidate.md").read_text(encoding="utf-8").strip()'
              '  # the #531 stand\n')


def once(text: str, old: str, new: str, where: str) -> str:
    if text.count(old) != 1:
        sys.exit(f"{where}: the insertion point occurs {text.count(old)} times, not once")
    return text.replace(old, new)


def main() -> None:
    repo, ctx = Path(sys.argv[1]).resolve(), Path(sys.argv[2]).resolve()
    ctx.mkdir(parents=True)  # a new directory: an existing one is an error, nothing is overwritten
    cand = repo / "docs/research/scripts/confirm531/candidate"

    cc = repo / "plugins/claude-code"
    for arm in ("base", "cand"):
        shutil.copytree(cc, ctx / f"plugin-{arm}", ignore=shutil.ignore_patterns("test", "node_modules"))
    p = ctx / "plugin-cand"
    skill = p / "skills/memcue/SKILL.md"
    skill.write_text(skill.read_text(encoding="utf-8").rstrip("\n") + "\n" + (cand / "cc-skill.md").read_text(
        encoding="utf-8"), encoding="utf-8")
    (p / "scripts/candidate-note.md").write_text((cand / "cc-note.md").read_text(encoding="utf-8"), encoding="utf-8")
    (p / "scripts/candidate_note.py").write_text(HOOK, encoding="utf-8")
    hooks_path = p / "hooks/hooks.json"
    hooks = json.loads(hooks_path.read_text(encoding="utf-8"))
    hooks["hooks"]["SessionStart"].append({"hooks": [{
        "type": "command", "command": 'python3 -I "${CLAUDE_PLUGIN_ROOT}/scripts/candidate_note.py"', "timeout": 30}]})
    hooks_path.write_text(json.dumps(hooks, indent=2) + "\n", encoding="utf-8")

    hm = repo / "plugins/hermes/memcue"
    for arm in ("base", "cand"):
        shutil.copytree(hm, ctx / f"hermes-plugin-{arm}", ignore=shutil.ignore_patterns("tests", "__pycache__"))
    h = ctx / "hermes-plugin-cand"
    (h / "candidate.md").write_text((cand / "hermes-block.md").read_text(encoding="utf-8"), encoding="utf-8")
    init = h / "__init__.py"
    src = init.read_text(encoding="utf-8")
    src = once(src, HERMES_ANCHOR, HERMES_INSERT, "hermes system_prompt_block")
    src = once(src, "\n_CJK = ", HERMES_DEF + "\n_CJK = ", "hermes module constants")
    init.write_text(src, encoding="utf-8")
    print(json.dumps({"context": str(ctx), "variants": sorted(x.name for x in ctx.iterdir())}))


if __name__ == "__main__":
    main()
