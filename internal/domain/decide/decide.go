package decide

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/pablogore/software-factory/internal/domain"
)

// ErrIllegalTransition means the event does not fit the job's current stage.
// Decide returns no events with it, so nothing changes.
var ErrIllegalTransition = errors.New("illegal transition")

// ErrUnsupported means the result or message is legal in this stage but its
// outcome (NEEDS_INFO, REJECT, BLOCKED, task results, a person's
// message outside needs-human) is not decided yet; E01.4 and E01.5 add it.
var ErrUnsupported = errors.New("unsupported result")

// Decide returns the events that follow ev for job in pipeline p. It never
// reads a clock: every event it returns carries the time of ev.
func Decide(job domain.Job, ev domain.Event, p domain.Pipeline) ([]domain.Event, error) {
	switch e := ev.(type) {
	case domain.ResultSubmitted:
		return decideResult(job, e, p)
	case domain.SpecgatePassed:
		return afterGate(job, ev, domain.StageSpecgate, domain.KindSpec, e.At, p)
	case domain.GatePassed:
		return afterGate(job, ev, domain.StageGate, domain.KindBuild, e.At, p)
	case domain.HumanApproved:
		if job.Stage == domain.StageNeedsHuman {
			return []domain.Event{domain.MergeRequested{Job: job.ID, At: e.At}}, nil
		}
	case domain.MessageReceived:
		return decideMessage(job, e, p)
	}
	return nil, illegal(job, ev)
}

// Apply folds events into job and returns the new job. It does not modify
// the job it receives.
func Apply(job domain.Job, evs []domain.Event) domain.Job {
	for _, ev := range evs {
		switch e := ev.(type) {
		case domain.StageEntered:
			job.Stage, job.Passed, job.Changes = e.Stage, nil, nil
		case domain.ReviewPassed:
			job.Passed = append(slices.Clip(job.Passed), e.Role)
		case domain.ChangesRequested:
			job.Changes = append(slices.Clip(job.Changes), e.Role)
		case domain.RoundStarted:
			job.Round, job.Passed, job.Changes = e.Round, nil, nil
		case domain.NeedsHuman:
			job.Stage, job.Passed, job.Changes = domain.StageNeedsHuman, nil, nil
		case domain.MergeRequested:
			job.Stage, job.Passed, job.Changes = domain.StageMerge, nil, nil
		}
	}
	return job
}

func decideResult(job domain.Job, e domain.ResultSubmitted, p domain.Pipeline) ([]domain.Event, error) {
	i := slices.IndexFunc(p.Stages, func(s domain.Stage) bool { return s.Name == job.Stage })
	if i < 0 || e.Result == nil {
		return nil, illegal(job, e)
	}
	stage := p.Stages[i]
	if e.Result.Kind() != stage.Kind || (len(stage.Roles) > 0 && !slices.Contains(stage.Roles, e.Role)) {
		return nil, illegal(job, e)
	}
	if err := e.Result.Validate(); err != nil {
		return nil, fmt.Errorf("decide: %s result from %q: %w", stage.Kind, e.Role, err)
	}

	switch r := e.Result.(type) {
	case domain.IntakeResult:
		if r.Decision == domain.IntakeAccept {
			return next(job, p, i, e.At), nil
		}
	case domain.SpecResult:
		return []domain.Event{domain.StageEntered{Job: job.ID, Stage: domain.StageSpecgate, At: e.At}}, nil
	case domain.BuildResult:
		if r.Status == domain.BuildReady {
			return []domain.Event{domain.StageEntered{Job: job.ID, Stage: domain.StageGate, At: e.At}}, nil
		}
	case domain.ReviewResult:
		return reviewed(job, e, r, p, i)
	case domain.ApproveResult:
		if r.Decision == domain.ApproveApprove {
			return next(job, p, i, e.At), nil
		}
		return []domain.Event{domain.NeedsHuman{Job: job.ID, Reason: escalated(e.Role, r.Reasons), At: e.At}}, nil
	}
	return nil, fmt.Errorf("%w: %s result from %q in stage %q", ErrUnsupported, stage.Kind, e.Role, job.Stage)
}

