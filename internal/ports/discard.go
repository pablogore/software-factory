package ports

import (
	"context"
	"time"
)

// Discarded is a request that created nothing because its author is not on
// its provider's allowlist. There is no job to hang it on, so it is kept on
// its own for a person to see who tried. The request's text is not kept: it
// came from someone the factory does not trust.
type Discarded struct {
	Provider string
	Thread   string
	// Request is the provider's id of the request, which tells a repeat.
	Request string
	Author  string
	At      time.Time
}

// DiscardLog keeps the requests that were discarded.
type DiscardLog interface {
	// Discard saves d. A request already saved, by provider, thread and
	// request, keeps the first one and is not an error, since a provider
	// may hand the same request twice.
	Discard(ctx context.Context, d Discarded) error
	// Discards lists every saved request in the order it was saved.
	Discards(ctx context.Context) ([]Discarded, error)
}
