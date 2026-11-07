// Package outbox delivers the external actions JobStore.Apply saved with a
// state change. It reads what to deliver from the outbox, never from memory,
// so an action saved before a stop is delivered after it (07-arquitectura.md).
//
// An action that fails to deliver is retried on the bus's schedule (1s, 2s,
// 4s, 8s and 16s, each wait capped at a minute); when the last retry fails
// too, the action is failed and stays listed with its attempts. Attempts, the
// last error and the next attempt are saved in the outbox, not logged.
//
// Nothing runs Deliver on a schedule yet: the scheduler will call it.
package outbox

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/pablogore/software-factory/internal/platform/backoff"
	"github.com/pablogore/software-factory/internal/ports"
)

// Deliverer hands due actions to their destination and saves how it went.
type Deliverer struct {
	outbox ports.Outbox
	dest   ports.Destination
	clock  ports.Clock

	// mu serializes Deliver, so two runs never hand the same action to the
	// destination at once.
	mu sync.Mutex
}

// New returns a deliverer of outbox's actions to dest. clock tells which
// actions are due.
func New(outbox ports.Outbox, dest ports.Destination, clock ports.Clock) *Deliverer {
	return &Deliverer{outbox: outbox, dest: dest, clock: clock}
}

// Deliver tries once, in saved order, every action that is due. It never
// waits for a retry: a failed delivery is saved to be retried later and
// Deliver goes on. It returns an error only when the outbox fails.
func (d *Deliverer) Deliver(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	due, err := d.outbox.DueActions(ctx, d.clock.Now())
	if err != nil {
		return fmt.Errorf("outbox: deliver: %w", err)
	}
	for _, q := range due {
		if err := d.try(ctx, q); err != nil {
			return fmt.Errorf("outbox: deliver: %w", err)
		}
	}
	return nil
}

// try hands q to the destination once and saves the outcome: delivered, or
// one more failed attempt, which gives up past the last retry. Only the
// outbox's errors are returned; the destination's goes into the failure.
func (d *Deliverer) try(ctx context.Context, q ports.QueuedAction) error {
	derr := d.dest.Deliver(ctx, q.Action)
	if derr == nil {
		return d.outbox.MarkActionDelivered(ctx, q.Key)
	}
	f := q.ActionFailure
	f.Attempts++
	f.LastError = derr.Error()
	f.Failed = f.Attempts > backoff.Retries
	f.NextAttempt = time.Time{}
	if !f.Failed {
		f.NextAttempt = d.clock.Now().Add(backoff.Wait(f.Attempts))
	}
	return d.outbox.MarkActionFailed(ctx, q.Key, f)
}
