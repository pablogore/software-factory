package runmode_test

import (
	"context"
	"errors"
	"math"
	"sync/atomic"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/runmode"
)

// raiseCap is a person's raise of cap c to value on issue job's stop seq, as
// the event source marks it.
func raiseCap(job domain.JobID, kind domain.AuthorKind, allowed bool, c domain.IssueCap, value float64, seq int) domain.CapRaiseRequested {
	return domain.CapRaiseRequested{Job: job, By: "pablogore", AuthorKind: kind, Allowed: allowed, Cap: c, Value: value, WaitSeq: seq, At: epoch}
}

// stoppedAt is a starter whose issue #1, worked as the slices of usd, is
// stopped before slice "1-s9", whose work counts its runs in ran.
func stoppedAt(ctx *specs.Context, ran *atomic.Int32, usd ...float64) starter {
	st := newStarter(ctx, newScheduler(ctx, 1, costs{}), runmode.DefaultCaps, reports{}.add(sliced(1, usd...)), &outbox{}, &stops{})
	_, err := st.Start(context.Background(), domain.NextSliceRequested{Job: "1-s1", Initiative: "1", At: epoch}, "1-s9", func(context.Context) error {
		ran.Add(1)
		return nil
	})
	ctx.Expect(errors.Is(err, runmode.ErrCapped)).To(specs.BeTrue())
	return st
}

type starterRefusalRow struct {
	name string
	ev   domain.CapRaiseRequested
	code domain.RaiseRefusal
}

