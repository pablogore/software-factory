"""`job replan` accepts a group of 2 to 3 `next` slices marked `[parallel: ...]`
with disjoint files, and says why when the group isn't valid. Every path that
starts the next slice starts a job per slice of the group, and the group
replans once, after its last slice closes.

Run: python3 -m unittest discover -s factory/tests -p 'test_*.py' -k parallel
"""

import shutil
import subprocess
import tempfile
import time
import unittest
from pathlib import Path

from test_e2e import E2EBase, SLICES, SPEC

GATE = Path(__file__).resolve().parents[1] / "bin" / "specgate"
INTAKE = "DECISION: ACCEPT\nRISK: low\nSURFACES: none\nRESCOPED: no\n\n## Scope\nx\n"


def rows(*rows):
    return SLICES.format(rows="".join(f"| {i} | slice | {v} | {st} |\n" for i, v, st in rows))


class ParallelSpecgateTest(unittest.TestCase):
    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp())

    def tearDown(self):
        shutil.rmtree(self.tmp)

    def errors(self, *slices):
        f = self.tmp / "slices.md"
        f.write_text(rows(*slices))
        out = subprocess.run([str(GATE), "slices", str(f)], capture_output=True, text=True).stdout
        return [l[2:] for l in out.splitlines() if l.startswith("- ")]

    def test_parallel_group_of_two_or_three_with_disjoint_files_passes(self):
        self.assertEqual(self.errors(("S1", "Uno [parallel: factory/bin/job]", "next"),
                                     ("S2", "Dos [parallel: `factory/bin/specgate`, factory/tests/]", "next")), [])
        self.assertEqual(self.errors(("S1", "Uno [parallel: a/b]", "next"), ("S2", "Dos [parallel: a/bc]", "next"),
                                     ("S3", "Tres [parallel: c]", "next"), ("S4", "Idea", "idea")), [])

    def test_parallel_slices_sharing_files_name_both_slices_and_files(self):
        errs = self.errors(("S1", "Uno [parallel: factory/bin/job, x.md]", "next"),
                           ("S2", "Dos [parallel: factory/bin/, y.md]", "next"),
                           ("S3", "Tres [parallel: ./x.md]", "next"))
        self.assertEqual(errs, ["parallel slices S1 and S2 share files: factory/bin/job (S1) / factory/bin (S2)",
                                "parallel slices S1 and S3 share files: x.md"])

    def test_parallel_more_than_three_or_without_files_is_refused(self):
        four = [(f"S{i}", f"V [parallel: f{i}]", "next") for i in range(1, 5)]
        self.assertEqual(self.errors(*four), ["4 `parallel` slices are `next`; the maximum is 3"])
        errs = self.errors(("S1", "Uno [parallel: a]", "next"), ("S2", "Dos [parallel:]", "next"))
        self.assertIn("S2: `parallel` without files; list them as `[parallel: path, path]`", errs)
        self.assertIn("S3: `parallel` without files", " ".join(self.errors(
            ("S1", "Uno", "next"), ("S3", "Tres [parallel]", "idea"))))

    def test_parallel_two_next_where_one_isnt_parallel_is_refused(self):
        errs = self.errors(("S1", "Uno [parallel: a]", "next"), ("S2", "Dos", "next"))
        self.assertEqual(errs, ["exactly one `next` is required, found 2; 2 to 3 can be `next` together "
                                "only if all are `parallel` (not `parallel`: S2)"])

    def test_parallel_absent_single_next_passes_as_today(self):
        self.assertEqual(self.errors(("S1", "Uno", "next"), ("S2", "Dos", "idea")), [])
        self.assertEqual(self.errors(("S1", "Uno", "idea")), ["exactly one `next` is required, found 0"])