// decideMessage ignores, without error, a message the event source did not
// mark as coming from an allowed person: bots, CI and strangers change nothing.
// In needs-human an allowed person's message is rework feedback: the job goes
// back to the build stage in the same round, with the text as input.
func decideMessage(job domain.Job, e domain.MessageReceived, p domain.Pipeline) ([]domain.Event, error) {
	if !e.Allowed || e.AuthorKind != domain.AuthorPerson {
		return nil, nil
	}
	if job.Stage != domain.StageNeedsHuman {
		return nil, fmt.Errorf("%w: message from %q in stage %q", ErrUnsupported, e.Author, job.Stage)
	}
	i := slices.IndexFunc(p.Stages, func(s domain.Stage) bool { return s.Kind == domain.KindBuild })
	if i < 0 {
		return nil, fmt.Errorf("decide: pipeline %q has no build stage", p.Name)
	}
	return []domain.Event{
		domain.ReworkRequested{Job: job.ID, By: e.Author, Text: e.Text, At: e.At},
		domain.StageEntered{Job: job.ID, Stage: p.Stages[i].Name, At: e.At},
	}, nil
}

// reviewed records the role's verdict and, once every role of the stage has
// answered, moves on: to the next stage when all passed, back to on_changes
// for another round when any asked for CHANGES, or to needs-human when that
// happens in the stage's last round.
func reviewed(job domain.Job, e domain.ResultSubmitted, r domain.ReviewResult, p domain.Pipeline, i int) ([]domain.Event, error) {
	if slices.Contains(job.Passed, e.Role) || slices.Contains(job.Changes, e.Role) {
		return nil, illegal(job, e)
	}
	stage := p.Stages[i]
	if r.Verdict == domain.ReviewChanges && stage.OnChanges == "" {
		return nil, fmt.Errorf("decide: review stage %q has no on_changes", stage.Name)
	}
	changes := job.Changes
	var evs []domain.Event
	if r.Verdict == domain.ReviewPass {
		evs = []domain.Event{domain.ReviewPassed{Job: job.ID, Role: e.Role, At: e.At}}
	} else {
		changes = append(slices.Clip(changes), e.Role)
		evs = []domain.Event{domain.ChangesRequested{Job: job.ID, Role: e.Role, Findings: r.Findings, At: e.At}}
	}
	for _, role := range stage.Roles {
		if role != e.Role && !slices.Contains(job.Passed, role) && !slices.Contains(changes, role) {
			return evs, nil
		}
	}
	if len(changes) == 0 {
		return append(evs, next(job, p, i, e.At)...), nil
	}
	if job.Round >= stage.MaxRounds {
		reason := fmt.Sprintf("CHANGES in the last round (%d of %d) from %s", job.Round, stage.MaxRounds, strings.Join(inOrder(stage.Roles, changes), ", "))
		return append(evs, domain.NeedsHuman{Job: job.ID, Reason: reason, At: e.At}), nil
	}
	return append(evs,
		domain.RoundStarted{Job: job.ID, Round: job.Round + 1, At: e.At},
		domain.StageEntered{Job: job.ID, Stage: stage.OnChanges, At: e.At},
	), nil
}

// escalated is the needs-human reason for an approver ESCALATE: a person
// must be able to act on it, so it keeps every reason the approver gave.
func escalated(role string, reasons []string) string {
	if len(reasons) == 0 {
		return fmt.Sprintf("%s escalated", role)
	}
	return fmt.Sprintf("%s escalated: %s", role, strings.Join(reasons, "; "))
}

// inOrder returns the roles in subset in the order the stage lists them, so
// the reason does not depend on the order reviewers answered in.
func inOrder(roles, subset []string) []string {
	var out []string
	for _, role := range roles {
		if slices.Contains(subset, role) {
			out = append(out, role)
		}
	}
	return out
}

// afterGate leaves a gate for the stage after the one of the given kind (a
// pipeline has one spec and one build stage, 04-roles.md).
func afterGate(job domain.Job, ev domain.Event, gate string, kind domain.Kind, at time.Time, p domain.Pipeline) ([]domain.Event, error) {
	i := slices.IndexFunc(p.Stages, func(s domain.Stage) bool { return s.Kind == kind })
	if job.Stage != gate || i < 0 {
		return nil, illegal(job, ev)
	}
	return next(job, p, i, at), nil
}

// next enters the stage after stage i, or requests the merge after the last.
func next(job domain.Job, p domain.Pipeline, i int, at time.Time) []domain.Event {
	if i+1 < len(p.Stages) {
		return []domain.Event{domain.StageEntered{Job: job.ID, Stage: p.Stages[i+1].Name, At: at}}
	}
	return []domain.Event{domain.MergeRequested{Job: job.ID, At: at}}
}

func illegal(job domain.Job, ev domain.Event) error {
	if ev == nil {
		return fmt.Errorf("%w: no event in stage %q", ErrIllegalTransition, job.Stage)
	}
	return fmt.Errorf("%w: %s in stage %q", ErrIllegalTransition, ev.EventName(), job.Stage)
}
