package wiring

import (
	"time"

	"github.com/pablogore/software-factory/internal/adapters/out/checkrun"
	"github.com/pablogore/software-factory/internal/adapters/out/gitdiff"
	"github.com/pablogore/software-factory/internal/platform/gatelock"
	"github.com/pablogore/software-factory/internal/ports"
	"github.com/pablogore/software-factory/internal/slices/build"
)

// Gate is the deterministic gate over jobs, running command as a local
// process in each job's worktree from worktrees and judging with git what
// each job changed since the base commit bases recorded for it. countCommand,
// when given, is run the same way in the worktree and in a git checkout of
// that base commit to count their tests. It runs each job's checks under
// the job's lock in locks, which the worker API's run_gate must share.
func Gate(jobs ports.JobStore, p Pipelines, worktrees ports.Workspace, bases ports.BaseLog, clock ports.Clock, locks *gatelock.Locks, command, countCommand []string) *build.Gate {
	return &build.Gate{Jobs: jobs, Pipelines: p, Worktrees: worktrees, Checks: checkrun.Runner{}, Bases: bases, Diffs: gitdiff.Differ{}, Clock: clock, Command: command, CountCommand: countCommand, Snapshots: gitdiff.Differ{}, Locks: locks}
}

// The limits of a run a builder asks for with run_gate, when none are
// configured.
const (
	DefaultRunGateTimeout   = 15 * time.Minute
	DefaultRunGateMaxOutput = 1 << 20
)

// RunGate is Gate as a builder's run_gate runs it, for the worker API: a
// run past timeout is killed with its process group, and at most maxOutput
// bytes of each command's output are kept, the run noting either.
func RunGate(jobs ports.JobStore, p Pipelines, worktrees ports.Workspace, bases ports.BaseLog, clock ports.Clock, locks *gatelock.Locks, command, countCommand []string, timeout time.Duration, maxOutput int) *build.Gate {
	g := Gate(jobs, p, worktrees, bases, clock, locks, command, countCommand)
	g.Checks = checkrun.Runner{Timeout: timeout, MaxOutput: maxOutput}
	return g
}
