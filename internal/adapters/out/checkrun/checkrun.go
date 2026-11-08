// Package checkrun runs a repo's checks as a local command, for the gate.
package checkrun

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/pablogore/software-factory/internal/domain"
)

// waitDelay is how long a run waits, once its command exits or is
// cancelled, for children still holding its output open.
const waitDelay = 10 * time.Second

// Runner runs commands with the environment of factoryd. A zero Runner
// keeps the whole output and has no time limit of its own, as the gate runs
// the checks; Timeout and MaxOutput bound a run a builder asks for.
type Runner struct {
	// Timeout, when positive, is how long a run may take: then the command
	// is killed with its whole process group and the run, failed, says so
	// in its notes.
	Timeout time.Duration
	// MaxOutput, when positive, is the most bytes of output a run keeps:
	// the rest is dropped as it comes and the run says so in its notes.
	MaxOutput int
}

// Run runs command in dir without a shell, as the leader of its own process
// group, and returns its exit code, how long it took and its stdout and
// stderr as one output, in the order they were written. A non-zero exit is
// a run, not an error, and so is one past Timeout; the error is for a
// command that could not start or a ctx that ended before it did. Either
// way, a command still running is killed with its process group.
func (r Runner) Run(ctx context.Context, dir string, command []string) (domain.ChecksRun, error) {
	if len(command) == 0 {
		return domain.ChecksRun{}, fmt.Errorf("checkrun: %w: command", domain.ErrMissingField)
	}
	runCtx := ctx
	if r.Timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, r.Timeout)
		defer cancel()
	}
	out := &capped{max: r.MaxOutput}
	cmd := exec.CommandContext(runCtx, command[0], command[1:]...)
	cmd.Dir, cmd.Stdout, cmd.Stderr, cmd.WaitDelay = dir, out, out, waitDelay
	ownGroup(cmd)
	start := time.Now()
	err := cmd.Run()
	run := domain.ChecksRun{Command: strings.Join(command, " "), Duration: time.Since(start), Output: out.buf.String()}
	if ctx.Err() != nil {
		return domain.ChecksRun{}, fmt.Errorf("checkrun: %q in %s: %w", run.Command, dir, ctx.Err())
	}
	if out.dropped > 0 {
		run.Notes = append(run.Notes, fmt.Sprintf("output truncated: kept the first %d bytes, dropped the other %d", r.MaxOutput, out.dropped))
	}
	if runCtx.Err() != nil {
		run.ExitCode = -1
		run.Notes = append(run.Notes, fmt.Sprintf("timed out: the checks ran over %s and were killed with their process group", r.Timeout))
		return run, nil
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

// capped keeps the first max bytes written to it, all of them when max is
// not positive, and counts the ones it drops.
type capped struct {
	mu      sync.Mutex
	max     int
	buf     bytes.Buffer
	dropped int
}

func (c *capped) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	keep := len(p)
	if c.max > 0 {
		keep = min(keep, max(c.max-c.buf.Len(), 0))
	}
	c.buf.Write(p[:keep])
	c.dropped += len(p) - keep
	return len(p), nil
}
