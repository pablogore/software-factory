package domain

import "fmt"

// Finding is a review observation in the common format every role shares
// (file:line, severity, category, role), so round memory and the dashboard
// work for any role (04-roles.md).
type Finding struct {
	File     string `json:"file"`
	Line     int    `json:"line,omitempty"`
	Severity string `json:"severity"`
	Category string `json:"category,omitempty"`
	Role     string `json:"role,omitempty"`
	Message  string `json:"message,omitempty"`
}

// Validate returns an ErrMissingField error naming the first required field
// that is empty.
func (f Finding) Validate() error {
	switch {
	case f.File == "":
		return fmt.Errorf("%w: file", ErrMissingField)
	case f.Severity == "":
		return fmt.Errorf("%w: severity", ErrMissingField)
	}
	return nil
}
