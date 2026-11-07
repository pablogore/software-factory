package scheduler_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/platform/scheduler"
)

// patience bounds how long a test waits for an agent to start or a result
// to arrive, so a broken scheduler fails instead of hanging.
const patience = 2 * time.Second

var errAgent = errors.New("agent failed")

// crew is a set of fake agents. Each one, once started, waits until the
// test finishes it; crew keeps the order they started in, how many run now
// and the most that ever ran at once.
type crew struct {
	mu      sync.Mutex
	order   []string
	running int
	peak    int
	agents  map[string]*agent
}

type agent struct {
	started chan struct{}
	finish  chan error
}

func newCrew() *crew { return &crew{agents: map[string]*agent{}} }

// work returns the Work of a new agent called name.
func (c *crew) work(name string) scheduler.Work {
	a := &agent{started: make(chan struct{}), finish: make(chan error)}
	c.mu.Lock()
	c.agents[name] = a
	c.mu.Unlock()
	return func(context.Context) error {
		c.mu.Lock()
		c.order = append(c.order, name)
		c.running++
		c.peak = max(c.peak, c.running)
		c.mu.Unlock()
		close(a.started)
		err := <-a.finish
		c.mu.Lock()
		c.running--
		c.mu.Unlock()
		return err
	}
}

func (c *crew) agent(name string) *agent {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.agents[name]
}

// started reports whether name has started, without waiting.
func (c *crew) started(name string) bool {
	select {
	case <-c.agent(name).started:
		return true
	default:
		return false
	}
}

// awaitStart waits until name has started.
func (c *crew) awaitStart(ctx *specs.Context, name string) {
	select {
	case <-c.agent(name).started:
	case <-time.After(patience):
		ctx.Errorf("agent %q did not start", name)
	}
}

// end finishes name's run with err.
func (c *crew) end(name string, err error) { c.agent(name).finish <- err }

func (c *crew) snapshot() (order []string, peak int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.order...), c.peak
}

// result waits for a submitted work's error.
func result(ctx *specs.Context, done <-chan error) error {
	select {
	case err := <-done:
		return err
	case <-time.After(patience):
		ctx.Errorf("work did not finish")
		return nil
	}
}

// submit hands over one new agent per name to s, in order.
func submit(s *scheduler.Scheduler, c *crew, names ...string) map[string]<-chan error {
	done := map[string]<-chan error{}
	for _, n := range names {
		done[n] = s.Submit(context.Background(), c.work(n))
	}
	return done
}

func newScheduler(ctx *specs.Context, concurrency int) *scheduler.Scheduler {
	s, err := scheduler.New(concurrency)
	ctx.Expect(err).To(specs.BeNil())
	return s
}

func TestScheduler(t *testing.T) {
	specs.Describe(t, "scheduler.Scheduler", func(s *specs.Spec) {
		s.When("concurrency is 2 and three agents are handed over", func(s *specs.Spec) {
			s.It("runs two and keeps the third waiting", func(ctx *specs.Context) {
				sch, c := newScheduler(ctx, 2), newCrew()
				submit(sch, c, "a", "b", "c")
				c.awaitStart(ctx, "a")
				c.awaitStart(ctx, "b")
				ctx.Expect(c.started("c")).To(specs.BeFalse())
				ctx.Expect(sch.Running()).ToEqual(2)
				ctx.Expect(sch.Waiting()).ToEqual(1)
				c.end("a", nil)
				c.end("b", nil)
				c.awaitStart(ctx, "c")
				c.end("c", nil)
			})

			s.It("starts the third when one finishes successfully", func(ctx *specs.Context) {
				sch, c := newScheduler(ctx, 2), newCrew()
				done := submit(sch, c, "a", "b", "c")
				c.awaitStart(ctx, "a")
				c.awaitStart(ctx, "b")
				c.end("a", nil)
				ctx.Expect(result(ctx, done["a"])).To(specs.BeNil())
				c.awaitStart(ctx, "c")
				ctx.Expect(sch.Running()).ToEqual(2)
				ctx.Expect(sch.Waiting()).ToEqual(0)
				c.end("b", nil)
				c.end("c", nil)
			})

			s.It("starts the third when one finishes with an error, and hands the error back", func(ctx *specs.Context) {
				sch, c := newScheduler(ctx, 2), newCrew()
				done := submit(sch, c, "a", "b", "c")
				c.awaitStart(ctx, "a")
				c.awaitStart(ctx, "b")
				c.end("b", errAgent)
				ctx.Expect(result(ctx, done["b"])).To(specs.MatchError(errAgent))
				c.awaitStart(ctx, "c")
				ctx.Expect(sch.Running()).ToEqual(2)
				c.end("a", nil)
				c.end("c", nil)
			})

			s.It("frees the slots once everything has finished", func(ctx *specs.Context) {
				sch, c := newScheduler(ctx, 2), newCrew()
				done := submit(sch, c, "a", "b", "c")
				for _, n := range []string{"a", "b", "c"} {
					c.awaitStart(ctx, n)
					c.end(n, nil)
					ctx.Expect(result(ctx, done[n])).To(specs.BeNil())
				}
				ctx.Expect(sch.Running()).ToEqual(0)
				ctx.Expect(sch.Waiting()).ToEqual(0)
			})
		})

		s.When("work waits in line", func(s *specs.Spec) {
			s.It("starts it in the order it was handed over", func(ctx *specs.Context) {
				sch, c := newScheduler(ctx, 1), newCrew()
				names := []string{"a", "b", "c", "d", "e"}
				submit(sch, c, names...)
				for _, n := range names {
					c.awaitStart(ctx, n)
					c.end(n, nil)
				}
				order, _ := c.snapshot()
				ctx.Expect(order).ToEqual(names)
			})
		})

		s.When("many agents are handed over with concurrency 2", func(s *specs.Spec) {
			s.It("never runs more than two at once", func(ctx *specs.Context) {
				sch, c := newScheduler(ctx, 2), newCrew()
				var names []string
				for i := range 10 {
					names = append(names, fmt.Sprintf("agent-%d", i))
				}
				done := submit(sch, c, names...)
				// Each agent is finished only once the next one has started,
				// so a freed slot is always taken while another agent runs.
				for i, n := range names {
					c.awaitStart(ctx, n)
					ctx.Expect(sch.Running()).To(specs.BeLessThanOrEqual(2))
					if i > 0 {
						c.end(names[i-1], nil)
						ctx.Expect(result(ctx, done[names[i-1]])).To(specs.BeNil())
					}
				}
				last := names[len(names)-1]
				c.end(last, nil)
				ctx.Expect(result(ctx, done[last])).To(specs.BeNil())
				order, peak := c.snapshot()
				ctx.Expect(order).To(specs.HaveLen(len(names)))
				ctx.Expect(peak).ToEqual(2)
			})
		})

		s.When("concurrency is below 1", func(s *specs.Spec) {
			s.It("refuses to build the scheduler", func(ctx *specs.Context) {
				_, err := scheduler.New(0)
				ctx.Expect(err).To(specs.MatchError(scheduler.ErrConcurrency))
			})
		})
	})
}
