package ports

import (
	"context"
	"errors"
	"time"

	"github.com/pablogore/software-factory/internal/domain"
)

// Store errors. Callers compare with errors.Is; the wrapped message names the
// job or the event at fault.
var (
	ErrJobNotFound  = errors.New("job not found")
	ErrJobExists    = errors.New("job already exists")
	ErrUnknownEvent = errors.New("unknown event type")
	// ErrVersionConflict means another writer saved the job after it was
	// loaded: nothing was saved, and the caller must load it again.
	ErrVersionConflict = errors.New("version conflict")
)

// Version counts a job's saves. The store keeps it beside the job, not in
// domain.Job: Load hands it out and Apply saves only if it is still current.
type Version int64

// JobStore keeps each job's current state. Apply saves the state and the
// events that explain it together, so a state never exists without its events
// (07-arquitectura.md). Every job a JobStore saves or loads passes
// domain.Job.Validate; one that does not is an error naming the field.
type JobStore interface {
	// Create saves a new job with no events yet. acts are saved to the
	// Outbox with it, pending, in the same transaction, so a new job's
	// first effects exist only if the job does. When the job exists
	// already (ErrJobExists) or an action fails Validate, nothing is saved.
	// An action whose Key is already saved, or repeats one in acts, is kept
	// once and is not an error.
	Create(ctx context.Context, job domain.Job, acts ...Action) error
	Load(ctx context.Context, id domain.JobID) (domain.Job, Version, error)
	// Apply runs decide.Decide for ev on job id as saved at version v, in
	// pipeline p, and saves decide.Apply(job, evs) with the evs Decide
	// returned in one transaction. Only Decide's events are saved, never ev
	// itself, so no caller can save an event Decide did not return: when
	// Decide refuses ev, Apply returns its error and saves nothing. If any part
	// fails, neither the state nor any event is saved. If the job was saved
	// since v, it fails with ErrVersionConflict. It returns Decide's events
	// and the job's new version; when Decide returns none, nothing is saved
	// and the version stays v.
	//
	// acts are the external actions the state change asks for. They are saved
	// to the Outbox in the same transaction, pending, so they exist only if
	// the state does. An action whose Key is already saved, or repeats one
	// in acts, is kept once and is not an error; one that fails Validate
	// saves nothing.
	Apply(ctx context.Context, id domain.JobID, v Version, p domain.Pipeline, ev domain.Event, acts ...Action) ([]domain.Event, Version, error)
}

// Record is one saved event in a job's history. At is the event's own time:
// the store reads no clock.
type Record struct {
	Event domain.Event
	At    time.Time
}

// EventLog reads back what JobStore.Apply saved.
type EventLog interface {
	// History lists the job's events in the order they were saved.
	History(ctx context.Context, id domain.JobID) ([]Record, error)
}
