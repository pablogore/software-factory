// Package queue decides which open issue is taken next. It keeps the issues
// it is handed in their explicit order (see Order) and hands the next one to
// the scheduler once nothing else waits there for a slot, so the scheduler,
// which runs what it is given first come first served, starts them in that
// order.
//
// An issue whose "Depends on #N" lines, or the tech lead's AFTER lines
// (see Order.After), name an issue not merged or closed waits, with the
// reason, and the next ready one in the order goes ahead of it (see
// Depends).
//
// The queue looks for room again, and at the dependencies, when issues are
// added and when the work of one of its issues starts or ends; work others
// hand to the scheduler holds the queue back until then.
package queue

import (
	"context"
	"slices"
	"sync"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/scheduler"
)

// Scheduler is what the queue needs from internal/platform/scheduler.
type Scheduler interface {
	Submit(ctx context.Context, job domain.JobID, w scheduler.Work) <-chan error
	Waiting() int
}

// Runner turns an issue into its job's work and hears how that work ended.
type Runner interface {
	// Work returns the job for issue and the work that starts it.
	Work(issue Issue) (domain.JobID, scheduler.Work)
	// Done receives the scheduler's result for issue's work: the work's
	// error, or why it never ran.
	Done(issue Issue, err error)
}

// Queue holds open issues until the scheduler can take them.
type Queue struct {
	sched  Scheduler
	order  Order
	deps   Issues
	runner Runner

	// mu guards waiting, handed, held and seen, so two looks for room never hand
	// over the same issue.
	mu sync.Mutex
	// waiting is kept sorted by order.
	waiting []Issue
	// handed is the issue in the scheduler's line, not started yet. Only
	// one is handed over while nothing else waits there, so it is the next
	// one taken.
	handed *Issue
	// held is why each waiting issue its dependencies hold was passed over
	// at the last look for room.
	held map[int]string
	seen map[int]bool
}

// Hold is a waiting issue its dependencies hold, and why.
type Hold struct {
	Issue  Issue
	Reason string
}

// New returns an empty queue that takes issues in order, once deps reports
// their dependencies finished, through runner, onto sched.
func New(sched Scheduler, order Order, deps Issues, runner Runner) *Queue {
	return &Queue{sched: sched, order: order, deps: deps, runner: runner, held: map[int]string{}, seen: map[int]bool{}}
}

// Add puts issues in the queue, each in its place by the order, and hands
// the next ones to the scheduler while it has room. An issue already added
// once is left out, so the open issues can be handed over again on every
// poll.
func (q *Queue) Add(ctx context.Context, issues ...Issue) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, is := range issues {
		if !q.seen[is.Number] {
			q.seen[is.Number] = true
			q.waiting = append(q.waiting, is)
		}
	}
	Sort(q.waiting, q.order)
	q.feed(ctx)
}

// Waiting lists the issues not started yet, in the order they will be
// taken.
func (q *Queue) Waiting() []Issue {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.handed == nil {
		return slices.Clone(q.waiting)
	}
	return append([]Issue{*q.handed}, q.waiting...)
}

// Held lists, in order, the waiting issues their dependencies held at the
// last look for room, each with the dependencies it waits for.
func (q *Queue) Held() []Hold {
	q.mu.Lock()
	defer q.mu.Unlock()
	var out []Hold
	for _, is := range q.waiting {
		if why, ok := q.held[is.Number]; ok {
			out = append(out, Hold{Issue: is, Reason: why})
		}
	}
	return out
}

// feed hands the next issue its dependencies do not hold to the scheduler
// when nothing waits there for a slot. Once that issue starts, its work
// feeds the one after it, so issues fill every free slot and then one more
// waits for the next. q.mu must be held.
func (q *Queue) feed(ctx context.Context) {
	if q.handed != nil || q.sched.Waiting() != 0 {
		return
	}
	for i, is := range q.waiting {
		if why := hold(ctx, q.deps, is, q.order.After[is.Number]); why != "" {
			q.held[is.Number] = why
			continue
		}
		delete(q.held, is.Number)
		q.waiting = slices.Delete(q.waiting, i, i+1)
		q.handed = &is
		job, w := q.runner.Work(is)
		done := q.sched.Submit(ctx, job, q.wrap(is, w))
		go q.await(ctx, is, done)
		return
	}
}

// wrap returns w with is marked started first: its slot is taken, so the
// next issue can go into the scheduler's line.
func (q *Queue) wrap(is Issue, w scheduler.Work) scheduler.Work {
	return func(ctx context.Context) error {
		q.mu.Lock()
		q.unhand(is)
		q.feed(ctx)
		q.mu.Unlock()
		return w(ctx)
	}
}

// await hands is's result to the runner once its work ends or is refused,
// then looks for room again: the slot is free, or went to other work.
func (q *Queue) await(ctx context.Context, is Issue, done <-chan error) {
	err := <-done
	q.mu.Lock()
	// Work the scheduler refused never started, so it is still handed.
	q.unhand(is)
	q.mu.Unlock()
	q.runner.Done(is, err)
	q.mu.Lock()
	q.feed(ctx)
	q.mu.Unlock()
}

// unhand clears handed if it is is. q.mu must be held.
func (q *Queue) unhand(is Issue) {
	if q.handed != nil && q.handed.Number == is.Number {
		q.handed = nil
	}
}
