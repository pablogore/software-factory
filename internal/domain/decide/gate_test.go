package decide_test

import (
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/domain/decide"
)

type gateRow struct {
	name      string
	round     int
	reviewers []string
	want      []domain.Event
}

func atGate(round int, reviewers []string) domain.Job {
	job := newJob()
	job.Stage, job.Round, job.Reviewers = domain.StageGate, round, reviewers
	return job
}

func gateFailed(minute int) domain.GateFailed {
	return domain.GateFailed{Job: jobID, At: at(minute)}
}

func TestDecideGate(t *testing.T) {
	specs.Describe(t, "Decide on a failing gate", func(s *specs.Spec) {
		secCode := []string{"reviewer-security", "reviewer-code"}

		s.When("the gate fails at the gate", func(s *specs.Spec) {
			rows := []gateRow{
				{"in round 1 goes back to build in round 2 with every reviewer", 1, nil,
					[]domain.Event{started(2, 1), entered("build", 1)}},
				{"in round 2 goes back to build in round 3 with the round's reviewers", 2, secCode,
					[]domain.Event{started(3, 1, secCode...), entered("build", 1)}},
				{"in round 3 of 3 ends in needs-human", 3, secCode,
					[]domain.Event{domain.NeedsHuman{Job: jobID, Reason: "gate failed in the last round (3 of 3)", At: at(1)}}},
			}
			specs.Table(s, rows, func(r gateRow) string { return r.name }, func(ctx *specs.Context, r gateRow) {
				evs, err := decide.Decide(atGate(r.round, r.reviewers), gateFailed(1), threeReviewers())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual(r.want)
			})
		})

		s.When("the gate fails outside the gate", func(s *specs.Spec) {
			stages := []string{"intake", "spec", domain.StageSpecgate, "build", "review", "approve", domain.StageMerge, domain.StageNeedsHuman}
			specs.Table(s, stages, func(st string) string { return "in " + st + " is refused" }, func(ctx *specs.Context, st string) {
				job, before := atGate(2, secCode), atGate(2, secCode)
				job.Stage, before.Stage = st, st

				evs, err := decide.Decide(job, gateFailed(1), threeReviewers())

				ctx.Expect(err).To(specs.MatchError(decide.ErrIllegalTransition))
				ctx.Expect(evs).To(specs.BeEmpty())
				ctx.Expect(job).ToEqual(before)
				ctx.Expect(decide.Apply(job, evs)).ToEqual(before)
			})
		})

		s.It("runs in the rebuilt round the reviewers the failed round was going to run", func(ctx *specs.Context) {
			p := threeReviewers()
			job := atGate(2, secCode)
			step := func(ev domain.Event) {
				evs, err := decide.Decide(job, ev, p)
				ctx.Expect(err).To(specs.BeNil())
				job = decide.Apply(job, evs)
			}

			step(gateFailed(1))
			ctx.Expect(job.Stage).ToEqual("build")
			ctx.Expect(job.Round).ToEqual(3)
			ctx.Expect(job.Reviewers).ToEqual(secCode)

			step(result("builder", 2, domain.BuildResult{Status: domain.BuildReady}))
			step(domain.GatePassed{Job: jobID, At: at(3)})
			ctx.Expect(job.Stage).ToEqual("review")

			evs, err := decide.Decide(job, pass("reviewer-ux", 4), p)
			ctx.Expect(err).To(specs.MatchError(decide.ErrIllegalTransition))
			ctx.Expect(evs).To(specs.BeEmpty())

			step(pass("reviewer-code", 5))
			step(pass("reviewer-security", 6))
			ctx.Expect(job.Stage).ToEqual("approve")
		})

		s.It("runs every reviewer in round 2 after a failing gate in round 1", func(ctx *specs.Context) {
			p := threeReviewers()
			job := atGate(1, nil)
			for _, ev := range []domain.Event{gateFailed(1), result("builder", 2, domain.BuildResult{Status: domain.BuildReady}), domain.GatePassed{Job: jobID, At: at(3)}, pass("reviewer-security", 4), pass("reviewer-ux", 5)} {
				evs, err := decide.Decide(job, ev, p)
				ctx.Expect(err).To(specs.BeNil())
				job = decide.Apply(job, evs)
			}
			ctx.Expect(job.Round).ToEqual(2)
			ctx.Expect(job.Stage).ToEqual("review")

			evs, err := decide.Decide(job, pass("reviewer-code", 6), p)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(evs).ToEqual([]domain.Event{passed("reviewer-code", 6), entered("approve", 6)})
		})
	})
}
