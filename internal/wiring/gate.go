package wiring

import (
	"context"
	"fmt"

	"github.com/pablogore/software-factory/internal/adapters/out/checkrun"
	"github.com/pablogore/software-factory/internal/adapters/out/configyaml"
	"github.com/pablogore/software-factory/internal/adapters/out/gitdiff"
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
// base commit to count their tests.
func Gate(jobs ports.JobStore, p Pipelines, worktrees ports.Workspace, bases ports.BaseLog, clock ports.Clock, command, countCommand []string) *build.Gate {
	return &build.Gate{Jobs: jobs, Pipelines: p, Worktrees: worktrees, Checks: checkrun.Runner{}, Bases: bases, Diffs: gitdiff.Differ{}, Clock: clock, Command: command, CountCommand: countCommand, RepoConfig: baseGates{}, Snapshots: gitdiff.Differ{}}
}

// baseGates reads GatesFile as a commit has it, so what a job's branch does
// to it never reaches the job's own gate.
type baseGates struct{}

func (baseGates) CountCommand(ctx context.Context, dir, base string) ([]string, error) {
	data, ok, err := gitdiff.Differ{}.AtBaseIfAny(ctx, dir, base, GatesFile)
	if err != nil || !ok {
		return nil, err
	}
	g, err := configyaml.LoadGates([]byte(data))
	if err != nil {
		return nil, fmt.Errorf("%s at %s: %w", GatesFile, base, err)
	}
	return g.CountCommand, nil
}
