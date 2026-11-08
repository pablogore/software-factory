package domain

import (
	"fmt"
	"path"
)

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
	// AlwaysRerun makes a review role run in every round, even after it passed.
	AlwaysRerun bool `json:"always_rerun,omitempty"`
	// AllowedPaths are the only files a build role's diff may add, modify or
	// delete, from the repo root: an entry ending in "/" covers everything
	// under it, any other is a path.Match pattern. None means no limit.
	AllowedPaths []string `json:"allowed_paths,omitempty"`
	// Tools are the only worker tools the role's sessions may call; none
	// means the ones every role has, submit_result and progress.
	Tools []string `json:"tools,omitempty"`
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

// MaxRoleNameLen is the longest a role name may be: a validation bound, not a
// retry count.
const MaxRoleNameLen = 64

// ValidateRoleName checks that name is 1 to MaxRoleNameLen ASCII lowercase
// letters, digits and hyphens, not starting or ending with a hyphen. Role
// names key the separation-of-duties check and are compared exactly, so a
// look-alike (other case, Unicode, whitespace) must not load. The name is
// quoted with non-ASCII escaped so an invisible fault shows in the message.
func ValidateRoleName(name string) error {
	if name == "" {
		return fmt.Errorf("%w: role name is empty", ErrInvalidValue)
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return fmt.Errorf("%w: role name %+q, want only lowercase letters, digits and hyphens", ErrInvalidValue, name)
		}
	}
	if name[0] == '-' || name[len(name)-1] == '-' {
		return fmt.Errorf("%w: role name %+q starts or ends with a hyphen", ErrInvalidValue, name)
	}
	if len(name) > MaxRoleNameLen {
		return fmt.Errorf("%w: role name %+q is %d characters, want at most %d", ErrInvalidValue, name, len(name), MaxRoleNameLen)
	}
	return nil
}

// Validate checks the role's name, kind and mode, and that only a build role
// declares allowed paths, each a well-formed pattern; the error names the
// role.
func (r Role) Validate() error {
	if err := ValidateRoleName(r.Name); err != nil {
		return err
	}
	if err := r.Kind.Validate(); err != nil {
		return fmt.Errorf("role %s: %w", r.Name, err)
	}
	if err := r.Mode.Validate(); err != nil {
		return fmt.Errorf("role %s: %w", r.Name, err)
	}
	// Only the gate after build reads them; on another kind they would look
	// like a limit that nothing enforces.
	if len(r.AllowedPaths) > 0 && r.Kind != KindBuild {
		return fmt.Errorf("role %s: %w: allowed_paths on a %s role, want only on a build role", r.Name, ErrInvalidValue, r.Kind)
	}
	for _, p := range r.AllowedPaths {
		if _, err := path.Match(p, ""); p == "" || err != nil {
			return fmt.Errorf("role %s: %w: allowed path %q", r.Name, ErrInvalidValue, p)
		}
	}
	return nil
}
