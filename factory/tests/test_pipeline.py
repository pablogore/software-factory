"""Tests for factory/pipeline.js, the dashboard's pipeline graph. Run: python3 -m unittest discover factory/tests

Each test writes an example job folder in a temp dir, reads it like the dashboard
does (every .md file and its first line) and runs pipelineSteps() with node.
Without node on the PATH those tests are skipped.
"""

import json
import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent
PIPELINE = HERE.parent / "pipeline.js"
REVIEWERS = {"code": "haiku", "ux": "sonnet"}
NEEDS_NODE = unittest.skipUnless(shutil.which("node"), "needs node on the PATH to run factory/pipeline.js")


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


def loop(job):
    """reviewLoop(job): the reviewers → build loop, or None."""
    script = ("const { reviewLoop } = require(process.argv[1]);"
              "console.log(JSON.stringify(reviewLoop(JSON.parse(require('fs').readFileSync(0, 'utf8')))))")
    res = subprocess.run(["node", "-e", script, str(PIPELINE)], input=json.dumps(job),
                         capture_output=True, text=True, check=True)
    return json.loads(res.stdout)


def job(stage, rnd=0, reviewers=REVIEWERS, limit=3):
    return {"id": "7-demo", "stage": stage, "round": rnd, "round_limit": limit,
            "agents": {"reviewers": reviewers} if reviewers else None}


def table(out):
    return [(s["label"], s["state"], s["current"], s["doc"]) for s in out]


@NEEDS_NODE
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

    def test_demo_first_round_has_no_loop(self):
        # Round 1 runs every reviewer: none is skipped and there's no loop back to build.
        first = dict(job("review", 1), round_reviewers=None)
        out = steps(first, {"intake.md": "DECISION: ACCEPT\n", "spec.md": "STATUS: READY\n",
                            "build.md": "STATUS: READY\n", "round-1/checks.md": "GATE: PASS\n"})
        self.assertEqual([(s["label"], s["state"], s["current"]) for s in out[3:5]], [
            ("code", "running", True), ("ux", "running", True),
        ])
        self.assertIsNone(loop(first))

    def test_demo_rework_round_2_skips_a_reviewer(self):
        # ux passed in round 1, so round 2 re-runs only code: ux is skipped, not pending.
        rework = dict(job("review", 2), round_reviewers=["code"])
        out = steps(rework, {
            "intake.md": "DECISION: ACCEPT\n", "spec.md": "STATUS: READY\n", "build.md": "STATUS: READY\n",
            "round-1/checks.md": "GATE: PASS\n", "round-1/review-code.md": "VERDICT: CHANGES\n",
            "round-1/review-ux.md": "VERDICT: PASS\n", "round-2/checks.md": "GATE: PASS\n",
        })
        self.assertEqual(table(out)[3:5], [
            ("code", "running", True, None),
            ("ux", "skipped", False, None),
        ])
        self.assertEqual(loop(rework), {"round": 2, "limit": 3})


@NEEDS_NODE
class PipelineStatesTest(unittest.TestCase):
    def test_skipped_reviewer_stays_skipped_while_rebuilding(self):
        rework = dict(job("build", 2), round_reviewers=["code"])
        out = steps(rework, {"intake.md": "DECISION: ACCEPT\n", "spec.md": "STATUS: READY\n",
                             "build.md": "STATUS: READY\n", "round-1/review-ux.md": "VERDICT: PASS\n"})
        self.assertEqual([(s["label"], s["state"]) for s in out[3:5]], [("code", "pending"), ("ux", "skipped")])

    def test_loop_shows_the_jobs_own_limit(self):
        self.assertEqual(loop(job("build", 4, limit=6)), {"round": 4, "limit": 6})

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
            ("code", "running", True), ("ux", "skipped", False),
        ])

    def test_needs_info_is_changes_on_intake(self):
        out = steps(job("needs-info", reviewers=None), {"intake.md": "DECISION: NEEDS-INFO\n"})
        self.assertEqual((out[0]["state"], out[0]["current"]), ("changes", True))


class TestDemoPipelineWithoutNode(unittest.TestCase):
    def test_demo_without_node_pipeline_tests_are_skipped(self):
        tmp = tempfile.mkdtemp()
        try:
            env = dict(os.environ, PATH=tmp, PYTHONPATH=str(HERE), PYTHONDONTWRITEBYTECODE="1")
            res = subprocess.run([sys.executable, "-m", "unittest", "-v",
                                  "test_pipeline.TestDemoPipeline", "test_pipeline.PipelineStatesTest"],
                                 cwd=tmp, env=env, capture_output=True, text=True)
        finally:
            shutil.rmtree(tmp)
        self.assertEqual(res.returncode, 0, res.stderr)
        loader = unittest.TestLoader()
        count = sum(loader.loadTestsFromTestCase(c).countTestCases() for c in (TestDemoPipeline, PipelineStatesTest))
        self.assertIn(f"OK (skipped={count})", res.stderr)
        self.assertIn("skipped 'needs node on the PATH", res.stderr)
        self.assertNotIn(" ... ok", res.stderr)


if __name__ == "__main__":
    unittest.main()
