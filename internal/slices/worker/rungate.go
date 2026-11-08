package worker

import (
	"context"
	"errors"
	"fmt"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/jobtoken"
)

// errNoGate means the Submitter was built without a Gate.
var errNoGate = errors.New("worker: run gate: no gate configured")

// RunGate runs job's deterministic checks for token's role and returns the
// run, deciding and saving nothing: the builder learns whether its work
// would pass the gate before it hands it in. The call takes nothing but the
// token and the job, so what runs is only what the Gate is configured with.
// A token not for job, of a role that may not call run_gate, or from a
// round or stage entry the job has left is a jobtoken error, and then no
// check runs. The checks run outside the lock Submit and Progress take, so
// a long run never holds up a result or a progress message.
func (s *Submitter) RunGate(ctx context.Context, token string, job domain.JobID) (domain.ChecksRun, error) {
	if err := s.authorizeGate(ctx, token, job); err != nil {
		return domain.ChecksRun{}, err
	}
	if s.Gate == nil {
		return domain.ChecksRun{}, errNoGate
	}
	run, err := s.Gate.Check(ctx, job)
	if err != nil {
		return domain.ChecksRun{}, fmt.Errorf("worker: run gate: %w", err)
	}
	return run, nil
}

// authorizeGate checks token may run job's checks now, under the lock
// Submit revokes tokens under, so a token whose result was saved runs none.
func (s *Submitter) authorizeGate(ctx context.Context, token string, job domain.JobID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, err := s.Tokens.Authorize(token, jobtoken.OpRunGate, job)
	if err != nil {
		return fmt.Errorf("worker: run gate: %w", err)
	}
	j, _, err := s.Jobs.Load(ctx, job)
	if err != nil {
		return fmt.Errorf("worker: run gate: %w", err)
	}
	hist, err := s.Jobs.History(ctx, job)
	if err != nil {
		return fmt.Errorf("worker: run gate: %w", err)
	}
	if err := g.Current(j, hist); err != nil {
		return fmt.Errorf("worker: run gate: %w", err)
	}
	return nil
}
