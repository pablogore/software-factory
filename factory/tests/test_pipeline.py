"""Tests for factory/pipeline.js, the dashboard's pipeline graph. Run: python3 -m unittest discover factory/tests

Each test writes an example job folder in a temp dir, reads it like the dashboard
does (every .md file and its first line) and runs pipelineSteps() with node.
"""

import json
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent
PIPELINE = HERE.parent / "pipeline.js"
REVIEWERS = {"code": "haiku", "ux": "sonnet"}


def steps(job, files):
    """pipelineSteps(job, firstLines) for a job folder with these files."""
    tmp = Path(tempfile.mkdtemp())
    try:
        folder = tmp / "jobs" / job["id"]
        for path, text in files.items():
            (folder / path).parent.mkdir(parents=True, exist_ok=True)
            (folder / path).write_text(text)
        (folder / "job.json").write_text(json.dumps(job))
        first = {str(p.relative_to(folder)): p.read_text().split("\n")[0].strip()
                 for p in folder.rglob("*.md")}
        script = ("const { pipelineSteps } = require(process.argv[1]);"
                  "const [job, first] = JSON.parse(require('fs').readFileSync(0, 'utf8'));"
                  "console.log(JSON.stringify(pipelineSteps(job, first)))")
        res = subprocess.run(["node", "-e", script, str(PIPELINE)], input=json.dumps([job, first]),
                             capture_output=True, text=True, check=True)
        return json.loads(res.stdout)
    finally:
        shutil.rmtree(tmp)


def job(stage, rnd=0, reviewers=REVIEWERS):
    return {"id": "7-demo", "stage": stage, "round": rnd, "agents": {"reviewers": reviewers} if reviewers else None}


def table(out):
    return [(s["label"], s["state"], s["current"], s["doc"]) for s in out]


class TestDemoPipeline(unittest.TestCase):
    def test_demo_job_in_intake(self):
        out = steps(job("intake", reviewers=None), {"issue.md": "# Issue\n"})
        self.assertEqual(table(out), [
            ("intake", "running", True, None),
            ("spec", "pending", False, None),
            ("build", "pending", False, None),
            ("approver", "pending", False, None),
        ])

    def test_demo_job_in_review_with_changes(self):
        out = steps(job("review", 1), {
            "intake.md": "DECISION: ACCEPT\n", "spec.md": "STATUS: READY\n", "build.md": "STATUS: READY\n",
            "round-1/checks.md": "GATE: PASS\n", "round-1/review-code.md": "VERDICT: CHANGES\n",
        })
        self.assertEqual(table(out), [
            ("intake", "ok", False, "intake.md"),
            ("spec", "ok", False, "spec.md"),
            ("build", "ok", False, "build.md"),
            ("code", "changes", False, "round-1/review-code.md"),
            ("ux", "running", True, None),
            ("approver", "pending", False, None),
        ])

    def test_demo_review_done_shows_each_verdict(self):
        # Every reviewer wrote its verdict and the job left review: each step shows its own.
        files = {"intake.md": "DECISION: ACCEPT\n", "spec.md": "STATUS: READY\n", "build.md": "STATUS: READY\n",
                 "round-1/checks.md": "GATE: PASS\n", "round-1/review-code.md": "VERDICT: CHANGES\n",
                 "round-1/review-ux.md": "VERDICT: PASS\n"}
        out = steps(job("triage", 1), files)
        self.assertEqual([(s["label"], s["state"], s["current"]) for s in out], [
            ("intake", "ok", False), ("spec", "ok", False), ("build", "ok", False),
            ("code", "changes", False), ("ux", "ok", False), ("approver", "pending", False),
        ])

    def test_demo_job_approved(self):
        out = steps(job("merged", 2), {
            "intake.md": "DECISION: ACCEPT\n", "spec.md": "STATUS: READY\n", "build.md": "STATUS: READY\n",
            "round-1/checks.md": "GATE: FAIL\n",
            "round-2/checks.md": "GATE: PASS\n", "round-2/review-code.md": "VERDICT: PASS\n",
            "round-2/review-ux.md": "VERDICT: PASS\n", "decision.md": "APPROVE\n",
        })
        self.assertEqual(table(out), [
            ("intake", "ok", False, "intake.md"),
            ("spec", "ok", False, "spec.md"),
            ("build", "ok", False, "build.md"),
            ("code", "ok", False, "round-2/review-code.md"),
            ("ux", "ok", False, "round-2/review-ux.md"),
            ("approver", "ok", False, "decision.md"),
        ])


class PipelineStatesTest(unittest.TestCase):
    def test_gate_failure_fails_build(self):
        out = steps(job("triage", 1), {"intake.md": "DECISION: ACCEPT\n", "spec.md": "STATUS: READY\n",
                                       "build.md": "STATUS: READY\n", "round-1/checks.md": "GATE: FAIL\n"})
        self.assertEqual(out[2]["state"], "failed")

    def test_rebuild_after_changes_runs_build_again(self):
        out = steps(job("build", 2), {"intake.md": "DECISION: ACCEPT\n", "spec.md": "STATUS: READY\n",
                                      "build.md": "STATUS: READY\n", "round-1/review-code.md": "VERDICT: CHANGES\n"})
        self.assertEqual([(s["label"], s["state"], s["current"]) for s in out[2:]], [
            ("build", "running", True), ("code", "pending", False), ("ux", "pending", False),
            ("approver", "pending", False),
        ])

    def test_blocked_build_and_escalation_fail(self):
        out = steps(job("needs-human", 1), {"intake.md": "DECISION: ACCEPT\n", "spec.md": "STATUS: READY\n",
                                            "build.md": "STATUS: BLOCKED\n", "decision.md": "ESCALATE\n"})
        self.assertEqual(out[2]["state"], "failed")
        self.assertEqual((out[-1]["state"], out[-1]["current"]), ("failed", True))

    def test_rework_round_runs_only_its_reviewers(self):
        rework = dict(job("review", 2), round_reviewers=["code"])
        out = steps(rework, {"intake.md": "DECISION: ACCEPT\n", "spec.md": "STATUS: READY\n",
                             "build.md": "STATUS: READY\n", "round-1/review-ux.md": "VERDICT: PASS\n",
                             "round-2/checks.md": "GATE: PASS\n"})
        self.assertEqual([(s["label"], s["state"], s["current"]) for s in out[3:5]], [
            ("code", "running", True), ("ux", "pending", False),
        ])

    def test_needs_info_is_changes_on_intake(self):
        out = steps(job("needs-info", reviewers=None), {"intake.md": "DECISION: NEEDS-INFO\n"})
        self.assertEqual((out[0]["state"], out[0]["current"]), ("changes", True))


if __name__ == "__main__":
    unittest.main()
