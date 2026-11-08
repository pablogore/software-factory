# Running the factory on Ork

[Ork](https://github.com/mimshins/ork) can run the factory's pipeline instead of
the `/factory` skill. The roles, their criteria, `specgate` and
`factory/checks.sh` stay the same; Ork runs the steps: worktrees, the review
loop, budgets, resuming, and a live view of each run.

This is a trial. It covers one issue as one slice: no initiatives, no replan.

## What it adds to a project

- `.ork/workflows/factory-issue.ork.yaml`: the pipeline. Intake → spec → specgate →
  builder → `factory/checks.sh` → reviewers (code always, security when intake
  says so, ux and ui on UI changes) → up to 3 fix rounds → approver. Approved
  work is pushed and opened as a pull request that closes the issue, and you
  confirm the merge. Escalated work is opened as a pull request, commented and
  labeled `factory:needs-human`.
- `.ork/agents/`: short Ork versions of the factory's agents. Each one reads its
  criteria from the matching `.claude/agents/*.md`, so the rules live in one
  place.

The project needs the factory installed as [INSTALL.md](../INSTALL.md) says,
with a real `factory/checks.sh` that passes on `main`.

## Run it from Claude Code

In a Claude Code session in the project, `/factory-ork run 42`. The session is
the orchestrator: it starts the run in the background, follows it, asks you in
the chat whenever Ork needs a decision, and reports the result. `ork ui` is for
watching. `/factory-ork status` and `/factory-ork stop` do what they say.

## Run it by hand

You need Node 24.20+, git, an authenticated `gh`, and your `claude` login.

```sh
npm install --global @krapht/ork
ork doctor
ork validate .ork/workflows/factory-issue.ork.yaml
ork run factory-issue --input issue=42
# a repo whose checks live elsewhere:
ork run factory-issue --input issue=42 --input checks="python3 -m unittest discover factory/tests"
ork ui          # the run in your browser: graph, logs, costs, questions
```

Ork asks in the terminal (or in `ork ui`, or with `ork decide`) when the loop
runs out of rounds, a check keeps failing, or the merge is ready. It never
pushes before the approver; the run's branch is `ork/<run>/builder`.

## Limits for now

- One issue is one slice: a large issue isn't split into an initiative.
- The merge waits for your answer. To merge on approve without asking, remove
  the `merge` gate and point the `pr → merge` edge at `do-merge`.
- Reviewer models are fixed per agent (`.ork/agents/*.agent.yaml`), not routed
  by risk as `config.json` does.
- Every run is a separate session on your machine: there is no queue of
  `factory`-labeled issues yet. `ork watch` with a trigger can add one.
