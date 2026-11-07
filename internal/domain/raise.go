package domain

import (
	"errors"
	"fmt"
)

// RaiseRefusal is why a person's raise of a cap (CapRaiseRequested) resumed
// nothing. It is a closed set of stable codes, never text for a person: each
// channel shows it in its own words, in the configured language.
type RaiseRefusal string

// The refusals, one per way a raise can be invalid.
const (
	RaiseNoAuthor   RaiseRefusal = "raise-no-author"   // the raise names nobody
	RaiseUnknownCap RaiseRefusal = "raise-unknown-cap" // the cap is not one of IssueCaps
	RaiseBadValue   RaiseRefusal = "raise-bad-value"   // the value is not a number the cap can take
	RaiseStaleWait  RaiseRefusal = "raise-stale-wait"  // the raise answers no wait, or another one
	RaiseNotStopped RaiseRefusal = "raise-not-stopped" // nothing waits stopped at that cap
	RaiseNotAbove   RaiseRefusal = "raise-not-above"   // the value does not pass the one reached
)

var raiseRefusals = []RaiseRefusal{RaiseNoAuthor, RaiseUnknownCap, RaiseBadValue, RaiseStaleWait, RaiseNotStopped, RaiseNotAbove}

// RaiseRefusals lists every refusal. It returns a copy, so changing it
// changes neither the set nor what Validate accepts.
func RaiseRefusals() []RaiseRefusal {
	return append([]RaiseRefusal(nil), raiseRefusals...)
}

// Validate returns an error when r is not one of RaiseRefusals.
func (r RaiseRefusal) Validate() error {
	return oneOf("raise refusal", r, raiseRefusals...)
}

// ErrRaiseRefused means a person's raise of a cap was invalid, so nothing
// resumed. The error is a *RaiseRefusedError, which carries the code.
var ErrRaiseRefused = errors.New("cap raise refused")

// RaiseRefusedError is a refused raise: Code is why, for every channel, and
// Detail the same in words for logs.
type RaiseRefusedError struct {
	Code   RaiseRefusal
	Detail string
}

// RefuseRaise returns the refusal of a raise for code, with detail in words.
func RefuseRaise(code RaiseRefusal, format string, args ...any) error {
	return &RaiseRefusedError{Code: code, Detail: fmt.Sprintf(format, args...)}
}

func (e *RaiseRefusedError) Error() string {
	return fmt.Sprintf("%v: %s: %s", ErrRaiseRefused, e.Code, e.Detail)
}

// Unwrap makes errors.Is(err, ErrRaiseRefused) hold.
func (e *RaiseRefusedError) Unwrap() error { return ErrRaiseRefused }

// RaiseRefusalOf returns the code of a refused raise in err's chain, or
// false when err is not one.
func RaiseRefusalOf(err error) (RaiseRefusal, bool) {
	var r *RaiseRefusedError
	if errors.As(err, &r) {
		return r.Code, true
	}
	return "", false
}
