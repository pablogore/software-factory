"""End-to-end: a human's feedback on a slice the approver approved, before the merge.

- with a round left it goes back to build in the next round, PR back in draft
- with no round left it waits in needs-human, the feedback saved
- a merged slice refuses it and says so; approve without APPROVE refuses as before

Run: python3 -m unittest discover -s factory/tests -p 'test_*.py'
"""

import json
import unittest

import test_initiative_fixes as fixes
from test_e2e import E2EBase


class ApproveFeedbackE2E(E2EBase):
    @classmethod
    def setUpClass(cls):
        super().setUpClass()
        cfg = cls.repo / "factory" / "config.json"  # every test leaves a job running
        cfg.write_text(json.dumps({**json.loads(cfg.read_text()), "concurrency": 10}, indent=2))

    # Its flow helpers, without importing the class (discover would run its tests here too).
    _f = fixes.InitiativeFixesE2E
    rows, slices, init_dir, start, to_build, to_approve = (
        _f.rows, _f.slices, _f.init_dir, _f.start, _f.to_build, _f.to_approve)
    del _f

    def approved(self, title):
        n, jid = self.start(title, ("S1", "slice", "Uno", "next"))
        self.to_build(jid, "S1")
        self.to_approve(jid, n, "v1\n")
        self.jobfile(jid, "decision.md", "APPROVE\n\n## Summary\n- ok\n")
        return n, jid, str(self.stage(jid)["pr"])

    def set_job(self, jid, **fields):
        path = self.repo / f"factory/jobs/{jid}/job.json"
        path.write_text(json.dumps({**json.loads(path.read_text()), **fields}))

    def test_demo_feedback_after_approve_goes_back_to_build_with_pr_in_draft(self):
        n, jid, pr = self.approved("Approved, then feedback")
        s = self.gh()
        s["prs"][pr]["isDraft"] = False  # the PR was marked ready on the way
        self.set_gh(s)
        out = self.job("feedback", jid, input="El botón tiene que decir Guardar\n").stdout
        self.assertIn("feedback-1.md → build", out)
        j = self.stage(jid)
        self.assertEqual((j["stage"], j["round"]), ("build", 2))
        self.assertIn("El botón tiene que decir Guardar",
                      (self.repo / f"factory/jobs/{jid}/feedback-1.md").read_text())
        self.assertTrue(self.gh()["prs"][pr]["isDraft"])

    def test_demo_feedback_after_approve_without_rounds_goes_to_needs_human(self):
        n, jid, pr = self.approved("Approved, last round")
        self.set_job(jid, round_limit=self.stage(jid)["round"])
        out = self.job("feedback", jid, input="Falta el mensaje de error\n").stdout
        self.assertIn("feedback-1.md → needs-human", out)
        j = self.stage(jid)
        self.assertEqual((j["stage"], j["round"]), ("needs-human", 1))
        self.assertIn("Falta el mensaje de error", (self.repo / f"factory/jobs/{jid}/feedback-1.md").read_text())
        self.assertIn("factory:needs-human", self.gh()["issues"][n]["labels"])
        self.job("reject", jid, input="test cleanup")

    def test_demo_feedback_on_a_merged_job_is_refused(self):
        n, jid, pr = self.approved("Merged, then feedback")
        self.job("merge", jid)
        self.assertTrue(self.stage(jid)["merged"])
        err = self.job("feedback", jid, ok=False, input="tarde\n").stderr
        self.assertIn(f"PR #{pr} is already merged", err)
        self.assertFalse((self.repo / f"factory/jobs/{jid}/feedback-1.md").exists())

    def test_feedback_in_approve_after_a_person_merged_the_pr_is_refused(self):
        n, jid, pr = self.approved("Merged by hand")
        s = self.gh()
        s["prs"][pr]["state"] = "MERGED"
        self.set_gh(s)
        err = self.job("feedback", jid, ok=False, input="tarde\n").stderr
        self.assertIn(f"PR #{pr} is already merged", err)
        self.assertEqual(self.stage(jid)["stage"], "approve")

    def test_feedback_in_approve_without_approve_is_refused_as_before(self):
        n, jid, pr = self.approved("Approve pending")
        (self.repo / f"factory/jobs/{jid}/decision.md").unlink()
        err = self.job("feedback", jid, ok=False, input="algo\n").stderr
        self.assertIn("is in approve, not needs-human", err)

    def test_direction_feedback_from_approve_is_refused(self):
        n, jid, pr = self.approved("Direction from approve")
        err = self.job("feedback", jid, "--direction", ok=False, input="otra cosa\n").stderr
        self.assertIn("`--direction` from approve isn't supported", err)
        self.assertEqual(self.stage(jid)["stage"], "approve")


if __name__ == "__main__":
    unittest.main()