class TestDemoParallelReplan(E2EBase):
    """The real `job replan` on a merged slice: every slices.md the spec-writer
    could write, valid or not. A refusal leaves the job in replan."""

    def init_dir(self, jid):
        return self.repo / "factory/initiatives" / jid

    def slices(self, jid, *slices):
        (self.init_dir(jid) / "slices.md").write_text(rows(*slices))
        (self.init_dir(jid) / "replan.md").write_text("STATUS: CONTINUE\n\n## Resumen\nx\n")
        time.sleep(0.02)

    def merged_slice(self):
        """Issue → initiative → S1 built, approved and merged: the job is in replan."""
        n = self.sh("gh issue create --label factory --title 'Parallel' --body x").stdout.strip().rsplit("/", 1)[1]
        jid = self.job("create", n).stdout.strip()
        self.jobfile(jid, "intake.md", INTAKE)
        self.job("advance", jid, "initiative")
        (self.init_dir(jid) / "vision.md").write_text("STATUS: READY\n\n# V\n")
        (self.init_dir(jid) / "context.md").write_text("# Contexto\n")
        (self.init_dir(jid) / "slices.md").write_text(rows(("S1", "Uno", "next"), ("S2", "Dos", "idea")))
        self.job("advance", jid, "spec")
        self.jobfile(jid, "spec.md", SPEC.format(status="READY", sid="S1", title="hacer algo", extra=""))
        self.job("advance", jid, "rate")
        self.jobfile(jid, "intake.md", INTAKE)
        self.sh(f"factory/bin/route {jid}")
        self.job("advance", jid, "build")
        self.build(jid, int(n), {f"{jid}.txt": "v1\n"}, ref=self.stage(jid)["pr_ref"])
        self.assertIn("GATE: PASS", self.to_review(jid))
        self.job("advance", jid, "review")
        self.reviews(jid, self.stage(jid)["round"], code="PASS")
        self.job("advance", jid, "approve")
        self.jobfile(jid, "decision.md", "APPROVE\n\n## Summary\n- ok\n")
        self.job("merge", jid)
        self.assertEqual(self.stage(jid)["stage"], "replan")
        return jid

    def refused(self, jid, *slices):
        self.slices(jid, *slices)
        err = self.job("replan", jid, ok=False).stderr
        self.assertEqual(self.stage(jid)["stage"], "replan")
        return err

    def test_demo_parallel_replan(self):
        jid = self.merged_slice()
        done = ("S1", "Uno", "merged")

        # 2. two parallel slices that share files: refused, naming slices and files
        err = self.refused(jid, done, ("S2", "Dos [parallel: factory/bin/job]", "next"),
                           ("S3", "Tres [parallel: factory/bin]", "next"))
        self.assertIn("parallel slices S2 and S3 share files: factory/bin/job (S2) / factory/bin (S3)", err)

        # 3. more than 3 parallel slices, or one without files: refused with the reason
        err = self.refused(jid, done, *[(f"S{i}", f"V [parallel: f{i}.py]", "next") for i in range(2, 6)])
        self.assertIn("4 `parallel` slices are `next`; the maximum is 3", err)
        err = self.refused(jid, done, ("S2", "Dos [parallel: a.py]", "next"), ("S3", "Tres [parallel]", "next"))
        self.assertIn("S3: `parallel` without files", err)

        # 1. three parallel slices with disjoint files: accepted
        self.slices(jid, done, ("S2", "Dos [parallel: factory/bin/job]", "next"),
                    ("S3", "Tres [parallel: factory/bin/specgate, factory/tests/test_parallel.py]", "next"),
                    ("S4", "Cuatro [parallel: .claude/]", "next"))
        self.assertIn("replan CONTINUE → checkpoint", self.job("replan", jid).stdout)
        self.assertEqual(self.stage(jid)["stage"], "checkpoint")

    def test_demo_parallel_absent_single_next_replans_as_today(self):
        # 4. one `next` and no `parallel`: CONTINUE → checkpoint, as before
        jid = self.merged_slice()
        self.slices(jid, ("S1", "Uno", "merged"), ("S2", "Dos", "next"))
        self.assertIn("replan CONTINUE → checkpoint", self.job("replan", jid).stdout)
        self.assertEqual(self.stage(jid)["stage"], "checkpoint")


