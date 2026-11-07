//go:build unix

// Package devserver implements ports.DevServers with Unix process groups:
// each dev server leads its own group, so stopping it signals every process
// it started, grandchildren included, even after the server itself exited.
//
// Stopping sends SIGTERM to the group and waits up to the grace period for
// the whole group to exit; whatever is left then gets SIGKILL.
package devserver

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/ports"
)

// DefaultGrace is how long a dev server gets to exit on its own before it is
// killed.
const DefaultGrace = 5 * time.Second

// poll is how often stopping checks whether the group is gone.
const poll = 20 * time.Millisecond

// ErrRunning means the stage already runs a dev server.
var ErrRunning = errors.New("devserver: stage already runs a dev server")

type key struct {
	job   domain.JobID
	stage string
}

type server struct {
	pgid int
	// done is closed once the group leader is reaped.
	done chan struct{}
}

// Servers runs dev servers. It is safe for concurrent use.
type Servers struct {
	grace time.Duration

	mu      sync.Mutex
	running map[key]*server
}

var _ ports.DevServers = (*Servers)(nil)

// New returns Servers that give a dev server grace to exit before killing it.
func New(grace time.Duration) *Servers {
	return &Servers{grace: grace, running: map[key]*server{}}
}

// Start implements ports.DevServers.
func (s *Servers) Start(_ context.Context, wt ports.Worktree, stage string, port int, command []string) error {
	switch {
	case wt.Job == "":
		return fmt.Errorf("job id: %w", domain.ErrMissingField)
	case wt.Path == "":
		return fmt.Errorf("worktree path: %w", domain.ErrMissingField)
	case stage == "":
		return fmt.Errorf("stage: %w", domain.ErrMissingField)
	case len(command) == 0 || command[0] == "":
		return fmt.Errorf("dev server command: %w", domain.ErrMissingField)
	case port < 1 || port > 65535:
		return fmt.Errorf("dev server port %d: %w", port, domain.ErrInvalidValue)
	}
	k := key{job: wt.Job, stage: stage}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.running[k]; ok {
		return fmt.Errorf("job %s stage %s: %w", wt.Job, stage, ErrRunning)
	}
	// Not CommandContext: the server lives until its stage or job stops, not
	// until the caller's context ends.
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Dir = wt.Path
	cmd.Env = append(cmd.Environ(), "PORT="+strconv.Itoa(port))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting dev server of job %s stage %s: %w", wt.Job, stage, err)
	}
	srv := &server{pgid: cmd.Process.Pid, done: make(chan struct{})}
	go func() {
		// Its exit status says nothing about whether the group is gone.
		_ = cmd.Wait()
		close(srv.done)
	}()
	s.running[k] = srv
	return nil
}

// StopStage implements ports.DevServers.
func (s *Servers) StopStage(ctx context.Context, job domain.JobID, stage string) error {
	k := key{job: job, stage: stage}
	s.mu.Lock()
	srv, ok := s.running[k]
	delete(s.running, k)
	s.mu.Unlock()
	if !ok {
		return nil
	}
	if err := s.stop(ctx, srv); err != nil {
		return fmt.Errorf("stopping dev server of job %s stage %s: %w", job, stage, err)
	}
	return nil
}

// StopJob implements ports.DevServers.
func (s *Servers) StopJob(ctx context.Context, job domain.JobID) error {
	s.mu.Lock()
	var stages []string
	for k := range s.running {
		if k.job == job {
			stages = append(stages, k.stage)
		}
	}
	s.mu.Unlock()
	var errs []error
	for _, stage := range stages {
		errs = append(errs, s.StopStage(ctx, job, stage))
	}
	return errors.Join(errs...)
}

// stop asks the group to exit, kills what is left after the grace period or
// once ctx ends, and returns when no process of the group is left.
func (s *Servers) stop(ctx context.Context, srv *server) error {
	if err := signal(srv.pgid, syscall.SIGTERM); err != nil {
		return err
	}
	timer := time.NewTimer(s.grace)
	defer timer.Stop()
	if gone, err := s.waitGone(ctx, srv, timer.C); gone || err != nil {
		return err
	}
	if err := signal(srv.pgid, syscall.SIGKILL); err != nil {
		return err
	}
	// SIGKILL can't be refused, so the group is gone as soon as the
	// processes are reaped.
	_, err := s.waitGone(context.Background(), srv, nil)
	return err
}

// waitGone reports whether the group left before deadline fires or ctx ends.
// The group is gone once its leader is reaped and no member is left: until
// then its id can't be reused, so signalling it never reaches a stranger.
func (s *Servers) waitGone(ctx context.Context, srv *server, deadline <-chan time.Time) (bool, error) {
	tick := time.NewTicker(poll)
	defer tick.Stop()
	for {
		select {
		case <-srv.done:
			if alive, err := groupAlive(srv.pgid); !alive || err != nil {
				return true, err
			}
		default:
		}
		select {
		case <-tick.C:
		case <-deadline:
			return false, nil
		case <-ctx.Done():
			return false, nil
		}
	}
}

// signal sends sig to the whole group; a group already gone is not an error.
func signal(pgid int, sig syscall.Signal) error {
	if err := syscall.Kill(-pgid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("signalling process group %d: %w", pgid, err)
	}
	return nil
}

func groupAlive(pgid int) (bool, error) {
	err := syscall.Kill(-pgid, 0)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, syscall.ESRCH):
		return false, nil
	case errors.Is(err, syscall.EPERM):
		// A member exists but can't be signalled: still alive.
		return true, nil
	default:
		return false, fmt.Errorf("checking process group %d: %w", pgid, err)
	}
}
