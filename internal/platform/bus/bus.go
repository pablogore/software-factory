// Package bus delivers saved domain events to the slices that subscribe to
// them. It reads what to deliver from the event feed, never from memory, so
// an event saved before a stop and not yet handled is delivered on the next
// Start (07-arquitectura.md).
//
// Delivery is at least once: a stop between a handler's success and saving
// it as processed, a retry, or a person retrying a dead event hands a
// handler an event again. Every handler must therefore be idempotent.
//
// A handler that fails is retried after 1s, 2s, 4s, 8s and 16s (each wait
// capped at a minute); when the last retry fails too, the event is dead for
// that subscriber. Attempts, the last error and the next attempt are saved in
// the feed, not logged. Per subscriber and per job, events keep their saved
// order: while one is retrying or dead, that subscriber's later events for
// the same job wait, and its events for other jobs and other subscribers go
// on.
package bus

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/ports"
)

// Bus errors. Callers compare with errors.Is.
var (
	// ErrNotStarted means Dispatch ran before Start replayed what was
	// pending: new events wait until it has.
	ErrNotStarted = errors.New("bus not started")
	// ErrStarted means Subscribe or Start ran after Start succeeded.
	ErrStarted = errors.New("bus already started")
	// ErrSubscriber means a subscription has no name, no events, no handler
	// or a name already taken.
	ErrSubscriber = errors.New("invalid subscriber")
)

const (
	// retries is how many times a failed event is tried again before it is
	// dead.
	retries   = 5
	firstWait = time.Second
	maxWait   = time.Minute
)

// wait is how long to wait after the nth failed attempt: it doubles from
// firstWait, never past maxWait.
func wait(n int) time.Duration {
	w := firstWait
	for i := 1; i < n && w < maxWait; i++ {
		w *= 2
	}
	return min(w, maxWait)
}

// Handler handles one event. Returning nil marks the event processed for
// its subscriber; an error leaves it to be retried. A handler must be
// idempotent, since the bus may hand it the same event more than once, and
// must not call Dispatch.
type Handler func(ctx context.Context, ev domain.Event) error

type subscriber struct {
	name   string
	events []string
	handle Handler
}

// Bus hands each subscriber, oldest first, the saved events it registered
// for and has not processed.
type Bus struct {
	feed  ports.EventFeed
	clock ports.Clock

	// mu serializes Start and Dispatch, so two of them never deliver the
	// same event to one subscriber at once.
	mu      sync.Mutex
	subs    []subscriber
	started bool
}

// New returns a bus over feed, with no subscribers and not started. clock
// tells when a failed event is due again.
func New(feed ports.EventFeed, clock ports.Clock) *Bus {
	return &Bus{feed: feed, clock: clock}
}

// Subscribe registers h under name for the events named in events. name is
// what the feed saves as processed, so it must stay the same across
// restarts. Subscribers register before Start. h must be idempotent.
func (b *Bus) Subscribe(name string, h Handler, events ...string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.started {
		return fmt.Errorf("bus: subscribe %q: %w", name, ErrStarted)
	}
	switch {
	case name == "":
		return fmt.Errorf("bus: subscriber without a name: %w", ErrSubscriber)
	case len(events) == 0:
		return fmt.Errorf("bus: subscriber %q without events: %w", name, ErrSubscriber)
	case h == nil:
		return fmt.Errorf("bus: subscriber %q without a handler: %w", name, ErrSubscriber)
	}
	for _, s := range b.subs {
		if s.name == name {
			return fmt.Errorf("bus: subscriber %q already registered: %w", name, ErrSubscriber)
		}
	}
	b.subs = append(b.subs, subscriber{name: name, events: events, handle: h})
	return nil
}

// Start tries once every pending event that is due, for every subscriber,
// and only then accepts Dispatch. It never waits for a retry: a handler that
// fails is saved to be retried later and Start goes on. It returns an error,
// and the bus stays not started, only when the feed fails; calling Start
// again resumes from what is pending.
func (b *Bus) Start(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.started {
		return fmt.Errorf("bus: start: %w", ErrStarted)
	}
	if err := b.deliver(ctx); err != nil {
		return fmt.Errorf("bus: start: %w", err)
	}
	b.started = true
	return nil
}

// Dispatch tries once every pending event that is due: the ones saved since
// the last run and the retries whose wait is over. Callers run it after
// saving events and when a retry falls due. Like Start it never waits, and
// it returns an error only when the feed fails.
func (b *Bus) Dispatch(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.started {
		return fmt.Errorf("bus: dispatch: %w", ErrNotStarted)
	}
	if err := b.deliver(ctx); err != nil {
		return fmt.Errorf("bus: dispatch: %w", err)
	}
	return nil
}

// deliver runs every subscriber, so one whose feed fails does not hold back
// the others, and joins their errors.
func (b *Bus) deliver(ctx context.Context) error {
	var errs []error
	for _, s := range b.subs {
		if err := b.drain(ctx, s); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// drain hands s its due events until none is left, so an event a handler
// saves is delivered in the same run. Each event is tried at most once per
// run, so a run never retries before the wait and always ends.
func (b *Bus) drain(ctx context.Context, s subscriber) error {
	tried := map[ports.Seq]bool{}
	for {
		n, err := b.pass(ctx, s, tried)
		if err != nil {
			return fmt.Errorf("subscriber %q: %w", s.name, err)
		}
		if n == 0 {
			return nil
		}
	}
}

// pass tries, in saved order, each of s's pending events that was not tried
// in this run, is due, and has no earlier event of its job left pending for
// s. It returns how many it tried.
func (b *Bus) pass(ctx context.Context, s subscriber, tried map[ports.Seq]bool) (int, error) {
	pending, err := b.feed.Pending(ctx, s.name, s.events)
	if err != nil {
		return 0, err
	}
	if len(pending) == 0 {
		return 0, nil
	}
	ds, err := b.feed.Deliveries(ctx, s.name)
	if err != nil {
		return 0, err
	}
	failed := map[ports.Seq]ports.Failure{}
	for _, d := range ds {
		failed[d.Seq] = d.Failure
	}
	now := b.clock.Now()
	held := map[domain.JobID]bool{}
	n := 0
	for _, p := range pending {
		job := p.Event.JobID()
		f, ok := failed[p.Seq]
		if held[job] || tried[p.Seq] || f.Dead || (ok && now.Before(f.NextAttempt)) {
			held[job] = true
			continue
		}
		tried[p.Seq] = true
		n++
		done, err := b.try(ctx, s, p, f)
		if err != nil {
			return n, err
		}
		if !done {
			held[job] = true
		}
	}
	return n, nil
}

// try hands p to s once and saves the outcome: processed, or one more failed
// attempt after f, which is dead past the last retry. It reports whether s
// processed p; only the feed's errors are returned, the handler's goes into
// the failure.
func (b *Bus) try(ctx context.Context, s subscriber, p ports.Saved, f ports.Failure) (bool, error) {
	herr := s.handle(ctx, p.Event)
	if herr == nil {
		return true, b.feed.MarkProcessed(ctx, s.name, p.Seq)
	}
	f.Attempts++
	f.LastError = herr.Error()
	f.Dead = f.Attempts > retries
	f.NextAttempt = time.Time{}
	if !f.Dead {
		f.NextAttempt = b.clock.Now().Add(wait(f.Attempts))
	}
	return false, b.feed.MarkFailed(ctx, s.name, p.Seq, f)
}
