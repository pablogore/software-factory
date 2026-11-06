package decide

import (
	"errors"
	"fmt"

	"github.com/pablogore/software-factory/internal/domain"
)

// ErrOtherInitiative means the event is addressed to a different initiative,
// or to a slice job other than the one the initiative waits on. Nothing
// changes.
var ErrOtherInitiative = errors.New("event for another initiative")

// Initiative returns the events that follow ev for initiative in.
// Like Decide it is pure: every event it returns carries the time of ev.
func Initiative(in domain.Initiative, ev domain.Event) ([]domain.Event, error) {
	if isNil(ev) {
		return nil, fmt.Errorf("%w: no event in initiative stage %q", ErrIllegalTransition, in.Stage)
	}
	if ev.JobID() != in.Job {
		return nil, fmt.Errorf("%w: %s for job %q, initiative %q waits on job %q", ErrOtherInitiative, ev.EventName(), ev.JobID(), in.ID, in.Job)
	}
	switch e := ev.(type) {
	case domain.ReplanSubmitted:
		if e.Initiative != in.ID {
			return nil, fmt.Errorf("%w: %s for initiative %q decided for %q", ErrOtherInitiative, ev.EventName(), e.Initiative, in.ID)
		}
		if in.Stage == domain.InitiativeReplan {
			return replanned(in, e)
		}
	case domain.HumanApproved:
		if in.Stage == domain.InitiativeCheckpoint {
			return []domain.Event{domain.NextSliceRequested{Job: in.Job, Initiative: in.ID, At: e.At}}, nil
		}
	}
	return nil, fmt.Errorf("%w: %s in initiative stage %q", ErrIllegalTransition, ev.EventName(), in.Stage)
}

// replanned starts the next slice on CONTINUE, right away with checkpoint
// auto and after a person's OK otherwise. DONE and NEEDS-HUMAN come in E01.6 S2.
func replanned(in domain.Initiative, e domain.ReplanSubmitted) ([]domain.Event, error) {
	switch e.Status {
	case domain.ReplanContinue:
	case domain.ReplanDone, domain.ReplanNeedsHuman:
		return nil, fmt.Errorf("%w: replan %s for initiative %q", ErrUnsupported, e.Status, in.ID)
	default:
		return nil, fmt.Errorf("decide: replan status %q: %w", e.Status, domain.ErrInvalidValue)
	}
	switch in.Checkpoint {
	case domain.CheckpointAuto:
		return []domain.Event{domain.NextSliceRequested{Job: e.Job, Initiative: in.ID, At: e.At}}, nil
	case domain.CheckpointEverySlice, "":
		return []domain.Event{domain.CheckpointReached{Job: e.Job, Initiative: in.ID, At: e.At}}, nil
	}
	return nil, fmt.Errorf("decide: initiative %q checkpoint %q: %w", in.ID, in.Checkpoint, domain.ErrInvalidValue)
}

// ApplyInitiative folds events into in and returns the new initiative.
// Events of other initiatives, and job events, leave it as it is.
func ApplyInitiative(in domain.Initiative, evs []domain.Event) domain.Initiative {
	for _, ev := range evs {
		switch e := ev.(type) {
		case domain.ReplanRequested:
			if e.Initiative == in.ID {
				in.Stage, in.Job = domain.InitiativeReplan, e.Job
			}
		case domain.CheckpointReached:
			if e.Initiative == in.ID {
				in.Stage = domain.InitiativeCheckpoint
			}
		case domain.NextSliceRequested:
			if e.Initiative == in.ID {
				in.Stage = domain.InitiativeActive
			}
		}
	}
	return in
}