class ParallelGroupBase(E2EBase):
    def init_dir(self, jid):
        return self.repo / "factory/initiatives" / jid

    def replan_files(self, init, *slices, status="CONTINUE"):
        (self.init_dir(init) / "slices.md").write_text(rows(*slices))
        (self.init_dir(init) / "replan.md").write_text(f"STATUS: {status}\n\n## Resumen\nx\n")
        time.sleep(0.02)

    def initiative(self, *slices):
        """Issue → intake → initiative with these slices → spec. Returns (issue, first job)."""
        n = self.sh("gh issue create --label factory --title 'Group' --body x").stdout.strip().rsplit("/", 1)[1]
        jid = self.job("create", n).stdout.strip()
        self.jobfile(jid, "intake.md", INTAKE)
        self.job("advance", jid, "initiative")
        (self.init_dir(jid) / "vision.md").write_text("STATUS: READY\n\n# V\n")
        (self.init_dir(jid) / "context.md").write_text("# Contexto\n")
        (self.init_dir(jid) / "slices.md").write_text(rows(*slices))
        self.job("advance", jid, "spec")
        return n, jid

    def ship(self, jid, n):
        """spec → rate → build → gate → review → approve → merge, as the agents would."""
        sid = self.stage(jid)["slice_id"]
        self.jobfile(jid, "spec.md", SPEC.format(status="READY", sid=sid, title="hacer algo", extra=""))
        self.job("advance", jid, "rate")
        self.jobfile(jid, "intake.md", INTAKE)
        self.sh(f"factory/bin/route {jid}")
        self.job("advance", jid, "build")
        self.build(jid, int(n), {f"{sid}.txt": "v1\n"}, ref="Refs")
        self.assertIn("GATE: PASS", self.to_review(jid))
        self.job("advance", jid, "review")
        self.reviews(jid, self.stage(jid)["round"], code="PASS")
        self.job("advance", jid, "approve")
        self.jobfile(jid, "decision.md", "APPROVE\n\n## Summary\n- ok\n")
        self.assertIn("merged", self.job("merge", jid).stdout)
        return self.stage(jid)["stage"]

    def group_of(self, ids, *sids):
        """Every job is in spec on its own slice, and each one names the whole group."""
        for jid, sid in zip(ids, sids):
            j = self.stage(jid)
            self.assertEqual((j["stage"], j["slice_id"], j["group"]), ("spec", sid, ids))
            self.assertTrue((self.repo / ".worktrees" / jid).exists())


