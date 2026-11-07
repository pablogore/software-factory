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
	// Bases has the commit each job's worktree was made from: the diff
	// rules judge what the job changed since it, never since a ref the
	// builder could move.
	Bases bases
	// Command is the checks command, run without a shell in the worktree.
	Command []string
}

// Run runs the checks in the worktree of job id, which must be at the gate,
// and the diff rules on what the job changed since the base commit recorded
// for it, and decides GatePassed or GateFailed with the run on it: a broken
// rule, such as a protected path modified or a file outside the build role's
// allowed paths, fails the gate even when the checks pass, its reason kept
// in the run. It
// returns the events the job saved: the ChecksRan and the move to the next
// stage, or back to build in a new round, or to triage in the last one. When
// the checks cannot be run, or the job changed while they ran, nothing is
// saved. A job with no base recorded fails the gate with that reason: its
// diff can't be judged, and no ref stands in for the commit.
func (g *Gate) Run(ctx context.Context, id domain.JobID) ([]domain.Event, error) {
	if len(g.Command) == 0 {
		return nil, fmt.Errorf("gate: %w: checks command", domain.ErrMissingField)
	}
	if g.Bases == nil {
		return nil, fmt.Errorf("gate: %w: bases", domain.ErrMissingField)
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
	violations, err := g.diffRules(ctx, id, wt.Path, p)
	if err != nil {
		return nil, err
	}
	run, err := g.Checks.Run(ctx, wt.Path, g.Command)
	if err != nil {
		return nil, fmt.Errorf("gate: checks of %q: %w", id, err)
	}
	run.Violations = append(run.Violations, violations...)
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

// NoBase is the reason a job with no base commit recorded fails the gate.
const NoBase = "no base commit was recorded when the job's worktree was made, so its diff can't be judged"

// diffRules is a reason for each diff rule the worktree at dir breaks
// against job id's recorded base, or NoBase when it has none.
func (g *Gate) diffRules(ctx context.Context, id domain.JobID, dir string, p domain.Pipeline) ([]string, error) {
	base, err := g.Bases.Base(ctx, id)
	if errors.Is(err, ports.ErrNoBase) {
		return []string{NoBase}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("gate: base of %q: %w", id, err)
	}
	changes, err := g.Diffs.Changes(ctx, dir, base)
	if err != nil {
		return nil, fmt.Errorf("gate: diff of %q: %w", id, err)
	}
	return append(ProtectedTouched(changes), OutsideAllowed(changes, Roles(p))...), nil
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
