// Package scheduler caps how many agents run at once. It is handed work
// that runs an agent and starts it in a free slot, or keeps it in line until
// one frees; waiting work starts in the order it was handed over. A slot is
// one running agent, not one job.
//
// Choosing what to hand over next is not the scheduler's concern: it runs
// what it is given.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// ErrConcurrency means New was asked for fewer than one slot.
var ErrConcurrency = errors.New("concurrency must be at least 1")

// Work runs one agent. Its error is handed back to whoever submitted it;
// to the scheduler, success and failure both free the slot.
type Work func(ctx context.Context) error

type item struct {
	ctx  context.Context
	work Work
	done chan error
}

// Scheduler runs at most its concurrency of Work at once.
type Scheduler struct {
	slots int

	// mu guards running and line, so a freed slot and a new submission
	// never both take the same slot.
	mu      sync.Mutex
	running int
	line    []item
}

// New returns a scheduler with concurrency slots, all free.
func New(concurrency int) (*Scheduler, error) {
	if concurrency < 1 {
		return nil, fmt.Errorf("scheduler: %d: %w", concurrency, ErrConcurrency)
	}
	return &Scheduler{slots: concurrency}, nil
}

// Submit hands w over to run with ctx. It never blocks: w starts now if a
// slot is free, or waits in line behind earlier work. The returned channel
// receives w's error once it has run.
func (s *Scheduler) Submit(ctx context.Context, w Work) <-chan error {
	it := item{ctx: ctx, work: w, done: make(chan error, 1)}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running < s.slots {
		s.running++
		go s.run(it)
	} else {
		s.line = append(s.line, it)
	}
	return it.done
}

// Running is how many slots are taken.
func (s *Scheduler) Running() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// Waiting is how much work is in line for a slot.
func (s *Scheduler) Waiting() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.line)
}

// run runs it and every piece of work that inherits its slot after it.
func (s *Scheduler) run(it item) {
	for {
		err := it.work(it.ctx)
		next, ok := s.free()
		// The slot is handed on before it.done is sent, so whoever waits
		// for a result already sees the next work running.
		it.done <- err
		if !ok {
			return
		}
		it = next
	}
}

// free hands the finished slot to the oldest waiting work, or frees it when
// nothing waits.
func (s *Scheduler) free() (item, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.line) == 0 {
		s.running--
		return item{}, false
	}
	next := s.line[0]
	s.line[0] = item{}
	s.line = s.line[1:]
	return next, true
}
