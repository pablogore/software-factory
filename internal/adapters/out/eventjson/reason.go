package eventjson

import (
	"fmt"

	"github.com/pablogore/software-factory/internal/domain"
)

// DefaultMaxReason is how many characters of an override's reason a store
// keeps when whoever opens it sets no maximum.
const DefaultMaxReason = 4096

// CheckMaxReason refuses a maximum no reason could be kept under.
func CheckMaxReason(n int) error {
	if n < 1 {
		return fmt.Errorf("eventjson: %w: max reason %d, want at least 1", domain.ErrInvalidValue, n)
	}
	return nil
}

// CapReasons returns evs with each MergeOverridden.Reason cut to its first
// limit characters, never mid-character, and the rest kept word for word.
// Decide does not limit human text, so the store does before saving it; it
// cuts rather than refuses, so an override is never lost.
func CapReasons(evs []domain.Event, limit int) []domain.Event {
	out := make([]domain.Event, len(evs))
	for i, ev := range evs {
		if e, ok := ev.(domain.MergeOverridden); ok {
			e.Reason = truncate(e.Reason, limit)
			ev = e
		}
		out[i] = ev
	}
	return out
}

func truncate(s string, limit int) string {
	n := 0
	for i := range s {
		if n == limit {
			return s[:i]
		}
		n++
	}
	return s
}
