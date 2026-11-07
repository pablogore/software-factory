// Package checkrun runs a repo's checks as a local command, for the gate.
package checkrun

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/pablogore/software-factory/internal/domain"
)

// waitDelay is how long a run waits, once its command exits or is
// cancelled, for children still holding its output open.
const waitDelay = 10 * time.Second

// Runner runs commands with the environment of factoryd.
type Runner struct{}

// Run runs command in dir without a shell and returns its exit code, how
// long it took and its stdout and stderr as one output, in the order they
// were written. A non-zero exit is a run, not an error; the error is for a
// command that could not start or a ctx that ended before it did.
func (Runner) Run(ctx context.Context, dir string, command []string) (domain.ChecksRun, error) {
	if len(command) == 0 {
		return domain.ChecksRun{}, fmt.Errorf("checkrun: %w: command", domain.ErrMissingField)
	}
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir, cmd.Stdout, cmd.Stderr, cmd.WaitDelay = dir, &out, &out, waitDelay
	start := time.Now()
	err := cmd.Run()
	run := domain.ChecksRun{Command: strings.Join(command, " "), Duration: time.Since(start), Output: out.String()}
	if ctx.Err() != nil {
		return domain.ChecksRun{}, fmt.Errorf("checkrun: %q in %s: %w", run.Command, dir, ctx.Err())
	}
	if exit, ok := errors.AsType[*exec.ExitError](err); ok {
		run.ExitCode = exit.ExitCode()
		return run, nil
	}
	if err != nil {
		return domain.ChecksRun{}, fmt.Errorf("checkrun: %q in %s: %w", run.Command, dir, err)
	}
	return run, nil
}
