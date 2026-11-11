package domain

import "errors"

// Validation errors. Callers compare with errors.Is; the wrapped message names
// the field or value at fault.
var (
	ErrMissingField = errors.New("missing field")
	ErrInvalidValue = errors.New("invalid value")
)

// ErrBusy means the operation is already running for the same job, and a
// second one is refused rather than queued.
var ErrBusy = errors.New("already running")

// ErrTimedOut means the operation ran over its time limit and was stopped:
// it has no result, not a failed one.
var ErrTimedOut = errors.New("timed out")
