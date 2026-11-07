package ports

import "time"

// Clock tells the time to code that must not read it itself, so tests move
// it instead of waiting.
type Clock interface {
	Now() time.Time
}
