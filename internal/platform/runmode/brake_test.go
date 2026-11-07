package runmode_test

import (
	"context"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/platform/runmode"
	"github.com/pablogore/software-factory/internal/platform/scheduler"
)

func TestBrake(t *testing.T) {
	specs.Describe(t, "runmode.Brake", func(s *specs.Spec) {
		s.When("the brake is off", func(s *specs.Spec) {
			s.It("hands back the work's result and the scheduler's waiting work", func(ctx *specs.Context) {
				b := runmode.NewBrake(newScheduler(ctx, 1, costs{}))
				release := make(chan struct{})
				busy := b.Submit(context.Background(), "busy", func(context.Context) error { <-release; return errAgentFailed })
				waits := b.Submit(context.Background(), "waits", func(context.Context) error { return nil })
				ctx.Expect(b.Waiting()).ToEqual(1)
				close(release)
				ctx.Expect(<-busy).To(specs.MatchError(errAgentFailed))
				ctx.Expect(<-waits).To(specs.BeNil())
			})
		})

		s.When("the brake is on", func(s *specs.Spec) {
			s.It("counts one more waiting work and holds what reaches a slot until released", func(ctx *specs.Context) {
				b := runmode.NewBrake(newScheduler(ctx, 1, costs{}))
				b.Engage()
				ctx.Expect(b.Waiting()).ToEqual(1)
				ran := make(chan struct{})
				done := b.Submit(context.Background(), "held", func(context.Context) error { close(ran); return nil })
				select {
				case <-ran:
					ctx.Errorf("held work ran with the brake on")
				case <-done:
					ctx.Errorf("held work got a result with the brake on")
				case <-time.After(50 * time.Millisecond):
				}
				b.Release()
				ctx.Expect(b.Waiting()).ToEqual(0)
				select {
				case err := <-done:
					ctx.Expect(err).To(specs.BeNil())
				case <-time.After(patience):
					ctx.Errorf("held work did not run once released")
				}
			})

			s.It("hands back why the scheduler never ran the work", func(ctx *specs.Context) {
				b := runmode.NewBrake(newScheduler(ctx, 1, costs{spent: 10}))
				b.Engage()
				done := b.Submit(context.Background(), "spent", func(context.Context) error { return nil })
				select {
				case err := <-done:
					ctx.Expect(err).To(specs.MatchError(scheduler.ErrBudgetExhausted))
				case <-time.After(patience):
					ctx.Errorf("no result for work over budget")
				}
			})
		})
	})
}