func TestStarterRaise(t *testing.T) {
	specs.Describe(t, "runmode.Starter on a person's raise of a cap", func(s *specs.Spec) {
		s.When("the issue is stopped at its 5 slices", func(s *specs.Spec) {
			s.It("starts the slice it held once an allowed person raises the cap to 6", func(ctx *specs.Context) {
				var ran atomic.Int32
				st := stoppedAt(ctx, &ran, 1, 1, 1, 1, 1)

				done, err := st.Raise(context.Background(), raiseCap("1-s1", domain.AuthorPerson, true, domain.CapSlices, 6, 1))

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(await(ctx, done)).To(specs.BeNil())
				ctx.Expect(ran.Load()).ToEqual(int32(1))
			})

			s.It("starts nothing without a raise, nor on a raise not from an allowed person", func(ctx *specs.Context) {
				var ran atomic.Int32
				st := stoppedAt(ctx, &ran, 1, 1, 1, 1, 1)

				for _, ev := range []domain.CapRaiseRequested{
					raiseCap("1-s1", domain.AuthorPerson, false, domain.CapSlices, 6, 1),
					raiseCap("1-s1", domain.AuthorBot, true, domain.CapSlices, 6, 1),
					raiseCap("1-s1", domain.AuthorCI, true, domain.CapSlices, 6, 1),
				} {
					done, err := st.Raise(context.Background(), ev)
					ctx.Expect(err).To(specs.BeNil())
					ctx.Expect(done).To(specs.BeNil())
				}

				ctx.Expect(ran.Load()).ToEqual(int32(0))
				// The stop still waits for an allowed person's raise.
				done, err := st.Raise(context.Background(), raiseCap("1-s1", domain.AuthorPerson, true, domain.CapSlices, 6, 1))
				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(await(ctx, done)).To(specs.BeNil())
			})

			s.It("stops again at the USD cap when the issue is past it too, under a new wait", func(ctx *specs.Context) {
				var ran atomic.Int32
				st := stoppedAt(ctx, &ran, 2, 2, 2, 2, 2)

				_, err := st.Raise(context.Background(), raiseCap("1-s1", domain.AuthorPerson, true, domain.CapSlices, 6, 1))

				ctx.Expect(errors.Is(err, runmode.ErrCapped)).To(specs.BeTrue())
				ctx.Expect(err.Error()).To(specs.Contain("issue-usd 10 of 8"))
				done, err := st.Raise(context.Background(), raiseCap("1-s1", domain.AuthorPerson, true, domain.CapUSD, 12, 2))
				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(await(ctx, done)).To(specs.BeNil())
				ctx.Expect(ran.Load()).ToEqual(int32(1))
			})
		})

		s.When("the issue is stopped past its USD 8", func(s *specs.Spec) {
			s.It("starts the slice it held once an allowed person raises the cap past the USD reached", func(ctx *specs.Context) {
				var ran atomic.Int32
				st := stoppedAt(ctx, &ran, 5, 3.5)

				done, err := st.Raise(context.Background(), raiseCap("1-s2", domain.AuthorPerson, true, domain.CapUSD, 12.5, 1))

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(await(ctx, done)).To(specs.BeNil())
				ctx.Expect(ran.Load()).ToEqual(int32(1))
			})
		})

		s.When("the raise is invalid", func(s *specs.Spec) {
			valid := raiseCap("1-s1", domain.AuthorPerson, true, domain.CapSlices, 6, 1)
			with := func(change func(*domain.CapRaiseRequested)) domain.CapRaiseRequested {
				e := valid
				change(&e)
				return e
			}
			rows := []starterRefusalRow{
				{"without who asked", with(func(e *domain.CapRaiseRequested) { e.By = "\u200b" }), domain.RaiseNoAuthor},
				{"for an unknown cap", with(func(e *domain.CapRaiseRequested) { e.Cap = "slices" }), domain.RaiseUnknownCap},
				{"to 0 slices", with(func(e *domain.CapRaiseRequested) { e.Value = 0 }), domain.RaiseBadValue},
				{"to 6.5 slices", with(func(e *domain.CapRaiseRequested) { e.Value = 6.5 }), domain.RaiseBadValue},
				{"to more slices than an int holds", with(func(e *domain.CapRaiseRequested) { e.Value = 1e300 }), domain.RaiseBadValue},
				{"to NaN", with(func(e *domain.CapRaiseRequested) { e.Value = math.NaN() }), domain.RaiseBadValue},
				{"without the wait it answers", with(func(e *domain.CapRaiseRequested) { e.WaitSeq = 0 }), domain.RaiseStaleWait},
				{"for another wait", with(func(e *domain.CapRaiseRequested) { e.WaitSeq = 2 }), domain.RaiseStaleWait},
				{"for the cap the issue is not stopped at", with(func(e *domain.CapRaiseRequested) { e.Cap, e.Value = domain.CapUSD, 20 }), domain.RaiseNotStopped},
				{"for a job's budget", with(func(e *domain.CapRaiseRequested) { e.Cap, e.Value = domain.CapBudget, 20 }), domain.RaiseNotStopped},
				{"to the 5 slices reached", with(func(e *domain.CapRaiseRequested) { e.Value = 5 }), domain.RaiseNotAbove},
				{"to 4 slices", with(func(e *domain.CapRaiseRequested) { e.Value = 4 }), domain.RaiseNotAbove},
			}
			specs.Table(s, rows, func(r starterRefusalRow) string { return r.name + " it starts nothing, with code " + string(r.code) }, func(ctx *specs.Context, r starterRefusalRow) {
				var ran atomic.Int32
				st := stoppedAt(ctx, &ran, 1, 1, 1, 1, 1)

				done, err := st.Raise(context.Background(), r.ev)

				ctx.Expect(done).To(specs.BeNil())
				code, ok := domain.RaiseRefusalOf(err)
				ctx.Expect(ok).To(specs.BeTrue())
				ctx.Expect(code).ToEqual(r.code)
				ctx.Expect(ran.Load()).ToEqual(int32(0))
			})

			s.It("on an issue that is not stopped it starts nothing, with code raise-not-stopped", func(ctx *specs.Context) {
				st := newStarter(ctx, newScheduler(ctx, 1, costs{}), runmode.DefaultCaps, reports{}.add(sliced(2, 1)), &outbox{}, &stops{})

				done, err := st.Raise(context.Background(), raiseCap("2-s1", domain.AuthorPerson, true, domain.CapSlices, 6, 1))

				ctx.Expect(done).To(specs.BeNil())
				code, _ := domain.RaiseRefusalOf(err)
				ctx.Expect(code).ToEqual(domain.RaiseNotStopped)
			})

			s.It("when the raise cannot be saved it starts nothing and returns the error", func(ctx *specs.Context) {
				var ran atomic.Int32
				st := stoppedAt(ctx, &ran, 1, 1, 1, 1, 1)
				st.log.fail = errSaveFailed

				done, err := st.Raise(context.Background(), raiseCap("1-s1", domain.AuthorPerson, true, domain.CapSlices, 6, 1))

				ctx.Expect(done).To(specs.BeNil())
				ctx.Expect(errors.Is(err, errSaveFailed)).To(specs.BeTrue())
				ctx.Expect(ran.Load()).ToEqual(int32(0))
			})

			s.It("once accepted, the same raise delivered again starts nothing more", func(ctx *specs.Context) {
				var ran atomic.Int32
				st := stoppedAt(ctx, &ran, 1, 1, 1, 1, 1)
				raise := raiseCap("1-s1", domain.AuthorPerson, true, domain.CapSlices, 6, 1)
				done, err := st.Raise(context.Background(), raise)
				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(await(ctx, done)).To(specs.BeNil())

				done, err = st.Raise(context.Background(), raise)

				ctx.Expect(done).To(specs.BeNil())
				code, _ := domain.RaiseRefusalOf(err)
				ctx.Expect(code).ToEqual(domain.RaiseNotStopped)
				ctx.Expect(ran.Load()).ToEqual(int32(1))
			})
		})
	})
}
