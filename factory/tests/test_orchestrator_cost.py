"""Tests for the orchestrator's cost in `job summary`. Run: python3 -m pytest factory/tests -k orchestrator

Each test copies factory/ into a temporary repo, points CLAUDE_CONFIG_DIR at a
temporary folder, and writes sample transcripts there: orchestrator sessions
(<session>.jsonl) next to their subagents (<session>/subagents/agent-*.jsonl).
"""

import contextlib
import io
import json
import os
import shutil
import tempfile
import unittest
from pathlib import Path
from unittest import mock

from test_job import SRC, load_job_module


def assistant(msg_id, model, **usage):
    return {"type": "assistant", "message": {"id": msg_id, "model": model, "usage": usage}}


class OrchestratorCostTest(unittest.TestCase):
    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp())
        shutil.copytree(SRC, self.tmp / "factory", ignore=shutil.ignore_patterns("tests", "jobs"))
        env = mock.patch.dict(os.environ, {"CLAUDE_CONFIG_DIR": str(self.tmp / "claude")})
        env.start()
        self.addCleanup(env.stop)
        self.job = load_job_module(self.tmp)
        self.project = self.job.transcripts_dir()
        self.project.mkdir(parents=True)

    def tearDown(self):
        shutil.rmtree(self.tmp)

    def make(self, job_id, issue):
        j = {"id": job_id, "issue": issue, "title": "t", "issue_url": "", "pr": None, "pr_url": "",
             "branch": f"factory/{job_id}", "worktree": str(self.tmp / ".worktrees" / job_id),
             "stage": "build", "round": 0, "round_limit": 3, "intake": None, "agents": {},
             "gate": None, "reviews": {}, "cost_usd": 0.0, "initiative_id": None,
             "started": "2026-11-10T00:00:00Z", "ended": "2026-11-10T00:10:00Z"}
        self.job.job_dir(job_id).mkdir(parents=True)
        self.job.save(j)

    def transcript(self, path, entries):
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text("".join(json.dumps(e) + "\n" for e in entries))

    def agent(self, session, name, job_id, *entries):
        prompt = {"type": "user", "message": {"role": "user", "content": f"Job folder: {self.job.job_dir(job_id)}/. Build."}}
        self.transcript(self.project / session / "subagents" / f"agent-{name}.jsonl", [prompt, *entries])

    def orchestrator(self, session, *entries):
        prompt = {"type": "user", "message": {"role": "user", "content": "/factory run"}}
        self.transcript(self.project / f"{session}.jsonl", [prompt, *entries])

    def summary(self, *args):
        out = io.StringIO()
        with contextlib.redirect_stdout(out):
            self.job.cmd_summary(list(args))
        return out.getvalue().splitlines()

    def sample(self):
        """Two listed jobs share session `shared`; `solo` orchestrates only the
        first; `other` orchestrates a job that isn't listed. Opus: $4/Mtok in,
        $20/Mtok out; sonnet: $2/Mtok in."""
        self.make("001-one", 1)
        self.make("002-two", 2)
        self.make("003-three", 3)
        sonnet, opus = "claude-sonnet-5-5", "claude-opus-5-5"
        self.agent("shared", "a1", "001-one", assistant("m1", sonnet, input_tokens=500_000))
        self.agent("shared", "a2", "002-two", assistant("m2", sonnet, input_tokens=500_000))
        self.agent("solo", "a3", "001-one", assistant("m3", sonnet, input_tokens=250_000))
        self.agent("other", "a4", "003-three", assistant("m4", sonnet, input_tokens=250_000))
        # A streamed message repeats its id; it counts once, with its last usage.
        self.orchestrator("shared", assistant("o1", opus, input_tokens=1),
                          assistant("o1", opus, input_tokens=1_000_000))
        self.orchestrator("solo", assistant("o2", opus, output_tokens=100_000))
        self.orchestrator("other", assistant("o3", opus, input_tokens=2_500_000))

    def test_demo_shared_orchestrator_counts_once_apart_from_agents(self):
        self.sample()
        out = self.summary("001-one", "002-two")
        self.assertEqual(out[-1], "total agents $2.50, orchestrator $6.00 (API-equivalent)", out)

    def test_orchestrator_cost_counts_each_session_once(self):
        self.sample()
        jobs = [self.job.load(i) for i in ("001-one", "002-two")]
        self.assertEqual(self.job.orchestrator_cost(jobs), 6.0)
        self.assertEqual(self.job.orchestrator_cost(jobs[1:]), 4.0)

    def test_per_job_cost_counts_only_agents(self):
        self.sample()
        self.assertEqual(self.job.cost_usd(self.job.load("001-one")), 1.5)
        self.assertEqual(self.job.cost_usd(self.job.load("002-two")), 1.0)
        rows = {line.split()[0]: line.split()[5] for line in self.summary()[1:-1]}
        self.assertEqual(rows, {"001-one": "$1.50", "002-two": "$1.00", "003-three": "$0.50"})

    def test_summary_of_every_job_adds_every_orchestrator(self):
        self.sample()
        self.assertEqual(self.summary()[-1], "total agents $3.00, orchestrator $16.00 (API-equivalent)")

    def test_session_without_its_transcript_costs_nothing(self):
        self.make("001-one", 1)
        self.agent("gone", "a1", "001-one", assistant("m1", "claude-sonnet-5-5", input_tokens=500_000))
        self.assertEqual(self.summary()[-1], "total agents $1.00, orchestrator $0.00 (API-equivalent)")


if __name__ == "__main__":
    unittest.main()