class TestDemoParallelGroup(ParallelGroupBase):
    def test_demo_parallel_group_starts_together_and_replans_once(self):
        # 1. initiative → spec with 2 `parallel` next: a job per slice, not only the first
        n, s1 = self.initiative(("S1", "Uno [parallel: S1.txt]", "next"), ("S2", "Dos [parallel: S2.txt]", "next"),
                                ("S3", "Tres", "idea"))
        group = self.stage(s1)["group"]
        self.assertEqual(len(group), 2)
        self.group_of(group, "S1", "S2")
        s2 = group[1]
        states = [l.split("|")[4].strip() for l in (self.init_dir(s1) / "slices.md").read_text().splitlines()[4:]]
        self.assertEqual(states, ["in-progress", "in-progress", "idea"])

        # 2. S1 merges while S2 is open: S1 ends merged, no replan runs
        self.assertEqual(self.ship(s1, n), "merged")
        self.assertIn("not replan", self.job("replan", s1, ok=False).stderr)
        self.assertNotIn("replan", [r.split("\t")[3] for r in self.job("queue").stdout.splitlines()])
        # 3. S2, the last one, merges: one replan, reading both jobs
        self.assertEqual(self.ship(s2, n), "replan")
        self.assertEqual(self.job("replan-jobs", s2).stdout.split(), [s1, s2])
        self.job("direction", s2)

        # 1. checkpoint-ok with 3 `parallel` next: three jobs
        self.replan_files(s1, ("S1", "Uno", "merged"), ("S2", "Dos", "merged"),
                          ("S3", "Tres [parallel: S3.txt]", "next"), ("S4", "Cuatro [parallel: S4.txt]", "next"),
                          ("S5", "Cinco [parallel: S5.txt]", "next"))
        self.assertIn("checkpoint", self.job("replan", s2).stdout)
        out = self.job("checkpoint-ok", s1).stdout
        self.assertEqual(self.stage(s2)["stage"], "merged")
        group = out.split("next slice jobs ")[1].split()
        self.assertEqual(len(group), 3)
        self.group_of(group, "S3", "S4", "S5")

        # 2-3. with three: no replan until the third closes, then one for all three
        self.job("set-checkpoint", s1, "auto")
        self.assertEqual([self.ship(j, n) for j in group], ["merged", "merged", "replan"])
        self.assertEqual(self.job("replan-jobs", group[2]).stdout.split(), group)

        # 1. checkpoint auto: the replan itself starts a job per slice of the group
        self.replan_files(s1, *[(f"S{i}", "V", "merged") for i in range(1, 6)],
                          ("S6", "Seis [parallel: S6.txt]", "next"), ("S7", "Siete [parallel: S7.txt]", "next"))
        out = self.job("replan", group[2]).stdout
        self.assertEqual(self.stage(group[2])["stage"], "merged")
        self.group_of(out.split("next slice jobs ")[1].split(), "S6", "S7")


class TestDemoParallelEscalated(ParallelGroupBase):
    def test_demo_parallel_slice_in_needs_human_counts_as_closed(self):
        n, s1 = self.initiative(("S1", "Uno [parallel: S1.txt]", "next"), ("S2", "Dos [parallel: S2.txt]", "next"))
        s2 = self.stage(s1)["group"][1]
        self.assertEqual(self.ship(s1, n), "merged")
        self.assertEqual(self.stage(s1)["stage"], "merged")
        # S2 escalates: the group is closed, so the merged S1 runs the group's replan
        self.job("advance", s2, "needs-human")
        self.assertEqual(self.stage(s2)["stage"], "needs-human")
        self.assertEqual(self.stage(s1)["stage"], "replan")
        rows_ = [r.split("\t") for r in self.job("queue").stdout.splitlines()]
        self.assertIn(["resume", n, s1, "replan"], [r[:4] for r in rows_])
        self.assertEqual(self.job("replan-jobs", s1).stdout.split(), [s1, s2])
        self.replan_files(s1, ("S1", "Uno", "merged"), ("S2", "Dos", "in-progress"), status="DONE")
        self.assertIn("DONE", self.job("replan", s1).stdout)
        self.assertEqual(self.stage(s1)["stage"], "merged")


class TestDemoParallelAbsent(ParallelGroupBase):
    def test_demo_parallel_absent_one_next_starts_one_job_and_replans_after_it(self):
        # 4. one `next`, no `parallel`: one job, no group, replan right after its merge
        n, s1 = self.initiative(("S1", "Uno", "next"), ("S2", "Dos", "idea"))
        self.assertNotIn("group", self.stage(s1))
        self.assertEqual(len([p for p in (self.repo / "factory/jobs").iterdir() if (p / "job.json").exists()]), 1)
        self.assertEqual(self.ship(s1, n), "replan")
        self.assertEqual(self.job("replan-jobs", s1).stdout.split(), [s1])
        self.replan_files(s1, ("S1", "Uno", "merged"), ("S2", "Dos", "next"))
        self.assertIn("replan CONTINUE → checkpoint", self.job("replan", s1).stdout)
        out = self.job("checkpoint-ok", s1).stdout.strip()
        self.assertIn("→ next slice job ", out)
        s2 = out.split()[-1]
        j = self.stage(s2)
        self.assertEqual((j["stage"], j["slice_id"]), ("spec", "S2"))
        self.assertNotIn("group", j)


if __name__ == "__main__":
    unittest.main()
