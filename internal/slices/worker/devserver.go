package worker

import (
	"context"
	"errors"
	"fmt"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/jobtoken"
	"github.com/pablogore/software-factory/internal/ports"
)

// errNoDevServers means the Submitter was built without what runs dev
// servers.
var errNoDevServers = errors.New("worker: dev server: no dev servers configured")

// devKey is a role's session on a job: each runs at most one dev server, so
// one role handing in its result never stops another's.
type devKey struct {
	job  domain.JobID
	role string
}

// devServer is the dev server a session started, in the round and stage
// entry its token was issued in.
type devServer struct {
	stage string
	port  int
	round int
	entry int
}

// DevServer starts the dev server of token's role on job, in its worktree
// with the command the repo configures, and returns its port, a free one of
// the job's own range. A token not for job, of a role that may not call
// dev_server, or from a round or stage entry the job has left is a jobtoken
// error, and then nothing starts. A repo that configures no command is
// domain.ErrMissingField, and nothing starts. A second call while the
// session's server runs starts no other and returns the same port; a server
// left from an earlier round or stage entry is stopped first. The server
// runs until the role hands in its result, StopDevServer is called or
// StopLeft sees the session end.
func (s *Submitter) DevServer(ctx context.Context, token string, job domain.JobID) (int, error) {
	defer s.byJob.lock(job)()
	g, j, err := s.authorizeDev(ctx, token, job)
	if err != nil {
		return 0, err
	}
	k := devKey{job: job, role: g.Role}
	if d, ok := s.devServer(k); ok {
		if d.round == g.Round && d.entry == g.Entry {
			return d.port, nil
		}
		if err := s.stopDev(ctx, k); err != nil {
			return 0, err
		}
	}
	wt, err := s.Worktrees.Prepare(ctx, job)
	if err != nil {
		return 0, fmt.Errorf("worker: dev server: %w", err)
	}
	command, err := s.DevCommands.DevCommand(ctx, job, wt)
	if err != nil {
		return 0, fmt.Errorf("worker: dev server: %w", err)
	}
	if len(command.Args) == 0 {
		return 0, fmt.Errorf("worker: dev server: %w: the repo configures no dev server command", domain.ErrMissingField)
	}
	port, err := s.Ports.Free(job, s.devPortsOf(job))
	if err != nil {
		return 0, fmt.Errorf("worker: dev server: %w", err)
	}
	stage := j.Stage + "/" + g.Role
	if err := s.Servers.Start(ctx, wt, stage, port, command); err != nil {
		return 0, fmt.Errorf("worker: dev server: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.devs == nil {
		s.devs = map[devKey]devServer{}
	}
	s.devs[k] = devServer{stage: stage, port: port, round: g.Round, entry: g.Entry}
	return port, nil
}

// StopDevServer stops the dev server token's role started on job, and every
// process it started. A session running none is not an error. The token is
// checked as DevServer checks it.
func (s *Submitter) StopDevServer(ctx context.Context, token string, job domain.JobID) error {
	defer s.byJob.lock(job)()
	g, _, err := s.authorizeDev(ctx, token, job)
	if err != nil {
		return err
	}
	return s.stopDev(ctx, devKey{job: job, role: g.Role})
}

// DevServerEvents are the events StopLeft is handed: every event of a job,
// since any may move it out of a stage, and AttemptFailed.
var DevServerEvents = []string{
	"ResultSubmitted", "HumanApproved", "SpecgatePassed", "SpecgateFailed", "SpecRejected", "GatePassed", "GateFailed",
	"StageEntered", "ReviewPassed", "ChangesRequested", "RoundStarted", "NeedsHuman", "TriageRequested", "TriageRetried",
	"MergeFailed", "BudgetExhausted", "AttemptFailed", "NeedsInfo", "JobRejected", "MergeRequested", "MergeCompleted",
	"ReworkRequested", "InfoAnswered", "MergeOverridden", "LimitOverridden", "CapRaised", "JobStopped", "SliceDropped",
}

// StopLeft is the bus handler that stops the dev servers whose session is
// over: the role's after its AttemptFailed, and every server of the job
// started in a round or stage entry the job has left, as when it moves on,
// is closed, stopped or dropped. A job no longer found has all its servers
// stopped. Handing it the same event again stops nothing more.
func (s *Submitter) StopLeft(ctx context.Context, ev domain.Event) error {
	e, ok := ev.(interface{ JobID() domain.JobID })
	if !ok {
		return nil
	}
	job := e.JobID()
	defer s.byJob.lock(job)()
	var errs []error
	if f, ok := ev.(domain.AttemptFailed); ok {
		errs = append(errs, s.stopDev(ctx, devKey{job: job, role: f.Role}))
	}
	j, _, err := s.Jobs.Load(ctx, job)
	if errors.Is(err, ports.ErrJobNotFound) {
		j = domain.Job{ID: job}
	} else if err != nil {
		return fmt.Errorf("worker: dev server: %w", err)
	}
	var hist []ports.Record
	if err == nil {
		if hist, err = s.Jobs.History(ctx, job); err != nil {
			return fmt.Errorf("worker: dev server: %w", err)
		}
	}
	entry := jobtoken.Entry(hist)
	for _, k := range s.devKeysOf(job) {
		if d, ok := s.devServer(k); ok && (d.round != j.Round || d.entry != entry) {
			errs = append(errs, s.stopDev(ctx, k))
		}
	}
	return errors.Join(errs...)
}

// authorizeDev checks token may run job's dev server now and returns its
// grant and the job.
func (s *Submitter) authorizeDev(ctx context.Context, token string, job domain.JobID) (jobtoken.Grant, domain.Job, error) {
	g, err := s.Tokens.Authorize(token, jobtoken.OpDevServer, job)
	if err != nil {
		return jobtoken.Grant{}, domain.Job{}, fmt.Errorf("worker: dev server: %w", err)
	}
	j, _, err := s.Jobs.Load(ctx, job)
	if err != nil {
		return jobtoken.Grant{}, domain.Job{}, fmt.Errorf("worker: dev server: %w", err)
	}
	hist, err := s.Jobs.History(ctx, job)
	if err != nil {
		return jobtoken.Grant{}, domain.Job{}, fmt.Errorf("worker: dev server: %w", err)
	}
	if err := g.Current(j, hist); err != nil {
		return jobtoken.Grant{}, domain.Job{}, fmt.Errorf("worker: dev server: %w", err)
	}
	if s.Servers == nil || s.Worktrees == nil || s.Ports == nil || s.DevCommands == nil {
		return jobtoken.Grant{}, domain.Job{}, errNoDevServers
	}
	return g, j, nil
}

// stopDev stops k's dev server, if it runs one. Callers hold k's job lock.
func (s *Submitter) stopDev(ctx context.Context, k devKey) error {
	d, ok := s.devServer(k)
	if !ok {
		return nil
	}
	if err := s.Servers.StopStage(ctx, k.job, d.stage); err != nil {
		return fmt.Errorf("worker: dev server: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.devs, k)
	return nil
}

func (s *Submitter) devServer(k devKey) (devServer, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.devs[k]
	return d, ok
}

// devKeysOf is the sessions of job running a dev server.
func (s *Submitter) devKeysOf(job domain.JobID) []devKey {
	s.mu.Lock()
	defer s.mu.Unlock()
	var keys []devKey
	for k := range s.devs {
		if k.job == job {
			keys = append(keys, k)
		}
	}
	return keys
}

// devPortsOf is the ports job's sessions' servers hold.
func (s *Submitter) devPortsOf(job domain.JobID) []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	var taken []int
	for k, d := range s.devs {
		if k.job == job {
			taken = append(taken, d.port)
		}
	}
	return taken
}
