package decide_test

import (
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/domain/decide"
)

func budgetSpent(minute int) domain.BudgetExhausted {
	return domain.BudgetExhausted{Job: jobID, At: at(minute)}
}

func TestDecideBudget(t *testing.T) {
	specs.Describe(t, "Decide on a spent budget", func(s *specs.Spec) {
		s.When("the job is about to launch an agent", func(s *specs.Spec) {
			stages := []string{"intake", "spec", "build", "review", "approve"}
			specs.Table(s, stages, func(st string) string { return "in " + st + " it waits in needs-human for budget exhausted" }, func(ctx *specs.Context, st string) {
				job := newJob()
				job.Stage, job.Round = st, 2

				evs, err := decide.Decide(job, budgetSpent(1), defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual([]domain.Event{domain.NeedsHuman{Job: jobID, Reason: "budget exhausted", Cause: domain.CauseBudget, At: at(1)}})
				after := decide.Apply(job, evs)
				ctx.Expect(after.Stage).ToEqual(domain.StageNeedsHuman)
				ctx.Expect(after.Reason).ToEqual("budget exhausted")
				ctx.Expect(after.Cause).ToEqual(domain.CauseBudget)
				ctx.Expect(after.Round).ToEqual(2)
			})
		})

		s.When("the job launches no agent in its stage", func(s *specs.Spec) {
			stages := []string{domain.StageSpecgate, domain.StageGate, domain.StageMerge, domain.StageDone, domain.StageNeedsHuman, domain.StageNeedsInfo, domain.StageRejected}
			specs.Table(s, stages, func(st string) string { return "in " + st + " it is refused" }, func(ctx *specs.Context, st string) {
				job := newJob()
				job.Stage = st

				evs, err := decide.Decide(job, budgetSpent(1), defaultPipeline())

				ctx.Expect(err).To(specs.MatchError(decide.ErrIllegalTransition))
				ctx.Expect(evs).To(specs.BeEmpty())
			})

			s.It("in stopped it is refused as for any event", func(ctx *specs.Context) {
				evs, err := decide.Decide(stoppedJob(), budgetSpent(1), defaultPipeline())

				ctx.Expect(err).To(specs.MatchError(decide.ErrStopped))
				ctx.Expect(evs).To(specs.BeEmpty())
			})
		})

		s.When("a person replies to a job stopped for its budget", func(s *specs.Spec) {
			s.It("keeps it in needs-human for budget exhausted", func(ctx *specs.Context) {
				job := waitingFor(ctx, domain.CauseBudget)

				evs, err := decide.Decide(job, message(domain.AuthorPerson, true, 2), defaultPipeline())

				ctx.Expect(err).To(specs.MatchError(decide.ErrUnsupported))
				ctx.Expect(evs).To(specs.BeEmpty())
				after := decide.Apply(job, evs)
				ctx.Expect(after.Stage).ToEqual(domain.StageNeedsHuman)
				ctx.Expect(after.Reason).ToEqual("budget exhausted")
				ctx.Expect(after.Cause).ToEqual(domain.CauseBudget)
			})
		})
	})
}
