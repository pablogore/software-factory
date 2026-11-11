"""The factory skill keeps the orchestrator lean. Run: python3 -m pytest factory/tests -k lean_orchestrator

SKILL.md must ask the orchestrator to run on sonnet, to tell the user only what
they need to know or decide (not one line per transition), and to leave the
decisions that need no judgment to the scripts.
"""

import re
import shutil
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
SKILL = Path(".claude/skills/factory/SKILL.md")
DIRECTIVES = {
    "model": "Run the orchestrator on sonnet unless the user asks for another model",
    "messages": "Tell the user only what they need to know or decide",
    "scripts": "Leave every decision that needs no judgment to the `factory/bin/` scripts",
}
PER_TRANSITION = "short line per transition"


def flat(text):
    return re.sub(r"\s+", " ", text)


def missing_lean_directives(root):
    """The directives the skill under root lacks, plus "per-transition" if it still asks for it."""
    text = flat((root / SKILL).read_text())
    missing = [k for k, d in DIRECTIVES.items() if d not in text]
    if PER_TRANSITION in text:
        missing.append("per-transition")
    return missing


class LeanOrchestratorTest(unittest.TestCase):
    def test_lean_orchestrator_directives_in_skill(self):
        self.assertEqual(missing_lean_directives(ROOT), [])

    def test_demo_lean_orchestrator_check_fails_without_each_directive(self):
        tmp = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, tmp)
        (tmp / SKILL).parent.mkdir(parents=True)
        text = flat((ROOT / SKILL).read_text())
        for key, directive in DIRECTIVES.items():
            (tmp / SKILL).write_text(text.replace(directive, ""))
            self.assertEqual(missing_lean_directives(tmp), [key])
        (tmp / SKILL).write_text(text + "\nReport one short line per transition.\n")
        self.assertEqual(missing_lean_directives(tmp), ["per-transition"])


if __name__ == "__main__":
    unittest.main()
