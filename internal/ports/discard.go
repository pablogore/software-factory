package ports

import (
	"context"
	"time"
	"unicode/utf8"
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

// Every field of a Discarded came from someone the factory does not trust,
// so a DiscardLog bounds what it keeps of them.
const (
	// MaxDiscardField is how many bytes of each of a Discarded's provider,
	// thread, request and author a DiscardLog keeps.
	MaxDiscardField = 256
	// MaxDiscards is how many requests a DiscardLog keeps: saving one more
	// drops the oldest.
	MaxDiscards = 1000
)

// Capped is d with each field cut to MaxDiscardField bytes, at a rune's
// start, so what is kept is still valid UTF-8.
func (d Discarded) Capped() Discarded {
	d.Provider = capField(d.Provider)
	d.Thread = capField(d.Thread)
	d.Request = capField(d.Request)
	d.Author = capField(d.Author)
	return d
}

func capField(s string) string {
	if len(s) <= MaxDiscardField {
		return s
	}
	n := MaxDiscardField
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// DiscardLog keeps the requests that were discarded.
type DiscardLog interface {
	// Discard saves d, Capped. A request already saved, by provider, thread
	// and request, keeps the first one and is not an error, since a provider
	// may hand the same request twice. Past MaxDiscards requests the oldest
	// are dropped.
	Discard(ctx context.Context, d Discarded) error
	// Discards lists every saved request in the order it was saved.
	Discards(ctx context.Context) ([]Discarded, error)
}
