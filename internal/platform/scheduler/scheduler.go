// Package scheduler caps how many agents run at once. It is handed work
// that runs an agent and starts it in a free slot, or keeps it in line until
// one frees; waiting work starts in the order it was handed over. A slot is
// one running agent, not one job.
//
// One more slot, on top of the concurrency, is reserved for a person's
// reply, so new work filling every slot never makes someone wait for an
// answer. New work never takes it.
//
// Before launching any work the scheduler asks how much its job has spent:
// a job that spent its whole budget never runs its agent, and its slot goes
// at once to the next work in line.
//
// Choosing what to hand over next is not the scheduler's concern: it runs
// what it is given.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/pablogore/software-factory/internal/domain"
)

// ErrConcurrency means New was asked for fewer than one slot.
var ErrConcurrency = errors.New("concurrency must be at least 1")

// ErrBudget means New was given a budget that is not above 0, or no cost
// source to check it against.
var ErrBudget = errors.New("budget must be above 0, with a cost source")

// ErrBudgetExhausted means the job had spent its whole budget when its work
// was about to launch, so the agent never ran. Whoever submitted the work
// records domain.BudgetExhausted for the job, which stops it in needs-human.
var ErrBudgetExhausted = errors.New("budget exhausted")

// CostSource tells how much a job has spent so far, in USD.
type CostSource interface {
	Spent(ctx context.Context, job domain.JobID) (float64, error)
}

// Work runs one agent. Its error is handed back to whoever submitted it;
// to the scheduler, success and failure both free the slot.
type Work func(ctx context.Context) error

type item struct {
	ctx  context.Context
	job  domain.JobID
	work Work
	done chan error
}

// Scheduler runs at most its concurrency of Work at once, plus one reply
// in the reserved slot.
type Scheduler struct {
	slots  int
	budget float64
	costs  CostSource

	// mu guards running, reserved and line, so a freed slot and a new
	// submission never both take the same slot.
	mu       sync.Mutex
	running  int
	reserved bool
	line     []item
}

// New returns a scheduler with concurrency slots, all free, that launches
// a job's work only while costs says the job spent less than budget USD.
func New(concurrency int, budget float64, costs CostSource) (*Scheduler, error) {
	if concurrency < 1 {
		return nil, fmt.Errorf("scheduler: %d: %w", concurrency, ErrConcurrency)
	}
	if !(budget > 0) || costs == nil {
		return nil, fmt.Errorf("scheduler: USD %v: %w", budget, ErrBudget)
	}
	return &Scheduler{slots: concurrency, budget: budget, costs: costs}, nil
}

// Submit hands w, work for job, over to run with ctx. It never blocks: w
// starts now if a slot is free, or waits in line behind earlier work. The
// returned channel receives w's error once it has run, or ErrBudgetExhausted
// when it never ran.
func (s *Scheduler) Submit(ctx context.Context, job domain.JobID, w Work) <-chan error {
	it := item{ctx: ctx, job: job, work: w, done: make(chan error, 1)}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.take(it)
	return it.done
}

// SubmitReply hands over w, the work for job that answers a person's reply.
// It never blocks: w starts now in the reserved slot if it is free;
// otherwise it is handled like any work handed to Submit.
func (s *Scheduler) SubmitReply(ctx context.Context, job domain.JobID, w Work) <-chan error {
	it := item{ctx: ctx, job: job, work: w, done: make(chan error, 1)}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.reserved {
		s.reserved = true
		go s.runReserved(it)
	} else {
		s.take(it)
	}
	return it.done
}

// take starts it in a free regular slot, or puts it in line. s.mu must be
// held.
func (s *Scheduler) take(it item) {
	if s.running < s.slots {
		s.running++
		go s.run(it)
	} else {
		s.line = append(s.line, it)
	}
}

// Running is how many slots are taken, the reserved one included.
func (s *Scheduler) Running() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reserved {
		return s.running + 1
	}
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
		err := s.launch(it)
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

// runReserved runs it in the reserved slot. The slot is never handed to
// work in line, so it is free again for the next reply.
func (s *Scheduler) runReserved(it item) {
	err := s.launch(it)
	s.mu.Lock()
	s.reserved = false
	s.mu.Unlock()
	it.done <- err
}

// launch runs it's work unless its job already spent its whole budget. The
// spend is asked for now, not when the work was handed over, since the job
// may have spent more while it waited in line.
func (s *Scheduler) launch(it item) error {
	spent, err := s.costs.Spent(it.ctx, it.job)
	if err != nil {
		return fmt.Errorf("scheduler: spend of job %q: %w", it.job, err)
	}
	if spent >= s.budget {
		return fmt.Errorf("scheduler: job %q spent USD %.2f of %.2f: %w", it.job, spent, s.budget, ErrBudgetExhausted)
	}
	return it.work(it.ctx)
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
