package domain

import "fmt"

// Role is an instance of a Kind, declared in configuration with its own prompt.
// Adding a role of an existing kind needs no code (04-roles.md).
type Role struct {
	Name        string `json:"name"`
	Kind        Kind   `json:"kind"`
	Description string `json:"description,omitempty"`
	Prompt      string `json:"prompt"`
	Agent       string `json:"agent,omitempty"`
	Model       string `json:"model,omitempty"`
	When        When   `json:"when,omitempty"`
	Mode        Mode   `json:"mode,omitempty"`
}

// When says when a role runs; a role whose conditions don't apply is skipped
// at no cost. It runs when any one of Any holds.
type When struct {
	Any []Condition `json:"any,omitempty"`
}

// Condition is one entry of When.Any.
type Condition struct {
	Paths       []string `json:"paths,omitempty"`
	Labels      []string `json:"labels,omitempty"`
	DepsChanged bool     `json:"deps_changed,omitempty"`
	DiffLines   string   `json:"diff_lines,omitempty"`
}

// Mode is how far a role's findings reach, so a new role can be brought in
// without risk: shadow → advisory → blocking (04-roles.md).
type Mode string

// The three modes.
const (
	ModeShadow   Mode = "shadow"
	ModeAdvisory Mode = "advisory"
	ModeBlocking Mode = "blocking"
)

// Modes lists every mode in promotion order.
var Modes = []Mode{ModeShadow, ModeAdvisory, ModeBlocking}

// Validate returns an error when m is not one of Modes.
func (m Mode) Validate() error {
	for _, known := range Modes {
		if m == known {
			return nil
		}
	}
	return fmt.Errorf("%w: mode %q", ErrInvalidValue, m)
}

// Validate checks the role's kind and mode; the error names the role.
func (r Role) Validate() error {
	if err := r.Kind.Validate(); err != nil {
		return fmt.Errorf("role %s: %w", r.Name, err)
	}
	if err := r.Mode.Validate(); err != nil {
		return fmt.Errorf("role %s: %w", r.Name, err)
	}
	return nil
}
