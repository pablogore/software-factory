package worker

import (
	"context"
	"fmt"
	"slices"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/jobtoken"
)

// Resume picks up, as a server starts, the pauses on agents' limits that
// jobs ids recorded before it stopped: a pause and the attempts waiting it
// out lived only in that server's memory. It holds each agent a job's
// Limited names until its Until, and hands Scheduler again the attempt of
// every role of ids that waits in its current stage for one of those
// agents, the role that hit the limit among them. An attempt whose agent
// may go on already runs at once; the others wait for the pause, as
// Launch's do. ctx is the attempts' context too.
func (l *Launcher) Resume(ctx context.Context, ids []domain.JobID) error {
	var jobs []domain.Job
	agents := map[string]bool{}
	for _, id := range ids {
		job, _, err := l.Jobs.Load(ctx, id)
		if err != nil {
			return fmt.Errorf("worker: resume: %w", err)
		}
		if job.Limited != nil {
			l.Attempts.paused.pause(job.Limited.Agent, job.Limited.Until)
			agents[job.Limited.Agent] = true
		}
		jobs = append(jobs, job)
	}
	if len(agents) == 0 {
		return nil
	}
	for _, job := range jobs {
		if err := l.resume(ctx, job, agents); err != nil {
			return fmt.Errorf("worker: resume: %w", err)
		}
	}
	return nil
}

// resume hands Scheduler the attempt of each role of job's current stage
// that has no result yet and runs on one of agents, or hit the limit job is
// paused on. A role that cannot be read is left to its own attempt to fail,
// unless it is the one that hit the limit.
func (l *Launcher) resume(ctx context.Context, job domain.Job, agents map[string]bool) error {
	p, err := l.Pipelines.Pipeline(ctx, job.Pipeline)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(p.Stages, func(s domain.Stage) bool { return s.Name == job.Stage })
	if i < 0 {
		return nil
	}
	hist, err := l.Jobs.History(ctx, job.ID)
	if err != nil {
		return err
	}
	entry := jobtoken.Entry(hist)
	for _, name := range runningRoles(job, p.Stages[i]) {
		if slices.Contains(job.Passed, name) || slices.Contains(job.Changes, name) {
			continue
		}
		hit := job.Limited != nil && job.Limited.Role == name
		if !hit {
			role, _, err := l.Roles.Role(ctx, name)
			if err != nil || !agents[role.Agent] {
				continue
			}
		}
		// The job's failed attempts in the stage are the retry its last
		// attempt was, so an AttemptRetried still pending launches it once.
		l.submit(ctx, launchKey{job: job.ID, role: name, round: job.Round, entry: entry, retry: job.AttemptsFailed})
	}
	return nil
}
