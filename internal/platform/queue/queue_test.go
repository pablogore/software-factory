package queue_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/queue"
	"github.com/pablogore/software-factory/internal/platform/scheduler"
)

// patience bounds how long a test waits for an agent to start or a result
// to arrive, so a broken queue fails instead of hanging.
const patience = 2 * time.Second

var errAgent = errors.New("agent failed")

// runner is a fake queue.Runner. The agent of each issue, once started,
// waits until the test finishes it; runner keeps the order they started in
// and the results the queue handed back.
type runner struct {
	mu      sync.Mutex
	order   []int
	agents  map[int]*agent
	results map[int]chan error
}

type agent struct {
	started chan struct{}
	finish  chan error
}

func newRunner() *runner {
	return &runner{agents: map[int]*agent{}, results: map[int]chan error{}}
}

func (r *runner) Work(is queue.Issue) (domain.JobID, scheduler.Work) {
	a := r.agent(is.Number)
	return domain.JobID(fmt.Sprint(is.Number)), func(context.Context) error {
		r.mu.Lock()
		r.order = append(r.order, is.Number)
		r.mu.Unlock()
		close(a.started)
		return <-a.finish
	}
}

func (r *runner) Done(is queue.Issue, err error) { r.result(is.Number) <- err }

func (r *runner) agent(n int) *agent {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.agents[n] == nil {
		r.agents[n] = &agent{started: make(chan struct{}), finish: make(chan error)}
	}
	return r.agents[n]
}

func (r *runner) result(n int) chan error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.results[n] == nil {
		r.results[n] = make(chan error, 1)
	}
	return r.results[n]
}

func (r *runner) started() []int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int(nil), r.order...)
}

// awaitStart waits until issue n's agent has started.
func (r *runner) awaitStart(ctx *specs.Context, n int) {
	select {
	case <-r.agent(n).started:
	case <-time.After(patience):
		ctx.Errorf("issue #%d did not start", n)
	}
}

// end finishes issue n's agent with err and waits for its result.
func (r *runner) end(ctx *specs.Context, n int, err error) error {
	r.agent(n).finish <- err
	return r.awaitResult(ctx, n)
}

func (r *runner) awaitResult(ctx *specs.Context, n int) error {
	select {
	case err := <-r.result(n):
		return err
	case <-time.After(patience):
		ctx.Errorf("issue #%d did not finish", n)
		return nil
	}
}

// costs is a fake cost source that fails when err is set.
type costs struct{ err error }

func (c costs) Spent(context.Context, domain.JobID) (float64, error) { return 0, c.err }

// plenty is a fake GitHub limit source with plenty of calls left, so the
// scheduler never holds work for the rate limit.
type plenty struct{}

func (plenty) Limit(context.Context) (scheduler.RateLimit, error) {
	return scheduler.RateLimit{Remaining: 5000, Reset: epoch.Add(time.Hour)}, nil
}

var epoch = time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)

// stopped is a fake clock whose time never moves: with plenty of calls
// left nobody waits on it.
type stopped struct{}

func (stopped) Now() time.Time                       { return epoch }
func (stopped) After(time.Duration) <-chan time.Time { return nil }

func newScheduler(ctx *specs.Context, concurrency int, c costs) *scheduler.Scheduler {
	s, err := scheduler.New(concurrency, 5, c, scheduler.GitHub{Limits: plenty{}, Threshold: 100, Clock: stopped{}})
	ctx.Expect(err).To(specs.BeNil())
	return s
}

// takeAll finishes, one after another, the agents of the first total
// issues to start on a one-slot scheduler, and returns the order they
// started in.
func takeAll(ctx *specs.Context, r *runner, total int) []int {
	for i := range total {
		deadline := time.Now().Add(patience)
		for len(r.started()) <= i {
			if time.Now().After(deadline) {
				ctx.Errorf("only %d of %d issues started", i, total)
				return r.started()
			}
			time.Sleep(time.Millisecond)
		}
		ctx.Expect(r.end(ctx, r.started()[i], nil)).To(specs.BeNil())
	}
	return r.started()
}

