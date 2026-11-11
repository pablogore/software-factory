"""The factory skill keeps the orchestrator lean. Run: python3 -m pytest factory/tests -k lean_orchestrator

README.md and INSTALL.md must start the orchestrator with `claude --model sonnet`
(the skill can't change its own session's model). SKILL.md must say in one line
when it runs on opus and suggest sonnet, tell the user only what they need to
know or decide (not one line per transition), and leave the decisions that need
no judgment to the scripts.
"""

import re
import shutil
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
SKILL = Path(".claude/skills/factory/SKILL.md")
DIRECTIVES = {
    "readme-sonnet": (Path("README.md"), "claude --model sonnet"),
    "install-sonnet": (Path("INSTALL.md"), "claude --model sonnet"),
    "model": (SKILL, "If this session runs on opus, say so in one line at the start and suggest sonnet"),
    "messages": (SKILL, "Tell the user only what they need to know or decide"),
    "messages-failed": (SKILL, "a job that failed or is stuck"),
    "messages-cap": (SKILL, "a run stopped by a cap"),
    "scripts": (SKILL, "Leave every decision that needs no judgment to the `factory/bin/` scripts"),
}
PER_TRANSITION = "short line per transition"


def flat(text):
    return re.sub(r"\s+", " ", text)


def missing_lean_directives(root):
    """The directives missing under root, plus "per-transition" if the skill still asks for it."""
    missing = [k for k, (path, d) in DIRECTIVES.items() if d not in flat((root / path).read_text())]
    if PER_TRANSITION in flat((root / SKILL).read_text()):
        missing.append("per-transition")
    return missing


class LeanOrchestratorTest(unittest.TestCase):
    def test_lean_orchestrator_directives_in_skill(self):
        self.assertEqual(missing_lean_directives(ROOT), [])

    def test_demo_lean_orchestrator_check_fails_without_each_directive(self):
        tmp = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, tmp)
        paths = {path for path, _ in DIRECTIVES.values()}
        texts = {path: flat((ROOT / path).read_text()) for path in paths}

        def write(changed=None, text=None):
            for path in paths:
                (tmp / path).parent.mkdir(parents=True, exist_ok=True)
                (tmp / path).write_text(text if path == changed else texts[path])

        for key, (path, directive) in DIRECTIVES.items():
            write(path, texts[path].replace(directive, ""))
            self.assertEqual(missing_lean_directives(tmp), [key])
        write(SKILL, texts[SKILL] + "\nReport one short line per transition.\n")
        self.assertEqual(missing_lean_directives(tmp), ["per-transition"])


if __name__ == "__main__":
    unittest.main()
