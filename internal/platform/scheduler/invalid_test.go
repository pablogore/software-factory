package scheduler_test

import (
	"math"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/platform/scheduler"
)

// invalid is a spend or budget the cost source tells that is not a finite
// amount at or above 0.
type invalid struct {
	value string
	usd   float64
	shown string
}

var invalids = []invalid{
	{"spend", math.NaN(), "NaN"},
	{"spend", math.Inf(1), "+Inf"},
	{"spend", math.Inf(-1), "-Inf"},
	{"spend", -0.01, "-0.01"},
	{"budget", math.NaN(), "NaN"},
	{"budget", math.Inf(1), "+Inf"},
	{"budget", math.Inf(-1), "-Inf"},
	{"budget", -1, "-1"},
}

func (r invalid) costs() *costs {
	c := newCosts()
	if r.value == "spend" {
		c.set("a", r.usd)
	} else {
		c.raise("a", r.usd)
	}
	return c
}

func TestDemoInvalidCosts(t *testing.T) {
	specs.Describe(t, "scheduler.Scheduler with an invalid spend or budget", func(s *specs.Spec) {
		specs.Table(s, invalids, func(r invalid) string {
			return "never launches the agent for a " + r.value + " of USD " + r.shown + " and frees the slot"
		}, func(ctx *specs.Context, r invalid) {
			c := r.costs()
			sch, cr := newBudgeted(ctx, 1, c), newCrew()

			done := submit(sch, cr, "a", "b")

			cr.awaitStart(ctx, "b")
			err := result(ctx, done["a"])
			ctx.Expect(err).To(specs.MatchError(scheduler.ErrInvalidCost))
			ctx.Expect(err).To(specs.Not(specs.MatchError(scheduler.ErrBudgetExhausted)))
			ctx.Expect(err).To(specs.Not(specs.MatchError(errCosts)))
			ctx.Expect(err.Error()).ToEqual("scheduler: " + r.value + ` of job "a" is USD ` + r.shown + ": invalid cost")
			ctx.Expect(cr.started("a")).To(specs.BeFalse())
			cr.end("b", nil)
			ctx.Expect(result(ctx, done["b"])).To(specs.BeNil())
			ctx.Expect(sch.Running()).ToEqual(0)
		})

		s.It("never launches a person's reply either, and frees the reserved slot", func(ctx *specs.Context) {
			c := newCosts()
			c.set("a", math.NaN())
			sch, cr := newBudgeted(ctx, 1, c), newCrew()

			err := result(ctx, reply(sch, cr, "a"))

			ctx.Expect(err).To(specs.MatchError(scheduler.ErrInvalidCost))
			ctx.Expect(cr.started("a")).To(specs.BeFalse())
			ctx.Expect(sch.Running()).ToEqual(0)
		})

		s.It("launches the agent of a job that spent USD 0 with no budget of its own", func(ctx *specs.Context) {
			sch, cr := newBudgeted(ctx, 1, newCosts()), newCrew()

			done := submit(sch, cr, "a")

			cr.awaitStart(ctx, "a")
			cr.end("a", nil)
			ctx.Expect(result(ctx, done["a"])).To(specs.BeNil())
		})

		s.It("holds a job with no budget of its own to the scheduler's: USD 5 of 5 is exhausted", func(ctx *specs.Context) {
			c := newCosts()
			c.set("a", budget)
			c.raise("a", 0)
			sch, cr := newBudgeted(ctx, 1, c), newCrew()

			err := result(ctx, submit(sch, cr, "a")["a"])

			ctx.Expect(err).To(specs.MatchError(scheduler.ErrBudgetExhausted))
			ctx.Expect(err).To(specs.Not(specs.MatchError(scheduler.ErrInvalidCost)))
			ctx.Expect(cr.started("a")).To(specs.BeFalse())
		})

		s.It("still launches the agent of a job that spent USD 4.99 of 5", func(ctx *specs.Context) {
			c := newCosts()
			c.set("a", 4.99)
			sch, cr := newBudgeted(ctx, 1, c), newCrew()

			done := submit(sch, cr, "a")

			cr.awaitStart(ctx, "a")
			cr.end("a", nil)
			ctx.Expect(result(ctx, done["a"])).To(specs.BeNil())
		})
	})
}
