// Package bus delivers saved domain events to the slices that subscribe to
// them. It reads what to deliver from the event feed, never from memory, so
// an event saved before a stop and not yet handled is delivered on the next
// Start (07-arquitectura.md). Delivery is at least once: a stop between a
// handler's success and saving it as processed delivers that event again.
package bus

import (
	"context"
	"errors"
	"fmt"
	"sync"

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

// Handler handles one event. Returning nil marks the event processed for
// its subscriber; an error leaves it pending. A handler must not call
// Dispatch.
type Handler func(ctx context.Context, ev domain.Event) error

type subscriber struct {
	name   string
	events []string
	handle Handler
}

// Bus hands each subscriber, oldest first, the saved events it registered
// for and has not processed.
type Bus struct {
	feed ports.EventFeed

	// mu serializes Start and Dispatch, so two of them never deliver the
	// same event to one subscriber at once.
	mu      sync.Mutex
	subs    []subscriber
	started bool
}

// New returns a bus over feed, with no subscribers and not started.
func New(feed ports.EventFeed) *Bus {
	return &Bus{feed: feed}
}

// Subscribe registers h under name for the events named in events. name is
// what the feed saves as processed, so it must stay the same across
// restarts. Subscribers register before Start.
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

// Start delivers to every subscriber what it has not processed and only
// then accepts Dispatch. If any delivery fails it returns the error and the
// bus stays not started; calling Start again resumes from what is pending.
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

// Dispatch delivers to every subscriber the events saved since it last
// handled one. Callers run it after saving events.
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

// deliver runs every subscriber, so one that fails does not hold back the
// others, and joins their errors.
func (b *Bus) deliver(ctx context.Context) error {
	var errs []error
	for _, s := range b.subs {
		if err := b.drain(ctx, s); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// drain hands s its pending events in saved order until none is left, so an
// event a handler saves is delivered in the same run. It stops at the first
// failure: a later event never overtakes one still pending.
func (b *Bus) drain(ctx context.Context, s subscriber) error {
	for {
		pending, err := b.feed.Pending(ctx, s.name, s.events)
		if err != nil {
			return fmt.Errorf("subscriber %q: %w", s.name, err)
		}
		if len(pending) == 0 {
			return nil
		}
		for _, p := range pending {
			if err := s.handle(ctx, p.Event); err != nil {
				return fmt.Errorf("subscriber %q, event %d (%s): %w", s.name, p.Seq, p.Event.EventName(), err)
			}
			if err := b.feed.MarkProcessed(ctx, s.name, p.Seq); err != nil {
				return fmt.Errorf("subscriber %q: %w", s.name, err)
			}
		}
	}
}
