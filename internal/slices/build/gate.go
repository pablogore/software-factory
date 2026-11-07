package build

import (
	"context"
	"errors"
	"fmt"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/ports"
)

// DefaultCommand is the checks command of a repo that configures none: the
// one AGENTS.md says the factory's gate runs.
var DefaultCommand = []string{"make", "check"}

// ErrNotAtGate means the job is not waiting at the gate, so its checks are
// not run.
var ErrNotAtGate = errors.New("job not at the gate")

// Gate runs a job's checks and decides the verdict on it.
type Gate struct {
	Jobs      jobs
	Pipelines pipelines
	Worktrees worktrees
	Checks    checks
	Diffs     diffs
	Clock     ports.Clock
	// Command is the checks command, run without a shell in the worktree.
	Command []string
	// Base is the branch job branches start from; the diff rules judge
	// what a job changed since it left it.
	Base string
}

// Run runs the checks in the worktree of job id, which must be at the gate,
// and the diff rules on what the job changed since Base, and decides
// GatePassed or GateFailed with the run on it: a broken rule fails the gate
// even when the checks pass, its reason kept in the run. It returns the
// events the job saved: the ChecksRan and the move to the next stage, or back
// to build in a new round, or to triage in the last one. When the checks
// cannot be run, or the job changed while they ran, nothing is saved.
func (g *Gate) Run(ctx context.Context, id domain.JobID) ([]domain.Event, error) {
	if len(g.Command) == 0 {
		return nil, fmt.Errorf("gate: %w: checks command", domain.ErrMissingField)
	}
	if g.Base == "" {
		return nil, fmt.Errorf("gate: %w: base", domain.ErrMissingField)
	}
	job, v, err := g.Jobs.Load(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("gate: %w", err)
	}
	if job.Stage != domain.StageGate {
		return nil, fmt.Errorf("gate: %w: job %q is in %q", ErrNotAtGate, id, job.Stage)
	}
	p, err := g.Pipelines.Pipeline(ctx, job.Pipeline)
	if err != nil {
		return nil, fmt.Errorf("gate: %w", err)
	}
	wt, err := g.Worktrees.Prepare(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("gate: worktree of %q: %w", id, err)
	}
	changes, err := g.Diffs.Changes(ctx, wt.Path, g.Base)
	if err != nil {
		return nil, fmt.Errorf("gate: diff of %q: %w", id, err)
	}
	run, err := g.Checks.Run(ctx, wt.Path, g.Command)
	if err != nil {
		return nil, fmt.Errorf("gate: checks of %q: %w", id, err)
	}
	run.Violations = append(run.Violations, ProtectedTouched(changes)...)
	var ev domain.Event = domain.GatePassed{Job: id, Run: run, At: g.Clock.Now()}
	if !run.Passed() {
		ev = domain.GateFailed{Job: id, Run: run, At: g.Clock.Now()}
	}
	evs, _, err := g.Jobs.Apply(ctx, id, v, p, ev)
	if err != nil {
		return nil, fmt.Errorf("gate: %w", err)
	}
	return evs, nil
}

// LastChecks is the latest checks run kept in a job's history: after a
// failing gate, the log the builder of the new round gets.
func LastChecks(hist []ports.Record) (domain.ChecksRan, bool) {
	for i := len(hist) - 1; i >= 0; i-- {
		if e, ok := hist[i].Event.(domain.ChecksRan); ok {
			return e, true
		}
	}
	return domain.ChecksRan{}, false
}
