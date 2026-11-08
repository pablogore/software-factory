# new-factory

TODO: what this project is, in two or three sentences.

## Setup

TODO: how to set up a fresh checkout (install dependencies, env files to copy).

## Dev server

TODO: how to start the dev server on a given port, e.g. `npm run dev -- --port <port>`.

## Validation

TODO: the test, lint, typecheck and build commands. Keep them in sync with `factory/checks.sh`.

## Software factory

`/factory run` builds every open GitHub issue labeled `factory`: intake → spec →
build → gate → review → approve, one PR per issue; `/factory <idea>` has the
product owner write an epic and user stories first. State is in `factory/jobs/`
(gitignored); `factory/bin/job` is its only writer. Checks: `factory/checks.sh`.
Dashboard: `cd factory && python3 -m http.server`, then open
http://localhost:8000/dashboard.html.

Each job works in its own git worktree, a full checkout of the repo at
`.worktrees/<job-id>` (gitignored). A tool run from the repo root that doesn't
read `.gitignore` sees those copies too; exclude `.worktrees/` in its config.
