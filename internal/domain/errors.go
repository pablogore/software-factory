package domain

import "errors"

// Validation errors. Callers compare with errors.Is; the wrapped message names
// the field or value at fault.
var (
	ErrMissingField = errors.New("missing field")
	ErrInvalidValue = errors.New("invalid value")
)
