package worker

import (
	"context"
	"fmt"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/jobtoken"
)

// Progress records text on job as a progress message from token's role, an
// AgentProgressed, and returns the events the job saved. It runs under the
// job's lock Submit takes, so a message never makes a result's save conflict,
// and it keeps the token: an agent reports progress as often as it needs
// before its result. A token not for job, or from a round or stage entry the
// job has left, is a jobtoken error; an empty or blank text wraps
// domain.ErrMissingField and one over domain.MaxProgressText characters
// domain.ErrInvalidValue. In every case nothing is saved.
func (s *Submitter) Progress(ctx context.Context, token string, job domain.JobID, text string) ([]domain.Event, error) {
	defer s.byJob.lock(job)()
	g, err := s.Tokens.Authorize(token, jobtoken.OpProgress, job)
	if err != nil {
		return nil, fmt.Errorf("worker: progress: %w", err)
	}
	j, v, err := s.Jobs.Load(ctx, job)
	if err != nil {
		return nil, fmt.Errorf("worker: progress: %w", err)
	}
	hist, err := s.Jobs.History(ctx, job)
	if err != nil {
		return nil, fmt.Errorf("worker: progress: %w", err)
	}
	if err := g.Current(j, hist); err != nil {
		return nil, fmt.Errorf("worker: progress: %w", err)
	}
	p, err := s.Pipelines.Pipeline(ctx, j.Pipeline)
	if err != nil {
		return nil, fmt.Errorf("worker: progress: %w", err)
	}
	ev := domain.AgentProgressed{Job: job, Role: g.Role, Kind: domain.ProgressMessage, Text: text, At: s.Clock.Now()}
	evs, _, err := s.Jobs.Apply(ctx, job, v, p, ev)
	if err != nil {
		return nil, fmt.Errorf("worker: progress: %w", err)
	}
	return evs, nil
}
