package domain

import (
	"fmt"
	"strings"
	"time"
)

// ChecksRun is one run of the repo's checks in a job's worktree: the command
// as it was run, its exit code, how long it took and its combined output.
type ChecksRun struct {
	Command  string        `json:"command"`
	ExitCode int           `json:"exit_code"`
	Duration time.Duration `json:"duration"`
	Output   string        `json:"output"`
}

// Passed reports whether the checks exited with 0.
func (r ChecksRun) Passed() bool { return r.ExitCode == 0 }

// Validate returns an ErrMissingField error when no command was run, and an
// ErrInvalidValue error for a negative duration.
func (r ChecksRun) Validate() error {
	switch {
	case strings.TrimSpace(r.Command) == "":
		return fmt.Errorf("%w: checks command", ErrMissingField)
	case r.Duration < 0:
		return fmt.Errorf("%w: checks duration %s", ErrInvalidValue, r.Duration)
	}
	return nil
}
