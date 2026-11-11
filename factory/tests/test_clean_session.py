"""The clean-session command stays documented. Run: python3 -m pytest factory/tests -k clean_session

INSTALL.md and README.md must show the command that starts the orchestrator's
session without MCP servers, user settings or automatic memory, and say what
is confirmed about it and what isn't.
"""

import shutil
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
COMMAND = "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1 claude --model sonnet --strict-mcp-config --setting-sources project,local"
DOCS = ("INSTALL.md", "README.md")
# What the docs must say about the clean session: verified in spike S4, or not.
CLARIFICATIONS = (
    "subagents of a clean session have no MCP servers\nand no memory either",
    "without these flags they have both",
    "settings is not confirmed",
    "is a recommendation, not a measurement",
)
# What no doc may say any more: an unmeasured claim and a stale promise.
STALE = ("spends less", "measured in spike s4", "that is measured in")
ALL_DOCS = ("INSTALL.md", "README.md", "DESIGN.md")


def missing_clean_session(root):
    """The docs under root that don't show the clean-session command."""
    return [d for d in DOCS if COMMAND not in (root / d).read_text()]


def flat(text):
    """text with every run of whitespace as one space, so line wraps don't matter."""
    return " ".join(text.split())


def missing_clarifications(root):
    """(doc, clarification) pairs the docs under root don't state."""
    return [
        (d, c)
        for d in DOCS
        for c in CLARIFICATIONS
        if flat(c) not in flat((root / d).read_text())
    ]


def stale_claims(root):
    """(doc, phrase) pairs where a doc under root still says something stale."""
    return [
        (d, s)
        for d in ALL_DOCS
        for s in STALE
        if s in flat((root / d).read_text()).lower()
    ]


class CleanSessionDocsTest(unittest.TestCase):
    def test_clean_session_command_in_docs(self):
        self.assertEqual(missing_clean_session(ROOT), [])

    def test_clean_session_check_fails_without_command(self):
        tmp = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, tmp)
        for d in DOCS:
            shutil.copy(ROOT / d, tmp / d)
        for d in DOCS:
            text = (ROOT / d).read_text()
            (tmp / d).write_text(text.replace(COMMAND, ""))
            self.assertEqual(missing_clean_session(tmp), [d])
            (tmp / d).write_text(text)

    def test_clean_session_clarifications_in_docs(self):
        self.assertEqual(missing_clarifications(ROOT), [])

    def test_no_stale_clean_session_claims(self):
        self.assertEqual(stale_claims(ROOT), [])

    def test_demo_clean_session_docs_checks_catch_regressions(self):
        tmp = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, tmp)
        for d in ALL_DOCS:
            shutil.copy(ROOT / d, tmp / d)
        self.assertEqual(missing_clarifications(tmp), [])
        self.assertEqual(stale_claims(tmp), [])
        readme = (tmp / "README.md").read_text()
        (tmp / "README.md").write_text(
            flat(readme).replace("settings is not confirmed", "settings are inherited")
            + "\nThe orchestrator on sonnet spends less. That is measured in spike S4.\n"
        )
        self.assertEqual(
            missing_clarifications(tmp), [("README.md", "settings is not confirmed")]
        )
        self.assertEqual(
            stale_claims(tmp),
            [
                ("README.md", "spends less"),
                ("README.md", "measured in spike s4"),
                ("README.md", "that is measured in"),
            ],
        )


if __name__ == "__main__":
    unittest.main()
