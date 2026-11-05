package decide

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/pablogore/software-factory/internal/domain"
)

// ErrIllegalTransition means the event does not fit the job's current stage.
// Decide returns no events with it, so nothing changes.
var ErrIllegalTransition = errors.New("illegal transition")

// ErrUnsupported means the result is legal in this stage but its outcome
// (CHANGES, NEEDS_INFO, REJECT, BLOCKED, ESCALATE, task results) is not
// decided yet; E01.4 adds it.
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
	}
	return nil, illegal(job, ev)
}

// Apply folds events into job and returns the new job. It does not modify
// the job it receives.
func Apply(job domain.Job, evs []domain.Event) domain.Job {
	for _, ev := range evs {
		switch e := ev.(type) {
		case domain.StageEntered:
			job.Stage, job.Passed = e.Stage, nil
		case domain.ReviewPassed:
			job.Passed = append(slices.Clip(job.Passed), e.Role)
		case domain.MergeRequested:
			job.Stage, job.Passed = domain.StageMerge, nil
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
		if r.Verdict == domain.ReviewPass {
			return reviewPassed(job, e, p, i)
		}
	case domain.ApproveResult:
		if r.Decision == domain.ApproveApprove {
			return next(job, p, i, e.At), nil
		}
	}
	return nil, fmt.Errorf("%w: %s result from %q in stage %q", ErrUnsupported, stage.Kind, e.Role, job.Stage)
}

// reviewPassed records the role's PASS and moves on once every role of the
// stage has passed.
func reviewPassed(job domain.Job, e domain.ResultSubmitted, p domain.Pipeline, i int) ([]domain.Event, error) {
	if slices.Contains(job.Passed, e.Role) {
		return nil, illegal(job, e)
	}
	evs := []domain.Event{domain.ReviewPassed{Job: job.ID, Role: e.Role, At: e.At}}
	for _, role := range p.Stages[i].Roles {
		if role != e.Role && !slices.Contains(job.Passed, role) {
			return evs, nil
		}
	}
	return append(evs, next(job, p, i, e.At)...), nil
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