func TestQueue(t *testing.T) {
	specs.Describe(t, "queue.Queue", func(s *specs.Spec) {
		s.When("there is a tech lead order", func(s *specs.Spec) {
			s.It("hands issues to the scheduler in that order, the unlisted ones after by number", func(ctx *specs.Context) {
				r := newRunner()
				q := queue.New(newScheduler(ctx, 1, costs{}), queue.Order{TechLead: []int{9, 3}, Priority: []int{5, 1}}, noDeps{}, r)
				q.Add(context.Background(), issues(3, 5, 9, 1)...)
				ctx.Expect(takeAll(ctx, r, 4)).ToEqual([]int{9, 3, 1, 5})
			})
		})

		s.When("there is no tech lead order", func(s *specs.Spec) {
			s.It("hands issues over by the product owner's priority, the unlisted ones after by number", func(ctx *specs.Context) {
				r := newRunner()
				q := queue.New(newScheduler(ctx, 1, costs{}), queue.Order{Priority: []int{5, 1}}, noDeps{}, r)
				q.Add(context.Background(), issues(9, 1, 3, 5)...)
				ctx.Expect(takeAll(ctx, r, 4)).ToEqual([]int{5, 1, 3, 9})
			})
		})

		s.When("issues wait for a slot", func(s *specs.Spec) {
			s.It("lists them in the order they will be taken", func(ctx *specs.Context) {
				r := newRunner()
				q := queue.New(newScheduler(ctx, 1, costs{}), queue.Order{TechLead: []int{9, 3}}, noDeps{}, r)
				q.Add(context.Background(), issues(3, 5, 9, 1)...)
				r.awaitStart(ctx, 9)
				ctx.Expect(numbers(q.Waiting())).ToEqual([]int{3, 1, 5})
				for _, n := range []int{9, 3, 1} {
					want := numbers(q.Waiting())
					ctx.Expect(r.end(ctx, n, nil)).To(specs.BeNil())
					r.awaitStart(ctx, want[0])
					ctx.Expect(numbers(q.Waiting())).ToEqual(want[1:])
				}
				ctx.Expect(r.end(ctx, 5, nil)).To(specs.BeNil())
				ctx.Expect(r.started()).ToEqual([]int{9, 3, 1, 5})
			})

			s.It("puts an issue added later in its place among them", func(ctx *specs.Context) {
				r := newRunner()
				q := queue.New(newScheduler(ctx, 1, costs{}), queue.Order{TechLead: []int{9, 3, 7}}, noDeps{}, r)
				q.Add(context.Background(), issues(9, 3, 1)...)
				r.awaitStart(ctx, 9)
				q.Add(context.Background(), issues(7)...)
				ctx.Expect(numbers(q.Waiting())).ToEqual([]int{3, 7, 1})
				ctx.Expect(takeAll(ctx, r, 4)).ToEqual([]int{9, 3, 7, 1})
			})

			s.It("takes an issue added twice only once", func(ctx *specs.Context) {
				r := newRunner()
				q := queue.New(newScheduler(ctx, 1, costs{}), queue.Order{}, noDeps{}, r)
				q.Add(context.Background(), issues(1, 2)...)
				r.awaitStart(ctx, 1)
				q.Add(context.Background(), issues(1, 2)...)
				ctx.Expect(numbers(q.Waiting())).ToEqual([]int{2})
				ctx.Expect(takeAll(ctx, r, 2)).ToEqual([]int{1, 2})
			})
		})

		s.When("the scheduler has two slots", func(s *specs.Spec) {
			s.It("starts the first two in order and keeps the rest waiting", func(ctx *specs.Context) {
				r := newRunner()
				q := queue.New(newScheduler(ctx, 2, costs{}), queue.Order{Priority: []int{4, 2}}, noDeps{}, r)
				q.Add(context.Background(), issues(1, 2, 3, 4)...)
				r.awaitStart(ctx, 4)
				r.awaitStart(ctx, 2)
				ctx.Expect(numbers(q.Waiting())).ToEqual([]int{1, 3})
				ctx.Expect(r.end(ctx, 2, nil)).To(specs.BeNil())
				r.awaitStart(ctx, 1)
				ctx.Expect(numbers(q.Waiting())).ToEqual([]int{3})
				ctx.Expect(r.end(ctx, 4, nil)).To(specs.BeNil())
				r.awaitStart(ctx, 3)
				ctx.Expect(r.end(ctx, 1, nil)).To(specs.BeNil())
				ctx.Expect(r.end(ctx, 3, nil)).To(specs.BeNil())
				ctx.Expect(r.started()).ToEqual([]int{4, 2, 1, 3})
			})
		})

		s.When("other work already waits in the scheduler", func(s *specs.Spec) {
			s.It("keeps its issues until that work has a slot", func(ctx *specs.Context) {
				r := newRunner()
				sch := newScheduler(ctx, 1, costs{})
				release := make(chan struct{})
				busy := sch.Submit(context.Background(), "busy", func(context.Context) error { <-release; return nil })
				waits := sch.Submit(context.Background(), "waits", func(context.Context) error { return nil })
				q := queue.New(sch, queue.Order{}, noDeps{}, r)
				q.Add(context.Background(), issues(1)...)
				ctx.Expect(numbers(q.Waiting())).ToEqual([]int{1})
				ctx.Expect(r.started()).To(specs.BeEmpty())
				close(release)
				ctx.Expect(<-busy).To(specs.BeNil())
				ctx.Expect(<-waits).To(specs.BeNil())
				// The queue looks for room again when issues are added.
				q.Add(context.Background())
				r.awaitStart(ctx, 1)
				ctx.Expect(r.end(ctx, 1, nil)).To(specs.BeNil())
			})
		})

		s.When("an issue's work ends", func(s *specs.Spec) {
			s.It("hands the work's error to the runner", func(ctx *specs.Context) {
				r := newRunner()
				q := queue.New(newScheduler(ctx, 1, costs{}), queue.Order{}, noDeps{}, r)
				q.Add(context.Background(), issues(1)...)
				r.awaitStart(ctx, 1)
				ctx.Expect(r.end(ctx, 1, errAgent)).To(specs.MatchError(errAgent))
			})

			s.It("hands over why the scheduler never ran it, and takes the next", func(ctx *specs.Context) {
				errCosts := errors.New("costs unreadable")
				r := newRunner()
				q := queue.New(newScheduler(ctx, 1, costs{err: errCosts}), queue.Order{}, noDeps{}, r)
				q.Add(context.Background(), issues(1, 2)...)
				ctx.Expect(r.awaitResult(ctx, 1)).To(specs.MatchError(errCosts))
				ctx.Expect(r.awaitResult(ctx, 2)).To(specs.MatchError(errCosts))
				ctx.Expect(q.Waiting()).To(specs.BeEmpty())
				ctx.Expect(r.started()).To(specs.BeEmpty())
			})
		})
	})
}
