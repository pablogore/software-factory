package decide

import (
	"errors"
	"fmt"
	"strings"

	"github.com/pablogore/software-factory/internal/domain"
)

// ErrOtherInitiative means the event is addressed to a different initiative,
// or to a slice job other than the one the initiative waits on. Nothing
// changes.
var ErrOtherInitiative = errors.New("event for another initiative")

// Initiative returns the events that follow ev for initiative in.
// Like Decide it is pure: every event it returns carries the time of ev.
// As in Decide, a message not from an allowed person changes nothing and is
// not an error.
func Initiative(in domain.Initiative, ev domain.Event) ([]domain.Event, error) {
	if isNil(ev) {
		return nil, fmt.Errorf("%w: no event in initiative stage %q", ErrIllegalTransition, in.Stage)
	}
	if ev.JobID() != in.Job {
		return nil, fmt.Errorf("%w: %s for job %q, initiative %q waits on job %q", ErrOtherInitiative, ev.EventName(), ev.JobID(), in.ID, in.Job)
	}
	if m, ok := ev.(domain.MessageReceived); ok && ignored(m.Allowed, m.AuthorKind) {
		return nil, nil
	}
	switch e := ev.(type) {
	case domain.MessageReceived:
		if strings.TrimSpace(e.Author) == "" {
			return nil, fmt.Errorf("decide: message without who sent it: %w", domain.ErrMissingField)
		}
		// A person's answer is the only way out of needs-human: the replan
		// runs again with it.
		if in.Stage == domain.InitiativeNeedsHuman {
			return []domain.Event{domain.InitiativeAnswered{Job: in.Job, Initiative: in.ID, By: e.Author, Text: e.Text, At: e.At}}, nil
		}
	case domain.ReplanSubmitted:
		if e.Initiative != in.ID {
			return nil, fmt.Errorf("%w: %s for initiative %q decided for %q", ErrOtherInitiative, ev.EventName(), e.Initiative, in.ID)
		}
		if in.Stage == domain.InitiativeReplan {
			return replanned(in, e)
		}
	case domain.HumanApproved:
		if strings.TrimSpace(e.By) == "" {
			return nil, fmt.Errorf("decide: approval without who gave it: %w", domain.ErrMissingField)
		}
		if in.Stage == domain.InitiativeCheckpoint {
			return []domain.Event{domain.NextSliceRequested{Job: in.Job, Initiative: in.ID, At: e.At}}, nil
		}
	}
	return nil, fmt.Errorf("%w: %s in initiative stage %q", ErrIllegalTransition, ev.EventName(), in.Stage)
}

// replanned starts the next slice on CONTINUE, right away with checkpoint
// auto and after a person's OK otherwise. DONE closes the initiative and
// NEEDS-HUMAN stops it for a person; neither starts a slice.
func replanned(in domain.Initiative, e domain.ReplanSubmitted) ([]domain.Event, error) {
	switch e.Status {
	case domain.ReplanContinue:
	case domain.ReplanDone:
		return []domain.Event{domain.InitiativeEnded{Job: e.Job, Initiative: in.ID, At: e.At}}, nil
	case domain.ReplanNeedsHuman:
		return []domain.Event{domain.InitiativeNeedsHumanRaised{Job: e.Job, Initiative: in.ID, Reason: e.Reason, At: e.At}}, nil
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
// Events of other initiatives, and job events, leave it as it is, and so
// does anything after it closed: a closed initiative never reopens. A replan
// request is refused, leaving it as it is, unless the initiative is active
// and the request comes from the slice it runs, so another or a stale slice
// job never pulls it out of a checkpoint or needs-human. Only a person's
// answer to the slice it stopped on takes it from needs-human back to the
// replan.
func ApplyInitiative(in domain.Initiative, evs []domain.Event) domain.Initiative {
	for _, ev := range evs {
		if in.Stage == domain.InitiativeClosed {
			return in
		}
		switch e := ev.(type) {
		case domain.ReplanRequested:
			if e.Initiative == in.ID && in.Stage == domain.InitiativeActive && in.Job != "" && e.Job == in.Job {
				in.Stage = domain.InitiativeReplan
			}
		case domain.CheckpointReached:
			if e.Initiative == in.ID {
				in.Stage = domain.InitiativeCheckpoint
			}
		case domain.NextSliceRequested:
			if e.Initiative == in.ID {
				in.Stage, in.Job = domain.InitiativeActive, ""
			}
		case domain.SliceStarted:
			if e.Initiative == in.ID && in.Stage == domain.InitiativeActive && in.Job == "" {
				in.Job = e.Job
			}
		case domain.InitiativeEnded:
			if e.Initiative == in.ID {
				in.Stage = domain.InitiativeClosed
			}
		case domain.InitiativeNeedsHumanRaised:
			if e.Initiative == in.ID {
				in.Stage, in.Reason = domain.InitiativeNeedsHuman, e.Reason
			}
		case domain.InitiativeAnswered:
			if e.Initiative == in.ID && in.Stage == domain.InitiativeNeedsHuman && e.Job == in.Job {
				in.Stage, in.Reason = domain.InitiativeReplan, ""
			}
		}
	}
	return in
}
