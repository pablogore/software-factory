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
// a job that spent its whole budget, its own when a person raised it or else
// the scheduler's, never runs its agent, and its slot goes at once to the
// next work in line. Neither does a job whose spend or budget is not a finite
// amount at or above 0.
//
// Work handed over as using GitHub launches only while GitHub's remaining
// API limit is at or above a threshold. Below it, the work gives its slot to
// the next in line and waits for the limit's reset off any slot, then takes
// a slot like newly handed work.
//
// Choosing what to hand over next is not the scheduler's concern: it runs
// what it is given.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/ports"
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

// ErrInvalidCost means the cost source told a spend or a budget that is not
// a finite number at or above 0, so the agent never ran: launching it could
// let it run with no limit at all.
var ErrInvalidCost = errors.New("invalid cost")

// ErrGitHub means New was given a negative GitHub threshold, or no limit
// source or clock to wait with.
var ErrGitHub = errors.New("github threshold must be at least 0, with a limit source and a clock")

// RateLimit is what is left of GitHub's API limit and when it resets.
type RateLimit struct {
	Remaining int
	Reset     time.Time
}

// LimitSource tells GitHub's API limit as it stands now.
type LimitSource interface {
	Limit(ctx context.Context) (RateLimit, error)
}

// Clock tells the time and wakes whoever waits for a while, so tests move
// it instead of waiting.
type Clock interface {
	ports.Clock
	After(d time.Duration) <-chan time.Time
}

// GitHub is how the scheduler holds work that uses GitHub: such work waits
// for the reset while Limits says fewer than Threshold calls remain.
type GitHub struct {
	Limits    LimitSource
	Threshold int
	Clock     Clock
}

// CostSource tells how much a job has spent so far, in USD, and its own
// budget.
type CostSource interface {
	Spent(ctx context.Context, job domain.JobID) (float64, error)
	// Budget is the job's own budget a person raised (domain.Job.Budget),
	// or 0 when it has none and the scheduler's holds.
	Budget(ctx context.Context, job domain.JobID) (float64, error)
}

// Work runs one agent. Its error is handed back to whoever submitted it;
// to the scheduler, success and failure both free the slot.
type Work func(ctx context.Context) error

type item struct {
	ctx    context.Context
	job    domain.JobID
	work   Work
	github bool
	done   chan error
}

// Scheduler runs at most its concurrency of Work at once, plus one reply
// in the reserved slot.
type Scheduler struct {
	slots  int
	budget float64
	costs  CostSource
	github GitHub

	// mu guards running, reserved and line, so a freed slot and a new
	// submission never both take the same slot.
	mu       sync.Mutex
	running  int
	reserved bool
	line     []item
}

// New returns a scheduler with concurrency slots, all free, that launches
// a job's work only while costs says the job spent less than budget USD,
// and holds work that uses GitHub as github says.
func New(concurrency int, budget float64, costs CostSource, github GitHub) (*Scheduler, error) {
	if concurrency < 1 {
		return nil, fmt.Errorf("scheduler: %d: %w", concurrency, ErrConcurrency)
	}
	if !(budget > 0) || costs == nil {
		return nil, fmt.Errorf("scheduler: USD %v: %w", budget, ErrBudget)
	}
	if github.Threshold < 0 || github.Limits == nil || github.Clock == nil {
		return nil, fmt.Errorf("scheduler: github threshold %d: %w", github.Threshold, ErrGitHub)
	}
	return &Scheduler{slots: concurrency, budget: budget, costs: costs, github: github}, nil
}

// Submit hands w, work for job, over to run with ctx. It never blocks: w
// starts now if a slot is free, or waits in line behind earlier work. The
// returned channel receives w's error once it has run, or ErrBudgetExhausted
// or ErrInvalidCost when it never ran.
func (s *Scheduler) Submit(ctx context.Context, job domain.JobID, w Work) <-chan error {
	it := item{ctx: ctx, job: job, work: w, done: make(chan error, 1)}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.take(it)
	return it.done
}

// SubmitGitHub hands over w, work for job that uses GitHub. It is handled
// like work handed to Submit, except that while GitHub's remaining limit is
// below the threshold w waits for the reset without holding a slot. If ctx
// ends while w waits, the returned channel receives ctx's error and w never
// runs.
func (s *Scheduler) SubmitGitHub(ctx context.Context, job domain.JobID, w Work) <-chan error {
	it := item{ctx: ctx, job: job, work: w, github: true, done: make(chan error, 1)}
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
		wait, err := s.launch(it)
		next, ok := s.free()
		// The slot is handed on before it.done is sent, so whoever waits
		// for a result already sees the next work running.
		if wait > 0 {
			go s.park(it, wait)
		} else {
			it.done <- err
		}
		if !ok {
			return
		}
		it = next
	}
}

// runReserved runs it in the reserved slot. The slot is never handed to
// work in line, so it is free again for the next reply.
func (s *Scheduler) runReserved(it item) {
	// A reply never uses GitHub, so it never waits.
	_, err := s.launch(it)
	s.mu.Lock()
	s.reserved = false
	s.mu.Unlock()
	it.done <- err
}

// launch runs it's work unless its job already spent its whole budget, or
// it must first wait that long for GitHub's reset. The spend and the limit
// are asked for now, not when the work was handed over, since either may
// have changed while it waited in line.
func (s *Scheduler) launch(it item) (time.Duration, error) {
	spent, err := s.costs.Spent(it.ctx, it.job)
	if err != nil {
		return 0, fmt.Errorf("scheduler: spend of job %q: %w", it.job, err)
	}
	if !valid(spent) {
		return 0, fmt.Errorf("scheduler: spend of job %q is USD %v: %w", it.job, spent, ErrInvalidCost)
	}
	budget, err := s.costs.Budget(it.ctx, it.job)
	if err != nil {
		return 0, fmt.Errorf("scheduler: budget of job %q: %w", it.job, err)
	}
	if !valid(budget) {
		return 0, fmt.Errorf("scheduler: budget of job %q is USD %v: %w", it.job, budget, ErrInvalidCost)
	}
	if budget == 0 {
		budget = s.budget
	}
	if spent >= budget {
		return 0, fmt.Errorf("scheduler: job %q spent USD %.2f of %.2f: %w", it.job, spent, budget, ErrBudgetExhausted)
	}
	wait, err := s.hold(it)
	if err != nil || wait > 0 {
		return wait, err
	}
	return 0, it.work(it.ctx)
}

// valid tells whether usd is a finite amount at or above 0. NaN fails every
// comparison, so a plain spent >= budget would launch it.
func valid(usd float64) bool {
	return usd >= 0 && !math.IsInf(usd, 1)
}

// hold tells how long it must wait for GitHub's reset before it may
// launch: 0 when it does not use GitHub, enough calls remain, or the reset
// already passed.
func (s *Scheduler) hold(it item) (time.Duration, error) {
	if !it.github {
		return 0, nil
	}
	limit, err := s.github.Limits.Limit(it.ctx)
	if err != nil {
		return 0, fmt.Errorf("scheduler: github limit for job %q: %w", it.job, err)
	}
	if limit.Remaining >= s.github.Threshold {
		return 0, nil
	}
	return max(limit.Reset.Sub(s.github.Clock.Now()), 0), nil
}

// park waits off any slot for wait to pass, then hands it over again. The
// limit is asked for again when it next launches, in case it is still low.
func (s *Scheduler) park(it item, wait time.Duration) {
	select {
	case <-s.github.Clock.After(wait):
	case <-it.ctx.Done():
		it.done <- it.ctx.Err()
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.take(it)
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
