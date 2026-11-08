package gatelock_test

import (
	"context"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/gatelock"
)

// stopsOnCancel runs as a run taken with Try does: until run is cancelled,
// then it takes a while to stop and releases the lock. It returns the
// channel closed once it released.
func stopsOnCancel(run context.Context, release func()) <-chan struct{} {
	released := make(chan struct{})
	go func() {
		<-run.Done()
		time.Sleep(50 * time.Millisecond)
		release()
		close(released)
	}()
	return released
}

func released(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func TestLocks(t *testing.T) {
	specs.Describe(t, "gatelock.Locks", func(s *specs.Spec) {
		bg := context.Background()

		s.It("refuse a second Try on a job while the first holds it, and take it again once released", func(ctx *specs.Context) {
			l := gatelock.New()
			_, release, err := l.Try(bg, "j-1")
			ctx.Expect(err).To(specs.BeNil())

			_, _, err = l.Try(bg, "j-1")
			ctx.Expect(err).To(specs.MatchError(domain.ErrBusy))

			release()
			_, again, err := l.Try(bg, "j-1")
			ctx.Expect(err).To(specs.BeNil())
			again()
		})

		s.It("hold each job's lock apart", func(ctx *specs.Context) {
			l := gatelock.New()
			_, r1, err := l.Try(bg, "j-1")
			ctx.Expect(err).To(specs.BeNil())
			defer r1()
			_, r2, err := l.Try(bg, "j-2")
			ctx.Expect(err).To(specs.BeNil())
			r2()
		})

		s.It("Preempt cancels the run with ErrPreempted and returns only once it released the lock", func(ctx *specs.Context) {
			l := gatelock.New()
			run, release, err := l.Try(bg, "j-1")
			ctx.Expect(err).To(specs.BeNil())
			done := stopsOnCancel(run, release)

			ctx.Expect(l.Preempt(bg, "j-1")).To(specs.BeNil())

			ctx.Expect(released(done)).To(specs.BeTrue())
			ctx.Expect(context.Cause(run)).ToEqual(gatelock.ErrPreempted)
		})

		s.It("Preempt does nothing on a job no run holds", func(ctx *specs.Context) {
			ctx.Expect(gatelock.New().Preempt(bg, "j-1")).To(specs.BeNil())
		})

		s.It("Preempt gives up with ctx's error when ctx ends before the run stops", func(ctx *specs.Context) {
			l := gatelock.New()
			_, release, err := l.Try(bg, "j-1")
			ctx.Expect(err).To(specs.BeNil())
			defer release()
			short, cancel := context.WithTimeout(bg, 20*time.Millisecond)
			defer cancel()

			ctx.Expect(l.Preempt(short, "j-1")).To(specs.MatchError(context.DeadlineExceeded))
		})

		s.It("Acquire cancels a run taken with Try and takes the lock once it released it", func(ctx *specs.Context) {
			l := gatelock.New()
			run, release, err := l.Try(bg, "j-1")
			ctx.Expect(err).To(specs.BeNil())
			done := stopsOnCancel(run, release)

			gate, err := l.Acquire(bg, "j-1")

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(released(done)).To(specs.BeTrue())
			ctx.Expect(context.Cause(run)).ToEqual(gatelock.ErrPreempted)
			gate()
		})

		s.It("refuse a Try while the gate holds the lock, and Preempt leaves the gate alone", func(ctx *specs.Context) {
			l := gatelock.New()
			gate, err := l.Acquire(bg, "j-1")
			ctx.Expect(err).To(specs.BeNil())

			_, _, err = l.Try(bg, "j-1")
			ctx.Expect(err).To(specs.MatchError(domain.ErrBusy))
			ctx.Expect(l.Preempt(bg, "j-1")).To(specs.BeNil())

			gate()
			_, again, err := l.Try(bg, "j-1")
			ctx.Expect(err).To(specs.BeNil())
			again()
		})

		s.It("Acquire waits for the gate that holds the lock", func(ctx *specs.Context) {
			l := gatelock.New()
			first, err := l.Acquire(bg, "j-1")
			ctx.Expect(err).To(specs.BeNil())
			go func() {
				time.Sleep(50 * time.Millisecond)
				first()
			}()
			start := time.Now()

			second, err := l.Acquire(bg, "j-1")

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(time.Since(start) >= 50*time.Millisecond).To(specs.BeTrue())
			second()
		})
	})
}
