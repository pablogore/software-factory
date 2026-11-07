package ports

import (
	"context"
	"errors"
	"time"

	"github.com/pablogore/software-factory/internal/domain"
)

// Feed errors. Callers compare with errors.Is.
var (
	// ErrEventNotFound means no saved event has the given Seq.
	ErrEventNotFound = errors.New("event not found")
	// ErrNotDead means the subscriber has no dead event at the given Seq.
	ErrNotDead = errors.New("event not dead")
)

// Seq is an event's place in the order all jobs' events were saved: a later
// save always has a higher Seq.
type Seq int64

// Saved is one saved event with its place in the feed.
type Saved struct {
	Seq Seq
	Record
}

// Failure is what is saved about a subscriber's failed attempts at one event,
// so health and stats read it from the store rather than from logs.
type Failure struct {
	// Attempts counts the failed attempts so far, at least 1.
	Attempts  int
	LastError string
	// NextAttempt is when the event is due again; zero once it is dead.
	NextAttempt time.Time
	// Dead means the subscriber gave up: only a person's RetryDead or
	// DiscardDead moves the event on.
	Dead bool
}

// Delivery is a subscriber's failed delivery of one event of one job.
type Delivery struct {
	Subscriber string
	Seq        Seq
	Job        domain.JobID
	Failure
}

// EventFeed lets the bus hand every subscriber the saved events it has not
// processed yet, so a stop between saving an event and handling it leaves
// the event pending instead of lost. What a subscriber processed, and its
// failed deliveries, are kept per subscriber name and per job.
type EventFeed interface {
	// Pending lists, in saved order, the events whose name is in names that
	// subscriber has not marked processed, dead ones included. No names
	// lists nothing.
	Pending(ctx context.Context, subscriber string, names []string) ([]Saved, error)
	// MarkProcessed records that subscriber handled the event at seq, so
	// Pending no longer lists it for subscriber, and clears its failed
	// delivery. Marking it again is a no-op; an unknown seq is
	// ErrEventNotFound.
	MarkProcessed(ctx context.Context, subscriber string, seq Seq) error
	// MarkFailed saves f as subscriber's failed delivery of the event at
	// seq, replacing the one before. A dead f moves it to the dead events.
	// An unknown seq is ErrEventNotFound.
	MarkFailed(ctx context.Context, subscriber string, seq Seq, f Failure) error
	// Deliveries lists subscriber's failed deliveries, retrying and dead,
	// in saved order.
	Deliveries(ctx context.Context, subscriber string) ([]Delivery, error)
}

// DeadEvents is what a person does with an event a subscriber gave up on.
// Until one of them runs, that subscriber's later events for the same job
// wait.
type DeadEvents interface {
	// RetryDead makes the dead event at seq pending again for subscriber,
	// with no failed attempts, so the bus delivers it next.
	RetryDead(ctx context.Context, subscriber string, seq Seq) error
	// DiscardDead drops the dead event at seq for subscriber: it counts as
	// processed and is never delivered to subscriber again.
	DiscardDead(ctx context.Context, subscriber string, seq Seq) error
}
