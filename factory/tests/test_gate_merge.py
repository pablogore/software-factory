"""`gate <job>` runs the checks on the branch merged with a freshly fetched base,
in a throwaway worktree: a branch that only breaks (or conflicts) against the
new base fails, a base it can't fetch fails without checks, and the branch, its
worktree and origin stay as they were.

Run: python3 -m pytest factory/tests -k gate_merge
"""

import json
import unittest

from test_e2e import E2EBase

INTAKE = "DECISION: ACCEPT\nRISK: low\nSURFACES: none\nRESCOPED: no\n\n## Scope\nx\n"


class TestDemoGateMerge(E2EBase):
    @classmethod
    def setUpClass(cls):
        super().setUpClass()
        cfg = cls.repo / "factory" / "config.json"  # every test leaves a job running
        cfg.write_text(json.dumps({**json.loads(cfg.read_text()), "concurrency": 10}, indent=2))
        cls.other = cls.tmp / "other"  # someone else's clone: what moves main on origin
        cls.sh(f"git clone -q {cls.tmp / 'origin.git'} {cls.other}", cwd=cls.tmp)

    def push_base(self, files, msg):
        """Another PR lands on main while the job is in flight."""
        for rel, text in files.items():
            if text is None:
                (self.other / rel).unlink()
            else:
                (self.other / rel).write_text(text)
        self.sh(f"git pull -q --no-rebase && git add -A && git commit -qm '{msg}' && git push -q origin main",
                cwd=self.other)
        return self.sh("git rev-parse HEAD", cwd=self.other).stdout.strip()

    def built(self, title, files):
        """Issue → job built from today's main and in the gate stage."""
        n = self.sh(f"gh issue create --label factory --title '{title}' --body x").stdout.strip().rsplit("/", 1)[1]
        jid = self.job("create", n).stdout.strip()
        self.legacy(jid)
        self.jobfile(jid, "intake.md", INTAKE)
        self.sh(f"factory/bin/route {jid}")
        self.job("advance", jid, "spec")
        self.jobfile(jid, "spec.md", "# Algo\n\n## Acceptance criteria\n1. x\n")
        self.job("advance", jid, "build")
        self.build(jid, int(n), files, ref="Refs")
        self.job("advance", jid, "gate")
        return jid

    def snapshot(self, jid):
        """Everything the gate must leave alone: the branch, its worktree, origin, the shared refs."""
        wt = self.repo / ".worktrees" / jid
        return {
            "head": self.sh("git rev-parse HEAD", cwd=wt).stdout.strip(),
            "status": self.sh("git status --porcelain --untracked-files=all", cwd=wt).stdout,
            "origin": self.sh("git for-each-ref", cwd=self.tmp / "origin.git").stdout,
            "worktrees": self.sh("git worktree list --porcelain").stdout,
            "refs": self.sh("git for-each-ref").stdout,
        }

    def gate(self, jid):
        before = self.snapshot(jid)
        res = self.sh(f"factory/bin/gate {jid}", ok=False)
        self.assertEqual(self.snapshot(jid), before)
        checks = (self.repo / f"factory/jobs/{jid}/round-1/checks.md").read_text()
        self.assertIn(f"COMMIT: {before['head']}\n", checks)
        return res, checks

    def test_demo_clean_merge_passes_and_names_the_base_commit(self):
        jid = self.built("Clean", {"clean.txt": "branch\n"})
        base = self.push_base({"clean-base.txt": "base\n"}, "unrelated work")
        res, checks = self.gate(jid)
        self.assertEqual(res.returncode, 0, res.stdout + res.stderr)
        self.assertIn("GATE: PASS", checks)
        self.assertIn(f"merged with origin/main at {base}", checks)
        self.job("advance", jid, "review")

    def test_demo_breakage_only_after_the_merge_fails(self):
        jid = self.built("Breaks", {"breaks.txt": "branch\n"})
        try:
            base = self.push_base({"BROKEN": "x\n"}, "the base changes under the branch")
            res, checks = self.gate(jid)
            self.assertEqual(res.returncode, 1)
            self.assertIn("GATE: FAIL", checks)
            self.assertIn("checks.sh failed with exit", checks)
            self.assertIn(f"merged with origin/main at {base}", checks)
            self.assertIn(base[:7], res.stdout)
            # the branch alone still passes: its worktree has no BROKEN
            self.assertFalse((self.repo / ".worktrees" / jid / "BROKEN").exists())
            self.job("advance", jid, "build")  # a failing gate goes back to build, as before
        finally:
            self.push_base({"BROKEN": None}, "fix the base")

    def test_demo_conflict_fails_and_lists_the_files(self):
        jid = self.built("Conflicts", {"shared.txt": "from the branch\n", "mine.txt": "x\n"})
        base = self.push_base({"shared.txt": "from the base\n"}, "same file on main")
        res, checks = self.gate(jid)
        self.assertEqual(res.returncode, 1)
        self.assertIn("GATE: FAIL", checks)
        self.assertIn(f"Merging origin/main at {base[:7]} into the branch conflicts in:\n\n- shared.txt\n", checks)
        self.assertNotIn("- mine.txt", checks)
        self.assertIn("Merge origin/main into the branch", checks)
        self.assertIn("checks.sh not run", checks)
        self.assertIn("shared.txt", (self.repo / f"factory/jobs/{jid}/round-1/checks.log").read_text())
        self.job("advance", jid, "build")
        self.assertIn("shared.txt", (self.repo / f"factory/jobs/{jid}/round-1/gate-retry-1.log").read_text())

    def test_demo_unreachable_base_fails_without_checks(self):
        jid = self.built("Offline", {"offline.txt": "x\n"})
        url = self.sh("git remote get-url origin").stdout.strip()
        self.sh(f"git remote set-url origin {self.tmp / 'gone.git'}")
        try:
            res, checks = self.gate(jid)
        finally:
            self.sh(f"git remote set-url origin {url}")
        self.assertEqual(res.returncode, 1)
        self.assertIn("GATE: FAIL", checks)
        self.assertIn("Couldn't fetch origin/main, so the checks didn't run.", checks)
        self.assertIn("checks.sh not run", checks)
        log = (self.repo / f"factory/jobs/{jid}/round-1/checks.log").read_text()
        self.assertIn("couldn't fetch origin/main", log)
        self.assertNotIn("checks ok", log)


if __name__ == "__main__":
    unittest.main()
