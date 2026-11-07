package runmode

import (
	"context"
	"sync"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/queue"
	"github.com/pablogore/software-factory/internal/platform/scheduler"
)

// Brake sits between the queue and the scheduler, so it holds back only
// what the queue hands over: new issues. Work handed to the scheduler
// directly, as a slice of an initiative already running or a person's
// reply, never passes through it.
//
// While it is on, the queue sees one more work waiting in the scheduler's
// line and hands nothing over, and an issue it handed before that reaches a
// slot gives the slot back at once, unstarted, and waits until Release.
type Brake struct {
	sched queue.Scheduler

	// mu guards on and parked, so work reaching a slot as the brake comes
	// off is either parked and handed over again, or started.
	mu     sync.Mutex
	on     bool
	parked []parked
}

// parked is work the scheduler gave a slot while the brake was on.
type parked struct {
	ctx  context.Context
	job  domain.JobID
	work scheduler.Work
	done chan<- error
}

// NewBrake returns a brake, off, in front of sched.
func NewBrake(sched queue.Scheduler) *Brake {
	return &Brake{sched: sched}
}

// Submit hands w to the scheduler. The returned channel gets w's error once
// it ran, or why the scheduler never ran it, as the scheduler's would; work
// held back by the brake gets it only after Release.
func (b *Brake) Submit(ctx context.Context, job domain.JobID, w scheduler.Work) <-chan error {
	done := make(chan error, 1)
	b.submit(parked{ctx: ctx, job: job, work: w, done: done})
	return done
}

func (b *Brake) submit(p parked) {
	held := false
	inner := b.sched.Submit(p.ctx, p.job, func(ctx context.Context) error {
		b.mu.Lock()
		if b.on {
			b.parked = append(b.parked, p)
			held = true
			b.mu.Unlock()
			return nil
		}
		b.mu.Unlock()
		return p.work(ctx)
	})
	go func() {
		err := <-inner
		// held was set before the work returned, so before inner got its
		// result.
		if !held {
			p.done <- err
		}
	}()
}

// Waiting is the scheduler's waiting work, plus one while the brake is on,
// so the queue hands nothing over.
func (b *Brake) Waiting() int {
	b.mu.Lock()
	on := b.on
	b.mu.Unlock()
	if on {
		return b.sched.Waiting() + 1
	}
	return b.sched.Waiting()
}

// Engage puts the brake on. What already runs goes on; engaging it again is
// a no-op.
func (b *Brake) Engage() {
	b.mu.Lock()
	b.on = true
	b.mu.Unlock()
}

// Engaged tells whether the brake is on.
func (b *Brake) Engaged() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.on
}

// Release takes the brake off and hands the work it held back to the
// scheduler again, in the order it was held. The queue looks for room only
// when told to (see Mode.Release).
func (b *Brake) Release() {
	b.mu.Lock()
	b.on = false
	held := b.parked
	b.parked = nil
	b.mu.Unlock()
	for _, p := range held {
		b.submit(p)
	}
}
