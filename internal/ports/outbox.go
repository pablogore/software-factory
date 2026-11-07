package ports

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/pablogore/software-factory/internal/domain"
)

// ErrActionNotFound means the outbox has no action with the given key.
// Callers compare with errors.Is.
var ErrActionNotFound = errors.New("action not found")

// Action is one external effect a state change asks for: a comment, a push,
// a PR, a chat message. It is saved with that state change and delivered
// later, so a slice never calls the outside world itself
// (07-arquitectura.md).
type Action struct {
	// Key is the idempotency key: asking again for an action with a saved
	// Key keeps the saved one, and the destination may use it to recognise
	// a repeat.
	Key string
	// Kind says which destination handles it, such as "comment" or "push".
	Kind string
	// Payload is what the destination needs, in a form only it reads.
	Payload string
}

// Validate refuses an action without a key, which could not be told from a
// repeat, or without a kind, which no destination would handle.
func (a Action) Validate() error {
	if a.Key == "" {
		return fmt.Errorf("action of kind %q without key: %w", a.Kind, domain.ErrMissingField)
	}
	if a.Kind == "" {
		return fmt.Errorf("action %q without kind: %w", a.Key, domain.ErrMissingField)
	}
	return nil
}

// ActionFailure is what is saved about an action's failed deliveries, so
// health and stats read it from the store rather than from logs.
type ActionFailure struct {
	// Attempts counts the failed deliveries so far.
	Attempts  int
	LastError string
	// NextAttempt is when the action is due again; zero when it is due now,
	// was delivered or failed.
	NextAttempt time.Time
	// Failed means delivery gave up after the last retry. The action stays
	// listed, so it is seen, but is never due again.
	Failed bool
}

// QueuedAction is an action as the outbox keeps it: the job whose state
// change asked for it, and how its delivery went.
type QueuedAction struct {
	Job domain.JobID
	Action
	Delivered bool
	ActionFailure
}

// Outbox is what delivery reads and writes of the actions JobStore.Apply
// saved.
type Outbox interface {
	// DueActions lists, in saved order, the actions neither delivered nor
	// failed whose next attempt is not after now.
	DueActions(ctx context.Context, now time.Time) ([]QueuedAction, error)
	// Actions lists the job's actions in saved order, delivered and failed
	// ones included; a job with none lists nothing.
	Actions(ctx context.Context, id domain.JobID) ([]QueuedAction, error)
	// MarkActionDelivered records that the action with key was done,
	// keeping its failed attempts. An unknown key is ErrActionNotFound.
	MarkActionDelivered(ctx context.Context, key string) error
	// MarkActionFailed saves f as the action's failed deliveries, replacing
	// what was saved before. A failed f keeps no next attempt. An unknown
	// key is ErrActionNotFound.
	MarkActionFailed(ctx context.Context, key string, f ActionFailure) error
}

// Destination performs actions in the outside world.
type Destination interface {
	// Deliver performs a once. An error means it was not done and is worth
	// trying again, as a network failure or a 5xx answer is.
	Deliver(ctx context.Context, a Action) error
}
