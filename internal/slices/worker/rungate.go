package worker

import (
	"context"
	"errors"
	"fmt"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/gatelock"
	"github.com/pablogore/software-factory/internal/platform/jobtoken"
)

// errNoGate means the Submitter was built without a Gate, and errNoLocks
// without Locks.
var (
	errNoGate  = errors.New("worker: run gate: no gate configured")
	errNoLocks = errors.New("worker: no gate locks configured")
)

// RunGate runs job's deterministic checks for token's role and returns the
// run, deciding and saving nothing: the builder learns whether its work
// would pass the gate before it hands it in. The call takes nothing but the
// token and the job, so what runs is only what the Gate is configured with.
// A token not for job, of a role that may not call run_gate, or from a
// round or stage entry the job has left is a jobtoken error, and then no
// check runs. The checks run under the job's lock in Locks: while another
// run_gate or the gate holds it, the call is refused with domain.ErrBusy,
// neither waiting nor queued, and runs none. A result submitted meanwhile
// cancels the run, whose answer is then an error wrapping
// gatelock.ErrPreempted, never a failed run. The checks run outside the
// job's lock Submit and Progress take, so a long run never holds up a
// progress message.
func (s *Submitter) RunGate(ctx context.Context, token string, job domain.JobID) (domain.ChecksRun, error) {
	run, release, err := s.startGate(ctx, token, job)
	if err != nil {
		return domain.ChecksRun{}, err
	}
	defer release()
	if s.Gate == nil {
		return domain.ChecksRun{}, errNoGate
	}
	checks, err := s.Gate.Check(run, job)
	if cause := context.Cause(run); errors.Is(cause, gatelock.ErrPreempted) {
		return domain.ChecksRun{}, fmt.Errorf("worker: run gate: %w", cause)
	}
	if err != nil {
		return domain.ChecksRun{}, fmt.Errorf("worker: run gate: %w", err)
	}
	return checks, nil
}

// MayRunGate tells whether token's role may run job's checks, as RunGate
// authorizes it, so a role that may not, a reviewer's, is not shown
// run_gate. It runs nothing and leaves RunGate to refuse a call anyway.
func (s *Submitter) MayRunGate(token string, job domain.JobID) bool {
	_, err := s.Tokens.Authorize(token, jobtoken.OpRunGate, job)
	return err == nil
}

// startGate checks token may run job's checks now and takes the job's lock
// in Locks, under the job's lock Submit revokes tokens under, so a token
// whose result was saved runs none, and no run starts while a submission
// waits for the one it cancelled.
func (s *Submitter) startGate(ctx context.Context, token string, job domain.JobID) (context.Context, func(), error) {
	defer s.byJob.lock(job)()
	g, err := s.Tokens.Authorize(token, jobtoken.OpRunGate, job)
	if err != nil {
		return nil, nil, fmt.Errorf("worker: run gate: %w", err)
	}
	j, _, err := s.Jobs.Load(ctx, job)
	if err != nil {
		return nil, nil, fmt.Errorf("worker: run gate: %w", err)
	}
	hist, err := s.Jobs.History(ctx, job)
	if err != nil {
		return nil, nil, fmt.Errorf("worker: run gate: %w", err)
	}
	if err := g.Current(j, hist); err != nil {
		return nil, nil, fmt.Errorf("worker: run gate: %w", err)
	}
	if s.Locks == nil {
		return nil, nil, errNoLocks
	}
	run, release, err := s.Locks.Try(ctx, job)
	if err != nil {
		return nil, nil, fmt.Errorf("worker: run gate: %w", err)
	}
	return run, release, nil
}
