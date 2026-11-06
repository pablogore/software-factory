package decide_test

import (
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/domain/decide"
)

func exceptionAsked(minute int) domain.SpecgateFailed {
	return domain.SpecgateFailed{Job: jobID, Exception: true, At: at(minute)}
}

func waitingException(minute int) domain.NeedsHuman {
	return domain.NeedsHuman{Job: jobID, Reason: "spec asks for a length exception", SpecException: true, At: at(minute)}
}

// waitingJob is a job whose spec, rejected once before, asks for a length
// exception and waits for a person.
func waitingJob() domain.Job {
	job := atSpecgate(1)
	job.Stage, job.SpecException = domain.StageNeedsHuman, true
	return job
}

type exceptionRow struct {
	name     string
	rejected int
}

func TestDecideSpecException(t *testing.T) {
	specs.Describe(t, "Decide on a spec's length exception", func(s *specs.Spec) {
		s.When("specgate reports that the spec asks for an exception", func(s *specs.Spec) {
			rows := []exceptionRow{
				{"waits for a person when specgate never rejected the spec", 0},
				{"waits for a person without counting a second rejection", 1},
				{"waits for a person even after two rejections", 2},
			}
			specs.Table(s, rows, func(r exceptionRow) string { return r.name }, func(ctx *specs.Context, r exceptionRow) {
				job := atSpecgate(r.rejected)

				evs, err := decide.Decide(job, exceptionAsked(1), defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual([]domain.Event{waitingException(1)})
				after := decide.Apply(job, evs)
				ctx.Expect(after.Stage).ToEqual(domain.StageNeedsHuman)
				ctx.Expect(after.SpecException).To(specs.BeTrue())
				ctx.Expect(after.SpecRejections).ToEqual(r.rejected)
				ctx.Expect(after.Round).ToEqual(job.Round)
			})
		})

		s.When("the job waits on the exception", func(s *specs.Spec) {
			s.It("goes on to build on a person's OK, with the spec and the count as they were", func(ctx *specs.Context) {
				job := waitingJob()

				evs, err := decide.Decide(job, domain.HumanApproved{Job: jobID, By: "pablogore", At: at(2)}, defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual([]domain.Event{entered("build", 2)})
				after := decide.Apply(job, evs)
				ctx.Expect(after.Stage).ToEqual("build")
				ctx.Expect(after.SpecException).To(specs.BeFalse())
				ctx.Expect(after.SpecRejections).ToEqual(1)
				ctx.Expect(after.Round).ToEqual(job.Round)
			})

			s.It("goes back to spec with a person's feedback and the count unchanged", func(ctx *specs.Context) {
				job := waitingJob()

				evs, err := decide.Decide(job, message(domain.AuthorPerson, true, 2), defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual([]domain.Event{
					domain.ReworkRequested{Job: jobID, By: "pablogore", Text: "rename the flag", At: at(2)},
					entered("spec", 2),
				})
				after := decide.Apply(job, evs)
				ctx.Expect(after.Stage).ToEqual("spec")
				ctx.Expect(after.SpecException).To(specs.BeFalse())
				ctx.Expect(after.SpecRejections).ToEqual(1)
				ctx.Expect(after.Round).ToEqual(job.Round)
			})

			ignoredRows := []messageRow{
				{"ignores a person who is not allowed", message(domain.AuthorPerson, false, 2)},
				{"ignores a bot even when marked allowed", message(domain.AuthorBot, true, 2)},
				{"ignores CI even when marked allowed", message(domain.AuthorCI, true, 2)},
				{"ignores an author of unknown kind", message("", true, 2)},
			}
			specs.Table(s, ignoredRows, func(r messageRow) string { return r.name }, func(ctx *specs.Context, r messageRow) {
				job := waitingJob()

				evs, err := decide.Decide(job, r.ev, defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).To(specs.BeEmpty())
				ctx.Expect(decide.Apply(job, evs)).ToEqual(waitingJob())
			})

			refused := []step{
				{"refuses a specgate pass", domain.SpecgatePassed{Job: jobID, At: at(2)}, nil},
				{"refuses another specgate rejection", specgateFailed(2), nil},
				{"refuses another exception", exceptionAsked(2), nil},
				{"refuses a new spec", result("spec-writer", 2, domain.SpecResult{Title: "S5", Spec: "# S5"}), nil},
				{"refuses a build", result("builder", 2, domain.BuildResult{Status: domain.BuildReady}), nil},
				{"refuses a gate pass", domain.GatePassed{Job: jobID, At: at(2)}, nil},
				{"refuses a gate failure", domain.GateFailed{Job: jobID, At: at(2)}, nil},
				{"refuses a review", pass("reviewer-code", 2), nil},
				{"refuses an approval result", result("approver", 2, domain.ApproveResult{Decision: domain.ApproveApprove}), nil},
				{"refuses a merge", merged(2), nil},
			}
			specs.Table(s, refused, func(r step) string { return r.name }, func(ctx *specs.Context, r step) {
				job := waitingJob()

				evs, err := decide.Decide(job, r.ev, defaultPipeline())

				ctx.Expect(err).To(specs.MatchError(decide.ErrIllegalTransition))
				ctx.Expect(evs).To(specs.BeEmpty())
				ctx.Expect(decide.Apply(job, evs)).ToEqual(waitingJob())
			})

			s.It("refuses the OK when the pipeline has no spec stage", func(ctx *specs.Context) {
				p := defaultPipeline()
				p.Stages = p.Stages[2:]

				evs, err := decide.Decide(waitingJob(), domain.HumanApproved{Job: jobID, By: "pablogore", At: at(2)}, p)

				ctx.Expect(err).To(specs.MatchError(decide.ErrIllegalTransition))
				ctx.Expect(evs).To(specs.BeEmpty())
			})

			s.It("refuses the feedback when the pipeline has no spec stage", func(ctx *specs.Context) {
				p := defaultPipeline()
				p.Stages = p.Stages[2:]

				evs, err := decide.Decide(waitingJob(), message(domain.AuthorPerson, true, 2), p)

				ctx.Expect(err).To(specs.Not(specs.BeNil()))
				ctx.Expect(evs).To(specs.BeEmpty())
			})
		})

		s.It("asks again after feedback when the rewritten spec still asks, then goes on on the OK", func(ctx *specs.Context) {
			p := defaultPipeline()
			job := newJob()
			job.Stage = "spec"
			step := func(ev domain.Event) {
				evs, err := decide.Decide(job, ev, p)
				ctx.Expect(err).To(specs.BeNil())
				job = decide.Apply(job, evs)
			}

			step(result("spec-writer", 1, domain.SpecResult{Title: "S5", Spec: "# S5"}))
			step(exceptionAsked(2))
			ctx.Expect(job.Stage).ToEqual(domain.StageNeedsHuman)

			step(message(domain.AuthorPerson, true, 3))
			ctx.Expect(job.Stage).ToEqual("spec")
			step(result("spec-writer", 4, domain.SpecResult{Title: "S5", Spec: "# S5"}))
			step(exceptionAsked(5))
			ctx.Expect(job.Stage).ToEqual(domain.StageNeedsHuman)
			ctx.Expect(job.SpecException).To(specs.BeTrue())

			step(domain.HumanApproved{Job: jobID, By: "pablogore", At: at(6)})

			ctx.Expect(job.Stage).ToEqual("build")
			ctx.Expect(job.SpecRejections).ToEqual(0)
			ctx.Expect(job.Round).ToEqual(1)
		})

		s.It("still requests the merge on a person's OK in needs-human for another reason", func(ctx *specs.Context) {
			job := atSpecgate(1)
			job.Stage = domain.StageNeedsHuman

			evs, err := decide.Decide(job, domain.HumanApproved{Job: jobID, By: "pablogore", At: at(2)}, defaultPipeline())

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(evs).ToEqual([]domain.Event{domain.MergeRequested{Job: jobID, At: at(2)}})
		})
	})
}
