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
// (07-arquitectura.md).
type JobStore interface {
	// Create saves a new job with no events yet.
	Create(ctx context.Context, job domain.Job) error
	Load(ctx context.Context, id domain.JobID) (domain.Job, Version, error)
	// Apply takes the job and version as loaded and the events decide.Decide
	// returned for it, and saves decide.Apply(job, evs) with evs in one
	// transaction: if any part fails, neither the state nor any event is
	// saved. If the job was saved since v, it fails with ErrVersionConflict.
	// It returns the job's new version.
	Apply(ctx context.Context, job domain.Job, v Version, evs []domain.Event) (Version, error)
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
