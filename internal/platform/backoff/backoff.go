// Package backoff is the retry schedule the bus and the outbox share, so a
// failed event and a failed external action wait alike: 1s, 2s, 4s, 8s and
// 16s, each wait capped at a minute.
package backoff

import "time"

const (
	// Retries is how many times a failure is tried again before giving up.
	Retries   = 5
	firstWait = time.Second
	maxWait   = time.Minute
)

// Wait is how long to wait after the nth failed attempt: it doubles from
// firstWait, never past maxWait.
func Wait(n int) time.Duration {
	w := firstWait
	for i := 1; i < n && w < maxWait; i++ {
		w *= 2
	}
	return min(w, maxWait)
}
