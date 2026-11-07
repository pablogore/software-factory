package scheduler_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/scheduler"
)

var errLimits = errors.New("limit source down")

// threshold is the GitHub threshold every test scheduler is handed.
const threshold = 100

var epoch = time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)

// clock is a fake clock: time moves only when the test advances it, and
// asked tells the test each time someone starts waiting on it.
type clock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []waiter
	asked   chan time.Duration
}

type waiter struct {
	at time.Time
	c  chan time.Time
}

func newClock() *clock { return &clock{now: epoch, asked: make(chan time.Duration, 16)} }

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) After(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	c.mu.Lock()
	c.waiters = append(c.waiters, waiter{at: c.now.Add(d), c: ch})
	c.mu.Unlock()
	c.asked <- d
	return ch
}

// advance moves the time by d and wakes whoever waited until then.
func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	left := c.waiters[:0]
	for _, w := range c.waiters {
		if w.at.After(c.now) {
			left = append(left, w)
		} else {
			w.c <- c.now
		}
	}
	c.waiters = left
}

// awaitAsked waits until someone starts waiting on c, and tells for how long.
func (c *clock) awaitAsked(ctx *specs.Context) time.Duration {
	select {
	case d := <-c.asked:
		return d
	case <-time.After(patience):
		ctx.Errorf("nobody waited on the clock")
		return 0
	}
}

// limits is a fake GitHub limit source; it counts how often it is asked.
type limits struct {
	mu    sync.Mutex
	limit scheduler.RateLimit
	err   error
	calls int
}

func newLimits(remaining int, reset time.Time) *limits {
	return &limits{limit: scheduler.RateLimit{Remaining: remaining, Reset: reset}}
}

func (l *limits) Limit(context.Context) (scheduler.RateLimit, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls++
	return l.limit, l.err
}

func (l *limits) set(remaining int, reset time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.limit = scheduler.RateLimit{Remaining: remaining, Reset: reset}
}

func (l *limits) asked() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.calls
}

// plentyGitHub never holds GitHub work: plenty of calls remain.
func plentyGitHub() scheduler.GitHub {
	return scheduler.GitHub{Limits: newLimits(5000, epoch.Add(time.Hour)), Threshold: threshold, Clock: newClock()}
}

func newGitHub(ctx *specs.Context, concurrency int, l *limits, c *clock) *scheduler.Scheduler {
	s, err := scheduler.New(concurrency, budget, newCosts(), scheduler.GitHub{Limits: l, Threshold: threshold, Clock: c})
	ctx.Expect(err).To(specs.BeNil())
	return s
}

// github hands over one new agent called name, using GitHub, for the job of
// the same name.
func github(s *scheduler.Scheduler, c *crew, name string) <-chan error {
	return s.SubmitGitHub(context.Background(), domain.JobID(name), c.work(name))
}

// refusal is a GitHub setting New must refuse.
type refusal struct {
	name string
	gh   scheduler.GitHub
}

