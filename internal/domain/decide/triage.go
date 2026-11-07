package decide

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/pablogore/software-factory/internal/domain"
)

// wall decides a job that hit a wall the system detects: a builder BLOCKED,
// a gate failed or CHANGES in the last round, a failed merge. While it has
// triage retries left (triage_retries, a person's override for this job,
// else the pipeline's), triage looks at it first; after that, or with 0, it
// waits for a person with a closed question and the wall's cause, so OK and
// feedback act on it as without triage.
func wall(job domain.Job, p domain.Pipeline, cause domain.HumanCause, reason string, at time.Time) []domain.Event {
	if limit := triageRetries(job, p); job.TriageRetries >= limit {
		return []domain.Event{domain.NeedsHuman{Job: job.ID, Reason: reason, Question: spent(job.TriageRetries, limit), Cause: cause, At: at}}
	}
	return []domain.Event{domain.TriageRequested{Job: job.ID, Reason: reason, Cause: cause, At: at}}
}

// spent is the closed question for a wall triage no longer looks at.
func spent(used, limit int) string {
	if limit == 0 {
		return "Triage is off for this job (triage_retries 0). Send it back to build with your feedback, or stop it?"
	}
	return fmt.Sprintf("Triage already retried this job %d of %d times. Send it back to build with your feedback, or stop it?", used, limit)
}

// triaged decides the triage role's result for a job in triage. RETRY sends
// the job back to build for one more round with the brief as input,
// running the same reviewers again; ESCALATE puts it in needs-human with
// the question and the wall's cause. Only the default triage role may
// answer, and a job that records no valid wall fails closed.
func triaged(job domain.Job, e domain.ResultSubmitted, p domain.Pipeline) ([]domain.Event, error) {
	role, _ := domain.DefaultRole(domain.KindTriage)
	r, ok := e.Result.(domain.TriageResult)
	if !ok || e.Role != role {
		return nil, illegal(job, e)
	}
	if err := r.Validate(); err != nil {
		return nil, fmt.Errorf("decide: %s result from %q: %w", domain.KindTriage, e.Role, err)
	}
	if err := job.Wall.Validate(); err != nil {
		return nil, fmt.Errorf("decide: triage: wall: %w", err)
	}
	if r.Decision == domain.TriageEscalate {
		return []domain.Event{domain.NeedsHuman{Job: job.ID, Reason: job.Reason, Question: strings.TrimSpace(r.Question), Cause: job.Wall, At: e.At}}, nil
	}
	b := slices.IndexFunc(p.Stages, func(s domain.Stage) bool { return s.Kind == domain.KindBuild })
	if b < 0 {
		return nil, fmt.Errorf("decide: pipeline %q has no build stage", p.Name)
	}
	return []domain.Event{
		domain.TriageRetried{Job: job.ID, Role: e.Role, Brief: r.Brief, At: e.At},
		domain.RoundStarted{Job: job.ID, Round: job.Round + 1, Reviewers: slices.Clone(job.Reviewers), At: e.At},
		domain.StageEntered{Job: job.ID, Stage: p.Stages[b].Name, At: e.At},
	}, nil
}

// mergeFailed is a wall for a job whose merge did not go through.
func mergeFailed(job domain.Job, e domain.MergeFailed, p domain.Pipeline) ([]domain.Event, error) {
	if job.Stage != domain.StageMerge {
		return nil, illegal(job, e)
	}
	reason := "merge failed"
	if r := strings.TrimSpace(e.Reason); r != "" {
		reason += ": " + r
	}
	return wall(job, p, domain.CauseMergeFailed, reason, e.At), nil
}
