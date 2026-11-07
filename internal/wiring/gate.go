package wiring

import (
	"github.com/pablogore/software-factory/internal/adapters/out/checkrun"
	"github.com/pablogore/software-factory/internal/ports"
	"github.com/pablogore/software-factory/internal/slices/build"
)

// Gate is the deterministic gate over jobs, running command as a local
// process in each job's worktree from worktrees.
func Gate(jobs ports.JobStore, p Pipelines, worktrees ports.Workspace, clock ports.Clock, command []string) *build.Gate {
	return &build.Gate{Jobs: jobs, Pipelines: p, Worktrees: worktrees, Checks: checkrun.Runner{}, Clock: clock, Command: command}
}
