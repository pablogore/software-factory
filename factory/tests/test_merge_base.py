"""`job merge` refuses a PR whose base on GitHub isn't the job's base: the job
goes to needs-human and nothing changes on GitHub or on the branch.

Run: python3 -m pytest factory/tests -k merge
"""

import unittest

from test_e2e import E2EBase

INTAKE = "DECISION: ACCEPT\nRISK: low\nSURFACES: none\nRESCOPED: no\n\n## Scope\nx\n"


class TestDemoMergeBase(E2EBase):
    def approved(self, title):
        """Issue → job built, reviewed and approved: ready for `job merge`."""
        n = self.sh(f"gh issue create --label factory --title '{title}' --body x").stdout.strip().rsplit("/", 1)[1]
        jid = self.job("create", n).stdout.strip()
        self.legacy(jid)
        self.jobfile(jid, "intake.md", INTAKE)
        self.sh(f"factory/bin/route {jid}")
        self.job("advance", jid, "spec")
        self.jobfile(jid, "spec.md", "# Algo\n\n## Acceptance criteria\n1. x\n")
        self.job("advance", jid, "build")
        self.build(jid, int(n), {f"{jid}.txt": "v1\n"}, ref="Refs")
        self.assertIn("GATE: PASS", self.to_review(jid))
        self.job("advance", jid, "review")
        self.reviews(jid, 1, code="PASS")
        self.job("advance", jid, "approve")
        self.jobfile(jid, "decision.md", "APPROVE\n\n## Summary\n- ok\n")
        return jid, str(self.stage(jid)["pr"])

    def remote_head(self, jid):
        return self.sh(f"git ls-remote --heads origin factory/{jid}").stdout.strip()

    def test_demo_merge_refuses_a_pr_retargeted_to_another_base(self):
        jid, pr = self.approved("Retargeted")
        s = self.gh()
        s["prs"][pr]["base"] = "release"  # someone changed the PR's base on GitHub
        self.set_gh(s)
        before, head = self.gh()["prs"][pr], self.remote_head(jid)
        out = self.job("merge", jid).stdout
        self.assertIn("release", out)
        self.assertIn("main", out)
        j = self.stage(jid)
        self.assertEqual(j["stage"], "needs-human")
        why = (self.repo / f"factory/jobs/{jid}/decision.md").read_text()
        self.assertIn("targets release, but the job's base is main", why)
        # not merged, not retargeted, the branch on origin untouched; only the
        # needs-human escalation (comment, label) reaches GitHub
        after = self.gh()["prs"][pr]
        self.assertEqual((after["state"], after["base"]), (before["state"], before["base"]))
        self.assertEqual(after["state"], "OPEN")
        self.assertIn("targets release", after["comments"][-1]["body"])
        self.assertIn("factory:needs-human", self.gh()["issues"][str(j["issue"])]["labels"])
        self.assertEqual(self.remote_head(jid), head)
        self.assertEqual(self.sh("git rev-parse HEAD", cwd=self.repo / ".worktrees" / jid).stdout.split()[0],
                         head.split()[0])
        self.assertFalse((self.repo / f"factory/jobs/{jid}/round-1/merge-checks.md").exists())

    def test_demo_merge_with_the_jobs_base_merges_as_today(self):
        jid, pr = self.approved("Same base")
        self.assertIn("merged", self.job("merge", jid).stdout)
        self.assertEqual(self.gh()["prs"][pr]["state"], "MERGED")
        self.assertEqual(self.stage(jid)["stage"], "merged")
        self.assertEqual(self.remote_head(jid), "")


if __name__ == "__main__":
    unittest.main()
