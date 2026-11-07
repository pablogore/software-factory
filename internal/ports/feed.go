package ports

import "context"

// Seq is an event's place in the order all jobs' events were saved: a later
// save always has a higher Seq.
type Seq int64

// Saved is one saved event with its place in the feed.
type Saved struct {
	Seq Seq
	Record
}

// EventFeed lets the bus hand every subscriber the saved events it has not
// processed yet, so a stop between saving an event and handling it leaves
// the event pending instead of lost. What a subscriber processed is kept per
// subscriber name.
type EventFeed interface {
	// Pending lists, in saved order, the events whose name is in names that
	// subscriber has not marked processed. No names lists nothing.
	Pending(ctx context.Context, subscriber string, names []string) ([]Saved, error)
	// MarkProcessed records that subscriber handled the event at seq, so
	// Pending no longer lists it for subscriber. Marking it again is a no-op.
	MarkProcessed(ctx context.Context, subscriber string, seq Seq) error
}
