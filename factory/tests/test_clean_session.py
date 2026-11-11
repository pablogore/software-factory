"""The clean-session command stays documented. Run: python3 -m pytest factory/tests -k clean_session

INSTALL.md and README.md must show the command that starts the orchestrator's
session without MCP servers, user settings or automatic memory.
"""

import shutil
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
COMMAND = "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1 claude --strict-mcp-config --setting-sources project,local"
DOCS = ("INSTALL.md", "README.md")


def missing_clean_session(root):
    """The docs under root that don't show the clean-session command."""
    return [d for d in DOCS if COMMAND not in (root / d).read_text()]


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


if __name__ == "__main__":
    unittest.main()
