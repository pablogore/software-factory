package domain

import (
	"fmt"
	"strings"
	"time"
)

// ChecksRun is one run of the repo's checks in a job's worktree: the command
// as it was run, its exit code, how long it took and its combined output.
// Violations are the gate's rules the job's diff broke, one reason each; the
// builder gets them with the log.
type ChecksRun struct {
	Command    string        `json:"command"`
	ExitCode   int           `json:"exit_code"`
	Duration   time.Duration `json:"duration"`
	Output     string        `json:"output"`
	Violations []string      `json:"violations,omitempty"`
}

// Passed reports whether the checks exited with 0 and the diff broke no rule.
func (r ChecksRun) Passed() bool { return r.ExitCode == 0 && len(r.Violations) == 0 }

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
