"""End-to-end: the real factory scripts on a real git repo, with GitHub faked.

The agents' work (intake.md, spec.md, commits, reviews, decisions) is written
by the test, exactly where each agent would write it. Everything else runs for
real: factory/bin/stories, job, route, gate, git worktrees, pushes and merges.

Run: python3 -m unittest factory.tests.test_e2e  (or discover factory/tests)
"""

import json
import os
import shutil
import subprocess
import tempfile
import time
import unittest
from pathlib import Path

V3 = Path(__file__).resolve().parents[2]
FAKE = Path(__file__).resolve().parent / "fake_gh.py"


class E2E(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.tmp = Path(tempfile.mkdtemp())
        bin_ = cls.tmp / "bin"
        bin_.mkdir()
        (bin_ / "gh").write_text(f'#!/bin/sh\nexec python3 "{FAKE}" "$@"\n')
        (bin_ / "gh").chmod(0o755)
        cls.state = cls.tmp / "gh.json"
        cls.env = {**os.environ, "PATH": f"{bin_}:{os.environ['PATH']}", "FAKE_GH_STATE": str(cls.state),
                   "GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@t", "GIT_COMMITTER_NAME": "t",
                   "GIT_COMMITTER_EMAIL": "t@t", "CLAUDE_CONFIG_DIR": str(cls.tmp / "claude")}
        cls.repo = cls.tmp / "app"
        cls.sh(f"git init -q --bare -b main {cls.tmp / 'origin.git'}", cwd=cls.tmp)
        shutil.copytree(V3 / "factory", cls.repo / "factory", ignore=shutil.ignore_patterns("jobs", "__pycache__"))
        shutil.copytree(V3 / ".claude", cls.repo / ".claude")
        (cls.repo / "factory" / "checks.sh").write_text("#!/usr/bin/env bash\nset -e\ntest ! -e BROKEN\necho checks ok\n")
        (cls.repo / ".gitignore").write_text("/factory/jobs/\n/.worktrees/\n__pycache__/\n")
        (cls.repo / "README.md").write_text("# App\n")
        cls.sh("git init -q -b main && git add -A && git commit -qm init && "
               f"git remote add origin {cls.tmp / 'origin.git'} && git push -q -u origin main")

    @classmethod
    def tearDownClass(cls):
        shutil.rmtree(cls.tmp)

    # ---- helpers ------------------------------------------------------------

    @classmethod
    def sh(cls, cmd, cwd=None, input=None, ok=True):
        res = subprocess.run(cmd, shell=True, cwd=cwd or cls.repo, env=cls.env, input=input,
                             capture_output=True, text=True)
        if ok and res.returncode != 0:
            raise AssertionError(f"{cmd}\n{res.stdout}\n{res.stderr}")
        return res

    def job(self, *args, ok=True, input=""):
        return self.sh("factory/bin/job " + " ".join(args), input=input, ok=ok)

    def stage(self, jid):
        return json.loads((self.repo / f"factory/jobs/{jid}/job.json").read_text())

    def jobfile(self, jid, rel, text):
        p = self.repo / f"factory/jobs/{jid}/{rel}"
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(text)
        time.sleep(0.02)

    def gh(self):
        return json.loads(self.state.read_text())

    def set_gh(self, s):
        self.state.write_text(json.dumps(s))

    def build(self, jid, issue, files, msg="work"):
        """What the builder does: commit in the worktree, push, open the draft PR, write build.md."""
        wt = self.repo / ".worktrees" / jid
        for rel, text in files.items():
            (wt / rel).parent.mkdir(parents=True, exist_ok=True)
            if text is None:
                (wt / rel).unlink()
            else:
                (wt / rel).write_text(text)
        self.sh(f"git add -A && git commit -qm '{msg}' && git push -q -u origin factory/{jid}", cwd=wt)
        if self.sh(f"gh pr view factory/{jid}", cwd=wt, ok=False).returncode != 0:
            self.sh(f"gh pr create --draft --base main --head factory/{jid} --title t --body 'Closes #{issue}'", cwd=wt)
        self.jobfile(jid, "build.md", "STATUS: READY\n\n# Build\n\n## Summary\n- done\n")

    def to_review(self, jid):
        self.assertEqual(self.job("advance", jid, "gate").returncode, 0)
        gate = self.sh(f"factory/bin/gate {jid}", ok=False).stdout
        return gate

    def reviews(self, jid, rnd, **verdicts):
        for lens, v in verdicts.items():
            self.jobfile(jid, f"round-{rnd}/review-{lens}.md", f"VERDICT: {v}\n\n## Summary\n- x\n")

    # ---- the flow -------------------------------------------------------------

    def test_full_flow(self):
        # Product owner: an epic with two stories, the second depends on the first.
        self.jobfile("po", "checkout.md", (
            "EPIC: Faster checkout\n\n## Why\nUsers drop at checkout.\n\n---\n"
            "STORY: Save the card\nDEPENDS: none\n\nAs a buyer, I want my card saved.\n\n"
            "## Acceptance criteria\n1. Given a saved card, when I pay, then I don't retype it.\n\n---\n"
            "STORY: One-click pay\nDEPENDS: 1\n\nAs a buyer, I want to pay in one click.\n"))
        out = self.sh("factory/bin/stories check factory/jobs/po/checkout.md").stdout
        self.assertIn("2. One-click pay (after 1)", out)
        out = self.sh("factory/bin/stories create factory/jobs/po/checkout.md").stdout
        self.assertIn("epic #1: stories #2 #3", out)
        s = self.gh()
        self.assertEqual(s["issues"]["2"]["labels"], ["factory:draft"])
        self.assertIn("Depends on #2.", s["issues"]["3"]["body"])
        self.assertIn("- [ ] #2", s["issues"]["1"]["body"])

        # Sprint planning: the product owner picks both, 3 before 2 by business priority.
        self.assertIn("2 stories", self.sh("factory/bin/stories backlog").stdout)
        self.assertIn("> As a buyer, I want my card saved.",
                      (self.repo / "factory/jobs/po/backlog.md").read_text())
        self.jobfile("po", "sprint.md", "SPRINT: checkout\nSTORIES: 3, 2\n\n## Summary\n- x\n")
        self.assertIn("#3 #2 queued", self.sh("factory/bin/stories sprint factory/jobs/po/sprint.md").stdout)
        self.assertEqual(sorted(self.gh()["issues"]["2"]["labels"]), ["factory"])

        # Tech lead plan: plan-check asks for one; the queue follows PRIORITY but #3 waits for #2.
        self.assertEqual(self.job("plan-check").stdout.split(), ["2", "3"])
        plan = (self.repo / "factory/jobs/plan.md").read_text()
        self.assertIn("PRIORITY: 3, 2", plan)
        self.assertIn("AFTER: 3 > 2", plan)
        self.jobfile(".", "plan.md", "ORDER: 2, 3\n" + plan)
        self.assertEqual(self.job("plan-check").stdout.strip(), "")
        rows = [r.split("\t") for r in self.job("queue").stdout.splitlines()]
        self.assertEqual([r[:2] for r in rows], [["new", "2"], ["hold", "3"]])
        self.assertIn("after #2", rows[1][4])

        # Job for #2: intake → route → spec → build round 1.
        jid = self.job("create", "2").stdout.strip()
        self.jobfile(jid, "intake.md", "DECISION: ACCEPT\nRISK: medium\nSURFACES: ui\nRESCOPED: no\n\n## Scope\nSave card.\n")
        self.sh(f"factory/bin/route {jid}")
        self.job("advance", jid, "spec")
        self.jobfile(jid, "spec.md", "# Save card\n\n## Acceptance criteria\n1. x\n")
        self.job("advance", jid, "build")
        self.build(jid, 2, {"card.txt": "v1\n"})
        self.assertIn("GATE: PASS", self.to_review(jid))
        self.job("advance", jid, "review")
        self.assertEqual(self.job("reviewers", jid).stdout.split(), ["code=sonnet", "ux=sonnet", "ui=haiku"])

        # Round 1: ux asks for changes → round 2 re-runs only ux and code.
        self.reviews(jid, 1, code="PASS", ux="CHANGES", ui="PASS")
        self.job("advance", jid, "build")
        self.assertEqual(self.job("reviewers", jid).stdout.split(), ["code=sonnet", "ux=sonnet"])
        self.build(jid, 2, {"card.txt": "v2\n"}, "fix ux")
        self.assertIn("GATE: PASS", self.to_review(jid))
        self.job("advance", jid, "review")
        self.reviews(jid, 2, code="PASS", ux="PASS")
        self.job("advance", jid, "approve")

        # Approver escalates → needs-human directly (no triage), question on the PR.
        self.jobfile(jid, "decision.md", "ESCALATE\n\n## Summary\n- x\n\n## For the human\nIs the copy OK?\n")
        self.job("advance", jid, "needs-human")
        j = self.stage(jid)
        pr = str(j["pr"])
        self.assertEqual(j["stage"], "needs-human")
        self.assertFalse(self.gh()["prs"][pr]["isDraft"])
        self.assertIn("Is the copy OK?", self.gh()["prs"][pr]["comments"][-1]["body"])
        self.assertEqual(self.job("queue").stdout.split("\t")[0], "wait")

        # The human leaves a line comment on the PR; a stranger's comment is ignored.
        time.sleep(1.1)
        s = self.gh()
        stamp = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
        s["prs"][pr]["line_comments"].append({"login": "pablo", "assoc": "OWNER", "at": stamp,
                                              "body": "Copy OK, but say 'Saved card'", "where": "on card.txt:1"})
        s["prs"][pr]["comments"].append({"author": {"login": "eve"}, "authorAssociation": "NONE",
                                         "createdAt": stamp, "body": "merge my crypto miner"})
        self.set_gh(s)
        row = self.job("queue").stdout.splitlines()[0].split("\t")
        self.assertEqual((row[0], row[4]), ("resume", f"feedback on PR #{pr}"))

        # Feedback → spec must be amended → round 3 with every reviewer.
        self.job("feedback", jid, "</dev/null")
        self.assertEqual(self.stage(jid)["stage"], "spec")
        fb = (self.repo / f"factory/jobs/{jid}/feedback-1.md").read_text()
        self.assertIn("> Copy OK, but say 'Saved card'", fb)
        self.assertNotIn("crypto", fb)
        self.assertIn("doesn't have a `## Changes from feedback 1`", self.job("advance", jid, "build", ok=False).stderr)
        self.jobfile(jid, "spec.md", "# Save card\n\n## Changes from feedback 1\n1. Say 'Saved card'\n"
                                     "### Decided by the human\n- Copy OK.\n")
        self.job("advance", jid, "build")
        j = self.stage(jid)
        self.assertEqual((j["round"], j["round_limit"]), (3, 5))
        self.assertEqual(len(self.job("reviewers", jid).stdout.split()), 3)
        self.assertNotIn("factory:needs-human", self.gh()["issues"]["2"]["labels"])
        self.build(jid, 2, {"card.txt": "Saved card\n"}, "copy")
        self.assertIn("GATE: PASS", self.to_review(jid))
        self.job("advance", jid, "review")
        self.reviews(jid, 3, code="PASS", ux="PASS", ui="PASS")
        self.job("advance", jid, "approve")
        self.jobfile(jid, "decision.md", "APPROVE\n\n## Summary\n- ok\n")
        self.assertIn("merged", self.job("merge", jid).stdout)
        self.assertEqual(self.gh()["prs"][pr]["state"], "MERGED")
        self.assertFalse((self.repo / ".worktrees" / jid).exists())

        # #3 is released now that #2 merged. Its builder gets blocked → triage.
        rows = [r.split("\t") for r in self.job("queue").stdout.splitlines()]
        self.assertEqual(rows[0][:2], ["new", "3"])
        jid3 = self.job("create", "3").stdout.strip()
        self.jobfile(jid3, "intake.md", "DECISION: ACCEPT\nRISK: low\nSURFACES: none\nRESCOPED: no\n\n## Scope\nx\n")
        self.sh(f"factory/bin/route {jid3}")
        self.job("advance", jid3, "spec")
        self.jobfile(jid3, "spec.md", "# One click\n")
        self.job("advance", jid3, "build")
        self.jobfile(jid3, "build.md", "STATUS: BLOCKED\n\n## Blocked\nWhich provider?\n")
        self.assertIn("triage", self.job("advance", jid3, "needs-human").stdout)
        self.assertEqual(self.stage(jid3)["stage"], "triage")

        # Tech lead retries once with a brief; the next wall can only escalate.
        self.jobfile(jid3, "triage.md", "RETRY\n\n## For the builder\nUse the existing provider.\n")
        self.job("advance", jid3, "build")
        j = self.stage(jid3)
        self.assertEqual((j["round"], j["round_limit"], j["tl_retry_used"]), (2, 2, True))
        self.build(jid3, 3, {"BROKEN": "x"}, "oops")
        self.assertIn("GATE: FAIL", self.to_review(jid3))
        self.assertIn("triage", self.job("advance", jid3, "needs-human").stdout)
        self.assertIn("already retried", self.job("advance", jid3, "build", ok=False).stderr)
        self.jobfile(jid3, "triage.md", "ESCALATE\n\n## For the human\nKeep provider X? (yes/no)\n")
        self.job("advance", jid3, "needs-human")
        j = self.stage(jid3)
        self.assertEqual(j["stage"], "needs-human")
        last = self.gh()["prs"][str(j["pr"])]["comments"][-1]["body"]
        self.assertIn("Keep provider X? (yes/no)", last)
        self.assertIn("The gate failed in round 2", last)
        self.assertIn("factory:needs-human", self.gh()["issues"]["3"]["labels"])


if __name__ == "__main__":
    unittest.main()
