// Package checkrun runs a repo's checks as a local command, for the gate.
package checkrun

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/childenv"
)

// waitDelay is how long a run waits, once its process group is gone, for
// a process that left the group and still holds its output open.
const waitDelay = 10 * time.Second

// grace is how long checks that are cancelled or past their timeout have,
// after SIGTERM, before their group is killed.
const grace = 2 * time.Second

// goneWait is how long a run waits for its killed group to be reaped, and
// poll how often it looks.
const (
	goneWait = 5 * time.Second
	poll     = 10 * time.Millisecond
)

// Runner runs commands with childenv.Base, HOME the job's own in Homes, and
// GOTELEMETRY=off as their whole environment, so no credential of
// factoryd's, nor anything in the operator's home, reaches the checks of a
// job's branch. A Runner without a time limit or MaxOutput keeps the whole
// output and runs as long as it takes, as the gate runs the checks; Timeout
// and MaxOutput bound a run a builder asks for.
type Runner struct {
	// Homes holds each job's HOME, outside its worktree.
	Homes childenv.Homes
	// Timeout, when positive, is how long a run may take: then the command
	// is stopped with its whole process group and the run, failed, says so
	// in its notes.
	Timeout time.Duration
	// MaxOutput, when positive, is the most bytes of output a run keeps:
	// the rest is dropped as it comes and the run says so in its notes.
	MaxOutput int
}

// Run runs command in dir, for job, without a shell, as the leader of its own process
// group, and returns its exit code, how long it took and its stdout and
// stderr as one output, in the order they were written. A non-zero exit is
// a run, not an error, and so is one past Timeout; the error is for a
// command that could not start or a ctx that ended before it did. A command
// still running when ctx ends or Timeout passes gets SIGTERM with its group,
// and SIGKILL after grace. However the command ends, what is left of its
// group, such as a child it left in the background, is killed, and Run
// returns only once no process of the group is left.
func (r Runner) Run(ctx context.Context, job domain.JobID, dir string, command []string) (domain.ChecksRun, error) {
	if len(command) == 0 {
		return domain.ChecksRun{}, fmt.Errorf("checkrun: %w: command", domain.ErrMissingField)
	}
	name := strings.Join(command, " ")
	if err := ctx.Err(); err != nil {
		return domain.ChecksRun{}, fmt.Errorf("checkrun: %q in %s: %w", name, dir, err)
	}
	runCtx := ctx
	if r.Timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, r.Timeout)
		defer cancel()
	}
	home, err := r.Homes.For(job)
	if err != nil {
		return domain.ChecksRun{}, fmt.Errorf("checkrun: %q in %s: %w", name, dir, err)
	}
	// The command writes to a pipe of the run's own, not one exec copies
	// from, so Wait returns once the command exits even while a child in
	// the background holds the pipe.
	pr, pw, err := os.Pipe()
	if err != nil {
		return domain.ChecksRun{}, fmt.Errorf("checkrun: %q in %s: %w", name, dir, err)
	}
	defer func() { _ = pr.Close() }()
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Dir, cmd.Stdout, cmd.Stderr = dir, pw, pw
	cmd.Env = append(childenv.Base(home), "GOTELEMETRY=off")
	ownGroup(cmd)
	start := time.Now()
	err = cmd.Start()
	_ = pw.Close()
	if err != nil {
		return domain.ChecksRun{}, fmt.Errorf("checkrun: %q in %s: %w", name, dir, err)
	}
	out := &capped{max: r.MaxOutput}
	copied := make(chan struct{})
	go func() {
		_, _ = io.Copy(out, pr)
		close(copied)
	}()
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	select {
	case err = <-exited:
	case <-runCtx.Done():
		err = stop(cmd, exited)
	}
	duration := time.Since(start)
	if killErr := killGroup(cmd); killErr != nil {
		return domain.ChecksRun{}, fmt.Errorf("checkrun: %q in %s: %w", name, dir, killErr)
	}
	select {
	case <-copied:
	case <-time.After(waitDelay):
		_ = pr.Close()
		<-copied
	}
	run := domain.ChecksRun{Command: name, Duration: duration, Output: out.String()}
	if ctx.Err() != nil {
		return domain.ChecksRun{}, fmt.Errorf("checkrun: %q in %s: %w", name, dir, ctx.Err())
	}
	if out.dropped > 0 {
		run.Notes = append(run.Notes, fmt.Sprintf("output truncated: kept the first %d bytes, dropped the other %d", r.MaxOutput, out.dropped))
	}
	if runCtx.Err() != nil {
		run.ExitCode = -1
		run.Notes = append(run.Notes, fmt.Sprintf("timed out: the checks ran over %s and were stopped with their process group", r.Timeout))
		return run, nil
	}
	if exit, ok := errors.AsType[*exec.ExitError](err); ok {
		run.ExitCode = exit.ExitCode()
		return run, nil
	}
	if err != nil {
		return domain.ChecksRun{}, fmt.Errorf("checkrun: %q in %s: %w", name, dir, err)
	}
	return run, nil
}

// stop sends SIGTERM to cmd's group and, if cmd has not exited after grace,
// SIGKILL, and returns what Wait returned.
func stop(cmd *exec.Cmd, exited <-chan error) error {
	terminate(cmd)
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case err := <-exited:
		return err
	case <-timer.C:
	}
	_ = killGroup(cmd)
	return <-exited
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

func (c *capped) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}
