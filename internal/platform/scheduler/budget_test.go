package scheduler_test

import (
	"errors"
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/domain/decide"
	"github.com/pablogore/software-factory/internal/platform/scheduler"
)

var errCosts = errors.New("cost source down")

// building is a job in the build stage of a one-stage pipeline, about to
// launch its builder.
func building(name string) (domain.Job, domain.Pipeline) {
	job := domain.Job{ID: domain.JobID(name), Title: "Budget", Pipeline: "default", Stage: "build", Round: 1}
	p := domain.Pipeline{Name: "default", Stages: []domain.Stage{{Name: "build", Kind: domain.KindBuild, Roles: []string{"builder"}}}}
	return job, p
}

func TestSchedulerBudget(t *testing.T) {
	specs.Describe(t, "scheduler.Scheduler with a budget", func(s *specs.Spec) {
		s.When("the job spent USD 5 of its 5", func(s *specs.Spec) {
			s.It("never launches the agent and the job goes to needs-human for budget exhausted", func(ctx *specs.Context) {
				spent := newCosts()
				spent.set("a", 5)
				sch, c := newBudgeted(ctx, 2, spent), newCrew()

				err := result(ctx, submit(sch, c, "a")["a"])

				ctx.Expect(err).To(specs.MatchError(scheduler.ErrBudgetExhausted))
				ctx.Expect(c.started("a")).To(specs.BeFalse())
				ctx.Expect(sch.Running()).ToEqual(0)
				job, p := building("a")
				evs, err := decide.Decide(job, domain.BudgetExhausted{Job: job.ID, At: time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)}, p)
				ctx.Expect(err).To(specs.BeNil())
				after := decide.Apply(job, evs)
				ctx.Expect(after.Stage).ToEqual(domain.StageNeedsHuman)
				ctx.Expect(after.Reason).ToEqual("budget exhausted")
				ctx.Expect(after.Cause).ToEqual(domain.CauseBudget)
			})

			s.It("never launches a person's reply either, and frees the reserved slot", func(ctx *specs.Context) {
				spent := newCosts()
				spent.set("a", 5)
				sch, c := newBudgeted(ctx, 1, spent), newCrew()

				err := result(ctx, reply(sch, c, "a"))

				ctx.Expect(err).To(specs.MatchError(scheduler.ErrBudgetExhausted))
				ctx.Expect(c.started("a")).To(specs.BeFalse())
				ctx.Expect(sch.Running()).ToEqual(0)
				r := reply(sch, c, "b")
				c.awaitStart(ctx, "b")
				c.end("b", nil)
				ctx.Expect(result(ctx, r)).To(specs.BeNil())
			})
		})

		s.When("the job spent USD 4.99 of its 5", func(s *specs.Spec) {
			s.It("launches the agent as before", func(ctx *specs.Context) {
				spent := newCosts()
				spent.set("a", 4.99)
				sch, c := newBudgeted(ctx, 2, spent), newCrew()

				done := submit(sch, c, "a")

				c.awaitStart(ctx, "a")
				ctx.Expect(sch.Running()).ToEqual(1)
				c.end("a", nil)
				ctx.Expect(result(ctx, done["a"])).To(specs.BeNil())
			})
		})

		s.When("a job that spent its budget is next in line", func(s *specs.Spec) {
			s.It("never takes the slot, so the job behind it runs", func(ctx *specs.Context) {
				spent := newCosts()
				spent.set("spent", 5)
				sch, c := newBudgeted(ctx, 1, spent), newCrew()
				done := submit(sch, c, "a", "spent", "b")
				c.awaitStart(ctx, "a")
				ctx.Expect(sch.Waiting()).ToEqual(2)

				c.end("a", nil)

				c.awaitStart(ctx, "b")
				ctx.Expect(result(ctx, done["spent"])).To(specs.MatchError(scheduler.ErrBudgetExhausted))
				ctx.Expect(c.started("spent")).To(specs.BeFalse())
				ctx.Expect(sch.Running()).ToEqual(1)
				ctx.Expect(sch.Waiting()).ToEqual(0)
				c.end("b", nil)
				ctx.Expect(result(ctx, done["b"])).To(specs.BeNil())
			})

			s.It("checks the spend at launch, not when the work was handed over", func(ctx *specs.Context) {
				spent := newCosts()
				sch, c := newBudgeted(ctx, 1, spent), newCrew()
				done := submit(sch, c, "a", "late")
				c.awaitStart(ctx, "a")

				spent.set("late", 5)
				c.end("a", nil)

				ctx.Expect(result(ctx, done["late"])).To(specs.MatchError(scheduler.ErrBudgetExhausted))
				ctx.Expect(c.started("late")).To(specs.BeFalse())
				ctx.Expect(sch.Running()).ToEqual(0)
			})
		})

		s.When("the cost source fails", func(s *specs.Spec) {
			s.It("does not launch the agent, hands the error back and frees the slot", func(ctx *specs.Context) {
				spent := newCosts()
				spent.err = errCosts
				sch, c := newBudgeted(ctx, 1, spent), newCrew()

				err := result(ctx, submit(sch, c, "a")["a"])

				ctx.Expect(err).To(specs.MatchError(errCosts))
				ctx.Expect(err).To(specs.Not(specs.MatchError(scheduler.ErrBudgetExhausted)))
				ctx.Expect(c.started("a")).To(specs.BeFalse())
				ctx.Expect(sch.Running()).ToEqual(0)
			})
		})

		rows := []float64{0, -1, math.NaN()}
		specs.Table(s, rows, func(usd float64) string { return "refuses a budget of USD " + strconv.FormatFloat(usd, 'f', -1, 64) }, func(ctx *specs.Context, usd float64) {
			_, err := scheduler.New(1, usd, newCosts())
			ctx.Expect(err).To(specs.MatchError(scheduler.ErrBudget))
		})

		s.It("refuses a budget without a cost source", func(ctx *specs.Context) {
			_, err := scheduler.New(1, budget, nil)
			ctx.Expect(err).To(specs.MatchError(scheduler.ErrBudget))
		})
	})
}
