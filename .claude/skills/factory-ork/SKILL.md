---
name: factory-ork
description: Run the software factory on GitHub issues with Ork as the engine. Use when the user types /factory-ork followed by run <n>, status, stop, or a question about a run. This session is the orchestrator; Ork runs the steps.
---

# Factory on Ork

You are the orchestrator. Ork runs each job's steps (intake, spec, builder,
reviewers, approver, PR) in the background; you start runs, follow them, bring
every open question to the user in this chat, answer Ork with the user's choice,
and report. The user watches progress in `ork ui` if they want; they act here.

Never answer a decision the user hasn't answered, except the ones this file
says you may. Never run `gh pr merge`, `git push` or edit the job's worktree
yourself: Ork does that in its own steps.

## Setup check (once per session)

Run `ork doctor`. If `claude (cli driver)` or `gh` isn't ✓, tell the user the
line and stop. Run `ork validate factory-issue`; if it fails, show the error
and stop.

## `/factory-ork run <n>` (one or more issue numbers)

For each issue:

1. Start it in the background:
   ```sh
   ork run factory-issue --detach --input issue=<n> [--input base=<branch>] [--input checks="<command>"]
   ```
   `base` defaults to `main`. `checks` defaults to `bash factory/checks.sh`;
   in the software-factory repo itself use
   `python3 -m unittest discover factory/tests`. Keep the run id it prints.
2. Tell the user in one line: issue, run id, and that `ork ui` shows it live.
3. Follow it with the Monitor tool on `ork logs <run> -f`, so you hear about
   each step without polling. Don't relay every line: tell the user only when
   a node finishes (with its cost), a check fails, or the run ends.
4. When the log shows a question (`? ` or `asks`), run `ork decide --json` and
   handle it as **Decisions** says.
5. When the run ends, run `factory/bin/ork-job <run>`: it writes the job
   folder `.ork/jobs/issue-<n>-<run>/` (job.md, intake.md, spec.md, build.md,
   review-<role>.md, approver.md, one file per round). Read job.md, and
   report: what happened (PR link, needs-info, rejected,
   escalated, failed), the cost, and what the user needs to do, if anything.

Several issues run at the same time; the workflow's `limits` cap each run's
spend.

## Decisions

`ork decide --json` lists open questions with their id, run, node, options and
recommended option. For each one:

- **A gate or a loop out of rounds** (merge, stop/continue): show the user the
  question, the options, the recommended one, and the document if there is one
  (`ork decide --id <id> --document`). Use AskUserQuestion. Then answer:
  `ork decide --id <id> --choose <option> [--say "<user's note>"]`.
- **A tool permission** for the builder or spec-writer inside its worktree
  (`Bash`, `Edit`, `Write` on a path under `*-worktrees/ork-*`, or reading the
  checkout): you may allow it yourself, and say so in one line. Anything
  outside the worktree, any `git push`, `gh`, `rm`, network or install command:
  ask the user.
- **A reviewer or approver asking to write**: deny it (`--choose deny`) and tell
  the user.

## `/factory-ork status`

`ork ps --json` for runs, `ork decide --list` for open questions. Report one
line per live run: issue, current node, spend so far, waiting on what.

## `/factory-ork stop [<run>]`

Confirm with the user, then `ork stop <run>`. To stop one step and let the run
go on: `ork stop <run> --node <node>`. Pause and go on: `ork pause <run>` /
`ork continue <run>`. A crashed or stopped run comes back with
`ork resume <run> --detach`.

## `/factory-ork job [<run>]`

Run `factory/bin/ork-job <run>` (it works while the run is still going) and
give the user the folder and its job.md table.

## Questions about a run

Read the job folder first (`factory/bin/ork-job <run>` refreshes it), then, if
you need more: `ork logs <run> [--node <n>]`, `ork tree <run>`,
`ork worktrees`. The spec is `.ork/specs/issue-<n>.md`; the builder's work is
in the worktree on branch `ork/<run>/builder`.
