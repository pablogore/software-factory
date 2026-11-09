"""One builder per job (`job claim --spawn|--resume` / `job release`) and `job doctor`'s orphan worktrees.

The commands run for real on a temporary copy of factory/; the builders are
real processes (`sleep`), alive or already reaped.
"""

import contextlib
import io
import json
import os
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

from test_job import JobBase

SRC = Path(__file__).resolve().parents[1]


class TestDemoBuilderLock(unittest.TestCase):
    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp()).resolve()  # the script resolves its path: /private/var on macOS
        shutil.copytree(SRC, self.tmp / "factory", ignore=shutil.ignore_patterns("tests", "jobs", "__pycache__"))
        self.procs = []

    def tearDown(self):
        for p in self.procs:
            p.kill()
            p.wait()
        shutil.rmtree(self.tmp)

    def add_job(self, job_id, stage):
        d = self.tmp / "factory" / "jobs" / job_id
        d.mkdir(parents=True)
        (d / "job.json").write_text(json.dumps({
            "id": job_id, "issue": int(job_id[:3]), "title": "t", "stage": stage, "round": 1,
            "branch": f"factory/{job_id}", "worktree": str(self.tmp / ".worktrees" / job_id),
            "pr": None, "reviews": {}, "cost_usd": 0.0, "started": "2026-11-08T00:00:00Z", "ended": None}))

    def job(self, *args):
        return subprocess.run([str(self.tmp / "factory" / "bin" / "job"), *map(str, args)],
                              cwd=self.tmp, capture_output=True, text=True)

    def live_builder(self):
        p = subprocess.Popen(["sleep", "60"])
        self.procs.append(p)
        return p.pid

    def dead_builder(self):
        p = subprocess.Popen(["true"])
        p.wait()
        return p.pid

    def lock(self, job_id):
        return json.loads((self.tmp / "factory/jobs" / job_id / "builder.lock").read_text())

    def test_demo_builder_lock_second_spawn_from_the_same_session_is_refused(self):
        self.add_job("001-a", "build")
        session = self.live_builder()
        self.assertEqual(self.job("claim", "001-a", "--spawn", session).returncode, 0)
        res = self.job("claim", "001-a", "--spawn", session)
        self.assertEqual(res.returncode, 1)
        self.assertIn(f"builder-001-a already has this job (session pid {session}", res.stderr)
        self.assertEqual((self.lock("001-a")["pid"], self.lock("001-a")["builder"]), (session, "builder-001-a"))

    def test_demo_builder_lock_spawn_from_another_live_session_is_refused(self):
        self.add_job("001-a", "build")
        first, second = self.live_builder(), self.live_builder()
        self.assertEqual(self.job("claim", "001-a", "--spawn", first).returncode, 0)
        self.assertEqual(self.job("claim", "001-a", "--spawn", second).returncode, 1)
        self.assertEqual(self.lock("001-a")["pid"], first)

    def test_demo_builder_lock_resume_is_accepted_only_for_its_builder(self):
        self.add_job("001-a", "build")
        self.assertEqual(self.job("claim", "001-a", "--spawn", self.live_builder()).returncode, 0)
        res = self.job("claim", "001-a", "--resume", "builder-001-a")
        self.assertEqual(res.returncode, 0, res.stderr)
        res = self.job("claim", "001-a", "--resume", "builder-002-b")
        self.assertEqual(res.returncode, 1)
        self.assertIn("builder-002-b doesn't hold the builder lock (held by builder-001-a)", res.stderr)

    def test_demo_builder_lock_resume_without_a_lock_is_refused(self):
        self.add_job("001-a", "build")
        res = self.job("claim", "001-a", "--resume", "builder-001-a")
        self.assertEqual(res.returncode, 1)
        self.assertIn("no live builder holds it", res.stderr)

    def test_demo_builder_lock_dead_session_lock_is_taken_by_a_new_spawn(self):
        self.add_job("001-a", "build")
        dead = self.dead_builder()
        self.assertEqual(self.job("claim", "001-a", "--spawn", dead).returncode, 0)
        self.assertEqual(self.job("claim", "001-a", "--resume", "builder-001-a").returncode, 1)
        session = self.live_builder()
        res = self.job("claim", "001-a", "--spawn", session)
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertIn("the session that held the builder lock is gone", res.stderr)
        self.assertEqual(self.lock("001-a")["pid"], session)

    def test_demo_builder_lock_old_pid_form_is_refused(self):
        self.add_job("001-a", "build")
        res = self.job("claim", "001-a", self.live_builder())
        self.assertEqual(res.returncode, 1)
        self.assertIn("bad arguments", res.stderr)
        self.assertFalse((self.tmp / "factory/jobs/001-a/builder.lock").exists())

    def test_demo_builder_lock_release_frees_the_job_for_the_next_spawn(self):
        self.add_job("001-a", "build")
        first, second = self.live_builder(), self.live_builder()
        self.assertEqual(self.job("claim", "001-a", "--spawn", first).returncode, 0)
        self.assertEqual(self.job("release", "001-a").returncode, 0)
        self.assertFalse((self.tmp / "factory/jobs/001-a/builder.lock").exists())
        self.assertEqual(self.job("claim", "001-a", "--spawn", second).returncode, 0)

    def test_demo_doctor_lists_orphan_worktrees_and_deletes_nothing(self):
        self.add_job("001-open", "build")
        self.add_job("002-merged", "merged")
        wt = self.tmp / ".worktrees"
        for name in ("001-open", "002-merged", "003-no-job"):
            (wt / name).mkdir(parents=True)
            (wt / name / "README.md").write_text("x\n")
        res = self.job("doctor")
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertEqual(res.stdout.splitlines(), [f"orphan\t{wt / '002-merged'}", f"orphan\t{wt / '003-no-job'}"])
        self.assertIn("nothing was deleted", res.stderr)
        self.assertEqual(sorted(os.listdir(wt)), ["001-open", "002-merged", "003-no-job"])

    def test_demo_doctor_without_worktrees_lists_nothing(self):
        self.add_job("001-a", "build")
        res = self.job("doctor")
        self.assertEqual((res.returncode, res.stdout), (0, ""))


class TestDemoLeavingBuildFreesTheLock(JobBase):
    def test_demo_builder_lock_blocked_builder_leaving_build_frees_it(self):
        j = self.make("build", pr=None)
        self.write("build.md", "STATUS: BLOCKED\n\n## Blocked\nno env\n")
        with contextlib.redirect_stdout(io.StringIO()):
            self.job.cmd_claim_spawn(self.job.load(j["id"]), os.getpid())
        lock = self.job.builder_lock(j["id"])
        self.assertTrue(lock.exists())
        self.job.advance(self.job.sync(j), "needs-human")
        self.assertFalse(lock.exists())


if __name__ == "__main__":
    unittest.main()