func TestSchedulerGitHub(t *testing.T) {
	specs.Describe(t, "scheduler.Scheduler with GitHub's limit", func(s *specs.Spec) {
		s.When("fewer calls remain than the threshold", func(s *specs.Spec) {
			s.It("starts a GitHub stage only at the reset, while a non-GitHub stage runs at once", func(ctx *specs.Context) {
				l, clk, c := newLimits(threshold-1, epoch.Add(time.Hour)), newClock(), newCrew()
				sch := newGitHub(ctx, 2, l, clk)

				gh := github(sch, c, "gh")
				plain := submit(sch, c, "plain")

				c.awaitStart(ctx, "plain")
				ctx.Expect(clk.awaitAsked(ctx)).ToEqual(time.Hour)
				ctx.Expect(c.started("gh")).To(specs.BeFalse())
				clk.advance(59 * time.Minute)
				ctx.Expect(c.started("gh")).To(specs.BeFalse())

				l.set(5000, epoch.Add(2*time.Hour))
				clk.advance(time.Minute)

				c.awaitStart(ctx, "gh")
				ctx.Expect(sch.Running()).ToEqual(2)
				c.end("gh", nil)
				ctx.Expect(result(ctx, gh)).To(specs.BeNil())
				c.end("plain", nil)
				ctx.Expect(result(ctx, plain["plain"])).To(specs.BeNil())
			})

			s.It("holds no slot while it waits, so other work takes it", func(ctx *specs.Context) {
				l, clk, c := newLimits(0, epoch.Add(time.Hour)), newClock(), newCrew()
				sch := newGitHub(ctx, 1, l, clk)

				gh := github(sch, c, "gh")
				done := submit(sch, c, "a", "b")

				c.awaitStart(ctx, "a")
				clk.awaitAsked(ctx)
				ctx.Expect(c.started("gh")).To(specs.BeFalse())
				ctx.Expect(sch.Running()).ToEqual(1)
				ctx.Expect(sch.Waiting()).ToEqual(1)
				c.end("a", nil)
				c.awaitStart(ctx, "b")
				ctx.Expect(c.started("gh")).To(specs.BeFalse())

				l.set(5000, epoch.Add(2*time.Hour))
				clk.advance(time.Hour)
				c.end("b", nil)

				c.awaitStart(ctx, "gh")
				c.end("gh", nil)
				ctx.Expect(result(ctx, gh)).To(specs.BeNil())
				ctx.Expect(result(ctx, done["b"])).To(specs.BeNil())
			})

			s.It("runs once the reset passed, even if the source still reports the old limit", func(ctx *specs.Context) {
				l, clk, c := newLimits(0, epoch.Add(time.Hour)), newClock(), newCrew()
				sch := newGitHub(ctx, 1, l, clk)

				gh := github(sch, c, "gh")
				clk.awaitAsked(ctx)
				clk.advance(time.Hour)

				c.awaitStart(ctx, "gh")
				c.end("gh", nil)
				ctx.Expect(result(ctx, gh)).To(specs.BeNil())
				ctx.Expect(l.asked()).ToEqual(2)
			})

			s.It("hands back the context's error and never runs if the context ends while it waits", func(ctx *specs.Context) {
				l, clk, c := newLimits(0, epoch.Add(time.Hour)), newClock(), newCrew()
				sch := newGitHub(ctx, 1, l, clk)
				cctx, cancel := context.WithCancel(context.Background())

				gh := sch.SubmitGitHub(cctx, "gh", c.work("gh"))
				clk.awaitAsked(ctx)
				cancel()

				ctx.Expect(result(ctx, gh)).To(specs.MatchError(context.Canceled))
				ctx.Expect(c.started("gh")).To(specs.BeFalse())
				ctx.Expect(sch.Running()).ToEqual(0)
			})

			s.It("does not wait for a job that spent its budget", func(ctx *specs.Context) {
				spent := newCosts()
				spent.set("gh", budget)
				clk := newClock()
				sch, err := scheduler.New(1, budget, spent, scheduler.GitHub{Limits: newLimits(0, epoch.Add(time.Hour)), Threshold: threshold, Clock: clk})
				ctx.Expect(err).To(specs.BeNil())
				c := newCrew()

				ctx.Expect(result(ctx, github(sch, c, "gh"))).To(specs.MatchError(scheduler.ErrBudgetExhausted))
				ctx.Expect(c.started("gh")).To(specs.BeFalse())
				ctx.Expect(len(clk.asked)).ToEqual(0)
			})

			s.It("runs a stage that does not use GitHub at once, without asking for the limit", func(ctx *specs.Context) {
				l, clk, c := newLimits(0, epoch.Add(time.Hour)), newClock(), newCrew()
				sch := newGitHub(ctx, 1, l, clk)

				done := submit(sch, c, "plain")

				c.awaitStart(ctx, "plain")
				c.end("plain", nil)
				ctx.Expect(result(ctx, done["plain"])).To(specs.BeNil())
				ctx.Expect(l.asked()).ToEqual(0)
				ctx.Expect(len(clk.asked)).ToEqual(0)
			})
		})

		rows := []int{threshold, threshold + 1}
		specs.Table(s, rows, func(remaining int) string {
			if remaining == threshold {
				return "runs a GitHub stage at once when exactly the threshold remains"
			}
			return "runs a GitHub stage at once when more than the threshold remains"
		}, func(ctx *specs.Context, remaining int) {
			l, clk, c := newLimits(remaining, epoch.Add(time.Hour)), newClock(), newCrew()
			sch := newGitHub(ctx, 1, l, clk)

			gh := github(sch, c, "gh")

			c.awaitStart(ctx, "gh")
			c.end("gh", nil)
			ctx.Expect(result(ctx, gh)).To(specs.BeNil())
			ctx.Expect(len(clk.asked)).ToEqual(0)
		})

		s.When("the limit source fails", func(s *specs.Spec) {
			s.It("does not run the stage, hands the error back and frees the slot", func(ctx *specs.Context) {
				l, c := newLimits(5000, epoch), newCrew()
				l.err = errLimits
				sch := newGitHub(ctx, 1, l, newClock())

				err := result(ctx, github(sch, c, "gh"))

				ctx.Expect(err).To(specs.MatchError(errLimits))
				ctx.Expect(c.started("gh")).To(specs.BeFalse())
				ctx.Expect(sch.Running()).ToEqual(0)
			})
		})

		refused := []refusal{
			{"refuses a negative threshold", scheduler.GitHub{Limits: newLimits(0, epoch), Threshold: -1, Clock: newClock()}},
			{"refuses no limit source", scheduler.GitHub{Threshold: threshold, Clock: newClock()}},
			{"refuses no clock", scheduler.GitHub{Limits: newLimits(0, epoch), Threshold: threshold}},
		}
		specs.Table(s, refused, func(r refusal) string { return r.name }, func(ctx *specs.Context, r refusal) {
			_, err := scheduler.New(1, budget, newCosts(), r.gh)
			ctx.Expect(err).To(specs.MatchError(scheduler.ErrGitHub))
		})
	})
}
