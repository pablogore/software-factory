// Package outbox delivers the external actions JobStore.Apply saved with a
// state change. It reads what to deliver from the outbox, never from memory,
// so an action saved before a stop is delivered after it (07-arquitectura.md).
//
// An action that fails to deliver is retried on the bus's schedule (1s, 2s,
// 4s, 8s and 16s, each wait capped at a minute); when the last retry fails
// too, the action is failed and stays listed with its attempts. Attempts, the
// last error and the next attempt are saved in the outbox, not logged.
//
// A delivery whose result was lost, as after a timeout, is retried the same
// way, but the retry first asks the destination whether the action was done,
// so a comment or a push is never repeated.
//
// An action the destination refused for permission or validation is never
// retried: it is failed at once and, in the same step, the job that asked
// for it goes to needs-human.
//
// Nothing runs Deliver on a schedule yet: the scheduler will call it.
package outbox

import (
	"context"
	"errors"
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
// one more failed attempt, which gives up past the last retry. When q's last
// result was lost, the destination is asked first and q is delivered only if
// it was not done. A refusal fails q for good. Only the outbox's errors are
// returned; the destination's goes into the failure.
func (d *Deliverer) try(ctx context.Context, q ports.QueuedAction) error {
	if q.ResultLost {
		done, err := d.dest.Done(ctx, q.Key)
		if errors.Is(err, ports.ErrRefused) {
			return d.refused(ctx, q, err, true)
		}
		if err != nil {
			// Still unknown whether it was done, so the next attempt asks again.
			return d.failed(ctx, q, err, true)
		}
		if done {
			return d.outbox.MarkActionDelivered(ctx, q.Key)
		}
	}
	derr := d.dest.Deliver(ctx, q.Action)
	if derr == nil {
		return d.outbox.MarkActionDelivered(ctx, q.Key)
	}
	if errors.Is(derr, ports.ErrRefused) {
		return d.refused(ctx, q, derr, false)
	}
	return d.failed(ctx, q, derr, errors.Is(derr, ports.ErrResultLost))
}

// refused saves q as failed with err, one more attempt and no retry, and
// sends its job to needs-human in the same step. lost says whether the
// action may have been done.
func (d *Deliverer) refused(ctx context.Context, q ports.QueuedAction, err error, lost bool) error {
	f := q.ActionFailure
	f.Attempts++
	f.LastError = err.Error()
	f.ResultLost = lost
	f.Failed = true
	f.NextAttempt = time.Time{}
	return d.outbox.MarkActionRefused(ctx, q.Key, f, d.clock.Now())
}

// failed saves one more failed attempt of q with err, rescheduled on the
// backoff schedule or given up past the last retry. lost says whether the
// action may have been done.
func (d *Deliverer) failed(ctx context.Context, q ports.QueuedAction, err error, lost bool) error {
	f := q.ActionFailure
	f.Attempts++
	f.LastError = err.Error()
	f.ResultLost = lost
	f.Failed = f.Attempts > backoff.Retries
	f.NextAttempt = time.Time{}
	if !f.Failed {
		f.NextAttempt = d.clock.Now().Add(backoff.Wait(f.Attempts))
	}
	return d.outbox.MarkActionFailed(ctx, q.Key, f)
}
