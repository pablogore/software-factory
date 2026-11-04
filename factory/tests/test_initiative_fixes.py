"""Regression tests for the initiative flow's edge cases (review of PR #1):

- one-slice initiatives use `Refs`, so a replan that adds slices finds the issue open
- `[direction]` on a slice whose PR never merged drops the slice and closes the PR
- a slice that escalated from spec goes back to spec with the human's feedback
- auto checkpoint without a free slot parks the job instead of losing the next slice
- an initiative whose `next` slice never got a job shows up in `job queue`

Run: python3 -m unittest discover factory/tests
"""

import json
import time
import unittest

from test_e2e import E2EBase, SLICES, SPEC

INTAKE = "DECISION: ACCEPT\nRISK: low\nSURFACES: none\nRESCOPED: no\n\n## Scope\nx\n"


class InitiativeFixesE2E(E2EBase):
    def rows(self, *rows):
        return SLICES.format(rows="".join(f"| {i} | {k} | {v} | {st} |\n" for i, k, v, st in rows))

    def slices(self, init, *rows):
        (self.repo / f"factory/initiatives/{init}/slices.md").write_text(self.rows(*rows))
        time.sleep(0.02)

    def init_dir(self, init):
        return self.repo / "factory/initiatives" / init

    def queue(self):
        return [r.split("\t") for r in self.job("queue").stdout.splitlines()]

    def start(self, title, *rows, checkpoint=None):
        """Issue → job → intake → initiative → the first `next` slice in spec."""
        n = self.sh(f"gh issue create --label factory --title '{title}' --body x").stdout.strip().rsplit("/", 1)[1]
        jid = self.job("create", n).stdout.strip()
        self.jobfile(jid, "intake.md", INTAKE)
        self.job("advance", jid, "initiative")
        (self.init_dir(jid) / "vision.md").write_text("STATUS: READY\n\n# V\n")
        (self.init_dir(jid) / "context.md").write_text("# Contexto\n")
        self.slices(jid, *rows)
        if checkpoint:
            self.job("set-checkpoint", jid, checkpoint)
        self.job("advance", jid, "spec")
        return n, jid

    def to_build(self, jid, sid):
        self.jobfile(jid, "spec.md", SPEC.format(status="READY", sid=sid, title="hacer algo", extra=""))
        self.job("advance", jid, "rate")
        self.jobfile(jid, "intake.md", INTAKE)
        self.sh(f"factory/bin/route {jid}")
        self.job("advance", jid, "build")

    def to_approve(self, jid, n, content):
        self.build(jid, int(n), {f"{jid}.txt": content}, ref=self.stage(jid)["pr_ref"])
        self.assertIn("GATE: PASS", self.to_review(jid))
        self.job("advance", jid, "review")
        self.reviews(jid, self.stage(jid)["round"], code="PASS")
        self.job("advance", jid, "approve")

    def finish(self, jid):
        """End whatever job is left so the next test has free slots."""
        j = self.stage(jid)
        if j["stage"] in {"needs-info", "needs-human"}:
            self.job("reject", jid, input="test cleanup")

    # ---- tests ---------------------------------------------------------------

    def test_one_slice_initiative_uses_refs_and_survives_added_slices(self):
        n, jid = self.start("One slice", ("S1", "slice", "Todo", "next"), checkpoint="auto")
        self.to_build(jid, "S1")
        self.assertEqual(self.stage(jid)["pr_ref"], "Refs")
        self.to_approve(jid, n, "v1\n")
        self.jobfile(jid, "decision.md", "APPROVE\n\n## Summary\n- ok\n")
        self.job("merge", jid)
        self.assertEqual(self.gh()["issues"][n]["state"], "OPEN")  # Refs: GitHub didn't close it
        # the replan learns something and adds a slice
        self.slices(jid, ("S1", "slice", "Todo", "merged"), ("S2", "slice", "Más", "next"))
        (self.init_dir(jid) / "replan.md").write_text("STATUS: CONTINUE\n\n## Resumen\nS2.\n")
        jid2 = self.job("replan", jid).stdout.strip().split()[-1]
        self.assertEqual(self.stage(jid2)["stage"], "spec")
        self.assertEqual([r[0] for r in self.queue() if r[1] == n], ["resume"])
        self.finish_spec_job(jid2)

    def test_direction_on_unmerged_slice_drops_it_and_closes_the_pr(self):
        n, jid = self.start("Direction", ("S1", "slice", "Uno", "next"), ("S2", "slice", "Dos", "idea"))
        self.to_build(jid, "S1")
        self.to_approve(jid, n, "v1\n")
        self.jobfile(jid, "decision.md", "ESCALATE\n\n## Why\nproduct decision\n")
        self.job("advance", jid, "needs-human")
        pr = str(self.stage(jid)["pr"])
        out = self.job("feedback", jid, "--direction", input="Better build S2 instead\n").stdout
        self.assertIn("→ replan", out)
        j = self.stage(jid)
        self.assertTrue(j["abandoned"])
        self.assertEqual(self.gh()["prs"][pr]["state"], "CLOSED")
        self.assertIn("dropped", (self.init_dir(jid) / "slices.md").read_text())
        self.assertFalse((self.repo / ".worktrees" / jid).exists())
        # replan CONTINUE → checkpoint → OK: the job ends rejected, never merged
        self.slices(jid, ("S1", "slice", "Uno", "dropped"), ("S2", "slice", "Dos", "next"))
        (self.init_dir(jid) / "replan.md").write_text("STATUS: CONTINUE\n\n## Resumen\nS2.\n")
        self.job("replan", jid)
        self.assertEqual(self.stage(jid)["stage"], "checkpoint")
        jid2 = self.job("checkpoint-ok", jid).stdout.strip().split()[-1]
        self.assertEqual(self.stage(jid)["stage"], "rejected")
        self.assertEqual(self.stage(jid2)["slice_id"], "S2")
        self.assertEqual(self.gh()["issues"][n]["state"], "OPEN")
        self.finish_spec_job(jid2)

    def finish_spec_job(self, jid):
        """Move a slice job in spec to needs-human through specgate and reject it."""
        for _ in range(3):
            self.jobfile(jid, "spec.md", "STATUS: READY\n\n# bad\n")
            self.job("spec-check", jid, ok=False)
        self.job("reject", jid, input="cleanup")

    def test_spec_escalation_goes_back_to_spec_with_feedback(self):
        n, jid = self.start("Bad spec", ("S1", "slice", "Uno", "next"))
        for _ in range(3):
            self.jobfile(jid, "spec.md", "STATUS: READY\n\n# S1\n")
            out = self.job("spec-check", jid, ok=False).stdout
        self.assertIn("needs-human", out)
        self.assertEqual(self.stage(jid)["stage"], "needs-human")
        # rework can't go to build: nothing was routed or built
        self.assertIn("rework goes to spec", self.job("advance", jid, "build", ok=False).stderr)
        out = self.job("feedback", jid, input="Split it by screen, 3 criteria each\n").stdout
        self.assertIn("→ spec", out)
        j = self.stage(jid)
        self.assertEqual((j["stage"], j["spec_failures"]), ("spec", 0))
        self.assertIn("Split it by screen", (self.repo / f"factory/jobs/{jid}/feedback-1.md").read_text())
        self.jobfile(jid, "spec.md", SPEC.format(status="READY", sid="S1", title="hacer algo", extra=""))
        self.assertIn("SPECGATE: PASS", self.job("spec-check", jid).stdout)
        self.finish_spec_job(jid)

    def test_auto_checkpoint_without_slot_parks_and_stalled_initiative_resumes(self):
        cfg = self.repo / "factory/config.json"
        saved = cfg.read_text()
        cfg.write_text(json.dumps({**json.loads(saved), "concurrency": 2}, indent=2))
        try:
            n, jid = self.start("Auto", ("S1", "slice", "Uno", "next"), ("S2", "slice", "Dos", "idea"),
                                checkpoint="auto")
            self.to_build(jid, "S1")
            self.to_approve(jid, n, "v1\n")
            self.jobfile(jid, "decision.md", "APPROVE\n\n## Summary\n- ok\n")
            self.job("merge", jid)
            # another issue takes the other slot
            m = self.sh("gh issue create --label factory --title Other --body x").stdout.strip().rsplit("/", 1)[1]
            other = self.job("create", m).stdout.strip()
            self.slices(jid, ("S1", "slice", "Uno", "merged"), ("S2", "slice", "Dos", "next"))
            (self.init_dir(jid) / "replan.md").write_text("STATUS: CONTINUE\n\n## Resumen\nS2.\n")
            # concurrency drops to 1 and `other` holds it: no slot for S2 → parked, not lost
            cfg.write_text(json.dumps({**json.loads(saved), "concurrency": 1}, indent=2))
            self.assertIn("no free slot", self.job("replan", jid).stdout)
            self.assertEqual(self.stage(jid)["stage"], "checkpoint")
            self.assertEqual(self.stage(jid)["merged"], True)
            self.assertEqual([r[0] for r in self.queue() if r[1] == n], ["hold"])
            self.assertIn("concurrency", self.job("checkpoint-ok", jid, ok=False).stderr)
            self.assertEqual(self.stage(jid)["stage"], "checkpoint")  # the refusal left it waiting
            self.jobfile(other, "intake.md", "DECISION: REJECT\nRISK: low\nSURFACES: none\nRESCOPED: no\n\n## Why\nx\n")
            self.job("advance", other, "rejected")
            self.assertEqual([r[0] for r in self.queue() if r[1] == n], ["resume"])
            jid2 = self.job("checkpoint-ok", jid).stdout.strip().split()[-1]
            self.assertEqual(self.stage(jid2)["slice_id"], "S2")

            # a next slice that never got a job: the queue shows it
            path = self.repo / f"factory/jobs/{jid2}/job.json"
            j2 = json.loads(path.read_text())
            j2["stage"] = "merged"
            path.write_text(json.dumps(j2))
            self.slices(jid, ("S1", "slice", "Uno", "merged"), ("S2", "slice", "Dos", "merged"),
                        ("S3", "slice", "Tres", "next"))
            rows = [r for r in self.queue() if r[1] == n]
            self.assertEqual([(r[0], r[3]) for r in rows], [("resume", "next-slice")], rows)
            jid3 = self.job("next-slice", jid).stdout.strip()
            self.assertEqual(self.stage(jid3)["slice_id"], "S3")
            self.finish_spec_job(jid3)
        finally:
            cfg.write_text(saved)


if __name__ == "__main__":
    unittest.main()
