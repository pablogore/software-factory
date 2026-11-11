"""The approver's CI rule: no APPROVE while the PR's last commit has failing or pending checks.

The rule is text for a model, so these tests check that it (and the rules it sits
next to) stays in .claude/agents/approver.md, that the command it prescribes reads
green, failing, pending and no checks apart against a fake gh, and that the
approver's guard lets that command through.

Run: python3 -m pytest factory/tests -k approver_ci  (or discover factory/tests)
"""

import json
import os
import re
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
APPROVER = ROOT / ".claude" / "agents" / "approver.md"
GUARD = ROOT / "factory" / "bin" / "guard"
FAKE = Path(__file__).resolve().parent / "fake_gh.py"


def approver_text():
    return " ".join(APPROVER.read_text().split())  # wrapping doesn't matter


def checks_command():
    """The `gh pr checks <pr> ...` command the approver is told to run."""
    m = re.search(r"`cd <worktree> && (gh pr checks <pr>[^`]*)`", approver_text())
    assert m, "approver.md doesn't prescribe a `gh pr checks <pr>` command"
    return m.group(1)


def decide(proc):
    """What approver.md's CI rule says for one `gh pr checks` result: (decision, checks to name)."""
    if proc.returncode != 0:
        return ("as today", []) if "no checks reported" in proc.stderr else ("ESCALATE", [])
    bad = [c for c in json.loads(proc.stdout) if c["bucket"] in ("fail", "cancel", "pending")]
    return ("ESCALATE", bad) if bad else ("as today", [])


class TestDemoApproverCI(unittest.TestCase):
    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, self.tmp)
        bin_ = self.tmp / "bin"
        bin_.mkdir()
        (bin_ / "gh").write_text(f'#!/bin/sh\nexec python3 "{FAKE}" "$@"\n')
        (bin_ / "gh").chmod(0o755)
        self.state = self.tmp / "gh.json"
        self.env = {**os.environ, "PATH": f"{bin_}:{os.environ['PATH']}", "FAKE_GH_STATE": str(self.state)}

    def pr_with(self, checks):
        pr = {"number": 7, "head": "factory/007-x", "base": "main", "title": "x", "body": "Refs #1",
              "state": "OPEN", "isDraft": True, "comments": [], "reviews": [], "line_comments": [],
              "url": "https://github.com/acme/app/pull/7", "checks": checks}
        self.state.write_text(json.dumps({"user": "pablo", "next": 8, "issues": {}, "prs": {"7": pr},
                                          "labels": []}))

    def gh(self, command):
        return subprocess.run(command.replace("<pr>", "7"), shell=True, cwd=self.tmp, env=self.env,
                              capture_output=True, text=True)

    def test_approver_ci_green_decides_as_today(self):
        self.pr_with([{"name": "test", "state": "SUCCESS", "bucket": "pass"},
                      {"name": "deploy", "state": "SKIPPED", "bucket": "skipping"}])
        self.assertEqual(decide(self.gh(checks_command())), ("as today", []))

    def test_approver_ci_failing_escalates_naming_the_check(self):
        self.pr_with([{"name": "test", "state": "FAILURE", "bucket": "fail"},
                      {"name": "lint", "state": "SUCCESS", "bucket": "pass"},
                      {"name": "e2e", "state": "CANCELLED", "bucket": "cancel"}])
        decision, named = decide(self.gh(checks_command()))
        self.assertEqual(decision, "ESCALATE")
        self.assertEqual([(c["name"], c["state"]) for c in named], [("test", "FAILURE"), ("e2e", "CANCELLED")])

    def test_approver_ci_pending_escalates_naming_the_check(self):
        self.pr_with([{"name": "test", "state": "IN_PROGRESS", "bucket": "pending"},
                      {"name": "lint", "state": "SUCCESS", "bucket": "pass"}])
        decision, named = decide(self.gh(checks_command()))
        self.assertEqual(decision, "ESCALATE")
        self.assertEqual([(c["name"], c["state"]) for c in named], [("test", "IN_PROGRESS")])

    def test_approver_ci_no_checks_is_not_failing(self):
        self.pr_with([])
        proc = self.gh(checks_command())
        self.assertNotEqual(proc.returncode, 0)  # gh errors out, like the real one
        self.assertIn("no checks reported", proc.stderr)
        self.assertEqual(decide(proc), ("as today", []))
        rollup = self.gh("gh pr view <pr> --json statusCheckRollup")
        self.assertEqual(json.loads(rollup.stdout), {"statusCheckRollup": []})

    def test_approver_ci_guard_lets_the_checks_through(self):
        def guard(command):
            ev = {"tool_name": "Bash", "tool_input": {"command": command}, "cwd": str(ROOT)}
            return subprocess.run([str(GUARD), "approver"], input=json.dumps(ev), capture_output=True,
                                  text=True).returncode
        wt = ROOT / ".worktrees" / "007-x"
        self.assertEqual(guard(f"cd {wt} && {checks_command().replace('<pr>', '7')}"), 0)
        self.assertEqual(guard(f"cd {wt} && gh pr checks 7"), 0)
        self.assertEqual(guard(f"cd {wt} && gh pr view 7 --json statusCheckRollup"), 0)
        self.assertEqual(guard(f"cd {wt} && gh pr merge 7"), 2)  # the guard still blocks merging

    def test_approver_ci_rules_present(self):
        text = approver_text()
        new_rule = [
            "The PR's CI checks on its last commit are green.",
            "It fails with `no checks reported`: the PR has no CI checks",
            "That is not a failure: this point holds and you decide as you would without it.",
            "Every check's `bucket` is `pass` or `skipping`: this point holds.",
            "Any check's `bucket` is `fail`, `cancel` or `pending`: ESCALATE, and name each of those checks",
            "Any other error: ESCALATE, saying the checks could not be read and why.",
        ]
        existing = [
            "**Run the spec's `## Demo` test command** in the worktree, exactly as written;",
            "build.md's own claims are not evidence. It must pass and show the Valor the spec promises.",
            "ESCALATE if any of them fails, or if the change:",
            "touches money, access or identity: payments, auth, permissions, or database and storage security rules;",
            "changes deploy, CI or hosting config;",
            "migrates a database schema, sends email, writes production data, or adds a dependency;",
            "rests on a spec Assumption that is really a product or business decision,",
            "it syncs the branch with the base, runs `factory/checks.sh` again, and runs `gh pr merge`.",
            "The final round's `checks.md` says `GATE: PASS`",
            "The first line must be exactly `APPROVE` or `ESCALATE`.",
        ]
        for line in new_rule + existing:
            with self.subTest(line=line):
                self.assertIn(line, text)


if __name__ == "__main__":
    unittest.main()
