package wiring

import (
	"github.com/pablogore/software-factory/internal/adapters/out/checkrun"
	"github.com/pablogore/software-factory/internal/adapters/out/gitdiff"
	"github.com/pablogore/software-factory/internal/ports"
	"github.com/pablogore/software-factory/internal/slices/build"
)

// Gate is the deterministic gate over jobs, running command as a local
// process in each job's worktree from worktrees and judging with git what
// each job changed since the base commit bases recorded for it.
func Gate(jobs ports.JobStore, p Pipelines, worktrees ports.Workspace, bases ports.BaseLog, clock ports.Clock, command []string) *build.Gate {
	return &build.Gate{Jobs: jobs, Pipelines: p, Worktrees: worktrees, Checks: checkrun.Runner{}, Bases: bases, Diffs: gitdiff.Differ{}, Clock: clock, Command: command}
}
