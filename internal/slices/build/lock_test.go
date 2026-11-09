package build_test

import (
	"context"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/gatelock"
)

// afterRelease is a checks run that records whether the run_gate run's lock
// was already released when the gate's checks started.
type afterRelease struct {
	released <-chan struct{}
	clean    bool
}

func (a *afterRelease) Run(context.Context, domain.JobID, string, []string) (domain.ChecksRun, error) {
	select {
	case <-a.released:
		a.clean = true
	default:
	}
	return passing, nil
}

func TestGateLock(t *testing.T) {
	specs.Describe(t, "build.Gate.Run and the job's lock", func(s *specs.Spec) {
		bg := context.Background()

		s.It("cancels the builder's run_gate run and starts its checks only once that run stopped", func(ctx *specs.Context) {
			locks := gatelock.New()
			run, release, err := locks.Try(bg, "j-1")
			ctx.Expect(err).To(specs.BeNil())
			released := make(chan struct{})
			go func() {
				<-run.Done()
				time.Sleep(50 * time.Millisecond)
				release()
				close(released)
			}()
			c := &afterRelease{released: released}
			g, _ := newGate(ctx, domain.StageGate, 1, &checks{})
			g.Checks, g.Locks = c, locks

			_, err = g.Run(bg, "j-1")

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(c.clean).To(specs.BeTrue())
			ctx.Expect(context.Cause(run)).ToEqual(gatelock.ErrPreempted)
		})

		s.It("holds the lock while it runs, so a run_gate meanwhile is refused, and releases it after", func(ctx *specs.Context) {
			locks := gatelock.New()
			g, _ := newGate(ctx, domain.StageGate, 1, &checks{run: passing})
			g.Locks = locks
			var during error
			g.Checks = checkFunc(func() { _, _, during = locks.Try(bg, "j-1") })

			_, err := g.Run(bg, "j-1")

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(during).To(specs.MatchError(domain.ErrBusy))
			_, after, err := locks.Try(bg, "j-1")
			ctx.Expect(err).To(specs.BeNil())
			after()
		})

		s.It("refuses to run without locks, running no check", func(ctx *specs.Context) {
			c := &checks{run: passing}
			g, _ := newGate(ctx, domain.StageGate, 1, c)
			g.Locks = nil

			_, err := g.Run(bg, "j-1")

			ctx.Expect(err).To(specs.MatchError(domain.ErrMissingField))
			ctx.Expect(c.calls).ToEqual(0)
		})
	})
}

// checkFunc is a passing checks run that calls f while it runs.
type checkFunc func()

func (f checkFunc) Run(context.Context, domain.JobID, string, []string) (domain.ChecksRun, error) {
	f()
	return passing, nil
}
