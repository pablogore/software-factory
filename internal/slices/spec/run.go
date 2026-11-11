package spec

import (
	"context"
	"errors"
	"fmt"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/ports"
)

// ErrNotAtSpecgate means the job is not waiting at specgate, so its spec is
// not checked.
var ErrNotAtSpecgate = errors.New("job not at specgate")

// Specgate checks the spec a job's spec role handed in last and decides the
// verdict on the job, so a rejected spec goes back to its writer, or to a
// person once the pipeline's rejections run out, and a passing one moves on.
type Specgate struct {
	Jobs      jobs
	History   history
	Pipelines pipelines
	Clock     ports.Clock
}

// Run checks the last spec of job id, which must be at specgate, and returns
// the events the job saved. A person's OK of a length exception moves the
// job on without coming back here, so the spec is checked without one.
func (g *Specgate) Run(ctx context.Context, id domain.JobID) ([]domain.Event, error) {
	job, v, err := g.Jobs.Load(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("specgate: %w", err)
	}
	if job.Stage != domain.StageSpecgate {
		return nil, fmt.Errorf("specgate: %w: job %q is in %q", ErrNotAtSpecgate, id, job.Stage)
	}
	p, err := g.Pipelines.Pipeline(ctx, job.Pipeline)
	if err != nil {
		return nil, fmt.Errorf("specgate: %w", err)
	}
	hist, err := g.History.History(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("specgate: %w", err)
	}
	text, ok := lastSpec(hist)
	if !ok {
		return nil, fmt.Errorf("specgate: job %q: %w: no spec handed in", id, domain.ErrMissingField)
	}
	evs, _, err := g.Jobs.Apply(ctx, id, v, p, Check(text, false).Event(id, g.Clock.Now()))
	if err != nil {
		return nil, fmt.Errorf("specgate: %w", err)
	}
	return evs, nil
}

// Handle runs specgate for a job that entered it, as a bus subscriber. Any
// other event, or one the job has since left specgate after, is done with:
// the bus may hand an event over again.
func (g *Specgate) Handle(ctx context.Context, ev domain.Event) error {
	e, ok := ev.(domain.StageEntered)
	if !ok || e.Stage != domain.StageSpecgate {
		return nil
	}
	if _, err := g.Run(ctx, e.Job); err != nil && !errors.Is(err, ErrNotAtSpecgate) {
		return err
	}
	return nil
}

func lastSpec(hist []ports.Record) (string, bool) {
	text, ok := "", false
	for _, r := range hist {
		if e, isSpec := r.Event.(domain.SpecWritten); isSpec {
			text, ok = e.Spec, true
		}
	}
	return text, ok
}
