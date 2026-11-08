// Package gatelock is the one lock per job that a job's checks run under,
// whether a builder's run_gate or the gate itself runs them, so two runs
// never share a worktree. A run_gate run gives way: the job's result being
// submitted, or the gate starting, cancels it and waits until it ended.
package gatelock

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/pablogore/software-factory/internal/domain"
)

// ErrPreempted is the cause a run taken with Try is cancelled with when
// Preempt or Acquire gives its lock to the job's result and its gate.
var ErrPreempted = errors.New("checks cancelled: the job's result was submitted")

// Locks keeps which jobs' checks are running.
type Locks struct {
	mu   sync.Mutex
	held map[domain.JobID]*hold
}

// hold is a job's lock while it is held: done closes once it is released;
// cancel, for a run taken with Try, cancels it.
type hold struct {
	done   chan struct{}
	cancel context.CancelCauseFunc
}

// New returns locks none of which is held.
func New() *Locks {
	return &Locks{held: map[domain.JobID]*hold{}}
}

// Try takes job's lock for a run that gives way, and returns the context to
// run it with, a child of ctx, and the func that releases the lock. Preempt
// and Acquire cancel that context, with ErrPreempted as its cause, and wait
// for the release. A lock already held, by either kind of run, is refused
// with domain.ErrBusy, neither waiting nor queued.
func (l *Locks) Try(ctx context.Context, job domain.JobID) (context.Context, func(), error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.held[job] != nil {
		return nil, nil, fmt.Errorf("%w: the checks of job %q are running; call again once that run answers", domain.ErrBusy, job)
	}
	run, cancel := context.WithCancelCause(ctx)
	h := &hold{done: make(chan struct{}), cancel: cancel}
	l.held[job] = h
	return run, l.release(job, h), nil
}

// Preempt cancels job's run taken with Try, if one holds its lock, and
// returns once it released it, or with ctx's error when ctx ends first. A
// lock taken with Acquire, or none, is left as it is.
func (l *Locks) Preempt(ctx context.Context, job domain.JobID) error {
	l.mu.Lock()
	h := l.held[job]
	l.mu.Unlock()
	if h == nil || h.cancel == nil {
		return nil
	}
	h.cancel(ErrPreempted)
	select {
	case <-h.done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("gatelock: waiting for the checks of job %q to stop: %w", job, ctx.Err())
	}
}

// Acquire takes job's lock for the gate, which does not give way: it
// cancels a run taken with Try, waits for whoever holds the lock to release
// it, and returns the func that releases it, or ctx's error when ctx ends
// first.
func (l *Locks) Acquire(ctx context.Context, job domain.JobID) (func(), error) {
	for {
		l.mu.Lock()
		h := l.held[job]
		if h == nil {
			mine := &hold{done: make(chan struct{})}
			l.held[job] = mine
			l.mu.Unlock()
			return l.release(job, mine), nil
		}
		l.mu.Unlock()
		if h.cancel != nil {
			h.cancel(ErrPreempted)
		}
		select {
		case <-h.done:
		case <-ctx.Done():
			return nil, fmt.Errorf("gatelock: waiting for the checks of job %q to stop: %w", job, ctx.Err())
		}
	}
}

// release is the func that releases h, job's lock; calling it again does
// nothing.
func (l *Locks) release(job domain.JobID, h *hold) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			if l.held[job] == h {
				delete(l.held, job)
			}
			l.mu.Unlock()
			if h.cancel != nil {
				h.cancel(context.Canceled)
			}
			close(h.done)
		})
	}
}
