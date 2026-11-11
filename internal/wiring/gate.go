package wiring

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/pablogore/software-factory/internal/adapters/out/checkrun"
	"github.com/pablogore/software-factory/internal/adapters/out/configyaml"
	"github.com/pablogore/software-factory/internal/adapters/out/gitdiff"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/childenv"
	"github.com/pablogore/software-factory/internal/platform/gatelock"
	"github.com/pablogore/software-factory/internal/ports"
	"github.com/pablogore/software-factory/internal/slices/build"
)

// GatesFile is where a repo configures its gate, from the repo root.
const GatesFile = ".factory/gates.yaml"

// Gate is the deterministic gate over jobs, running command as a local
// process in each job's worktree from worktrees and judging with git what
// each job changed since the base commit bases recorded for it. The test
// count command, countCommand or else the one GatesFile sets at that base
// commit, is run the same way in the worktree and in a git checkout of that
// base commit to count their tests. It runs each job's checks under the
// job's lock in locks, which the worker API's run_gate must share, with the
// job's own HOME in homes, which its git runs with too.
func Gate(jobs ports.JobStore, p Pipelines, worktrees ports.Workspace, bases ports.BaseLog, clock ports.Clock, locks *gatelock.Locks, homes childenv.Homes, command, countCommand []string) *build.Gate {
	git := gitdiff.Differ{Homes: homes}
	return &build.Gate{Jobs: jobs, Pipelines: p, Worktrees: worktrees, Checks: checkrun.Runner{Homes: homes}, Bases: bases, Diffs: git, Clock: clock, Command: command, CountCommand: countCommand, RepoConfig: baseGates{git}, Snapshots: git, Locks: locks}
}

// baseGates reads GatesFile as a commit has it, so what a job's branch does
// to it never reaches the job's own gate.
type baseGates struct{ git gitdiff.Differ }

func (b baseGates) CountCommand(ctx context.Context, job domain.JobID, dir, base string) ([]string, error) {
	data, ok, err := b.git.AtBaseIfAny(ctx, job, dir, base, GatesFile)
	if err != nil || !ok {
		return nil, err
	}
	g, err := configyaml.LoadGates([]byte(data))
	if err != nil {
		return nil, fmt.Errorf("%s at %s: %w", GatesFile, base, err)
	}
	return g.CountCommand, nil
}

// The limits of a run a builder asks for with run_gate, when none are
// configured.
const (
	DefaultRunGateTimeout   = 15 * time.Minute
	DefaultRunGateMaxOutput = 1 << 20
)

// RunGateChecks is Gate as a builder's run_gate runs it: the whole call,
// its checks, its test counts and its git steps alike, ends once Timeout
// passes, its commands killed with their process group, and it answers with
// an error wrapping domain.ErrTimedOut, never a failed run. It returns only
// once what it ran has stopped, so the job's lock is free for the next call.
// A Timeout not set is DefaultRunGateTimeout.
type RunGateChecks struct {
	*build.Gate
	Timeout time.Duration
}

// Check implements GateChecks.
func (c *RunGateChecks) Check(ctx context.Context, id domain.JobID) (domain.ChecksRun, error) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultRunGateTimeout
	}
	ctx, cancel := context.WithTimeoutCause(ctx, timeout, domain.ErrTimedOut)
	defer cancel()
	run, err := c.Gate.Check(ctx, id)
	if errors.Is(context.Cause(ctx), domain.ErrTimedOut) {
		return domain.ChecksRun{}, fmt.Errorf("%w: the call ran over %s and was stopped, checks, test counts and git steps included; it has no result, so call again", domain.ErrTimedOut, timeout)
	}
	return run, err
}

// RunGate is Gate as a builder's run_gate runs it, for the worker API: a
// call past timeout is stopped, as RunGateChecks says, and at most
// maxOutput bytes of each command's output are kept, the run noting it.
func RunGate(jobs ports.JobStore, p Pipelines, worktrees ports.Workspace, bases ports.BaseLog, clock ports.Clock, locks *gatelock.Locks, homes childenv.Homes, command, countCommand []string, timeout time.Duration, maxOutput int) *RunGateChecks {
	g := Gate(jobs, p, worktrees, bases, clock, locks, homes, command, countCommand)
	g.Checks = checkrun.Runner{Homes: homes, MaxOutput: maxOutput}
	return &RunGateChecks{Gate: g, Timeout: timeout}
}
