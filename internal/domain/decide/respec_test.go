package decide_test

import (
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/domain/decide"
)

type needsHumanRow struct {
	name     string
	rejected int
}

// specEscalated is a job that specgate's third rejection put in needs-human.
func specEscalated() domain.Job {
	job := newJob()
	job.Stage, job.SpecRejections, job.SpecEscalated = domain.StageNeedsHuman, 3, true
	job.Reason = "spec rejected by specgate 3 times"
	return job
}

func TestDecideFeedbackAfterSpecgate(t *testing.T) {
	specs.Describe(t, "Decide on a person's feedback in needs-human", func(s *specs.Spec) {
		s.When("specgate's third rejection put the job in needs-human", func(s *specs.Spec) {
			s.It("goes back to spec with the feedback and the rejection count at 0", func(ctx *specs.Context) {
				job := specEscalated()

				evs, err := decide.Decide(job, message(domain.AuthorPerson, true, 1), defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual([]domain.Event{
					domain.ReworkRequested{Job: jobID, By: "pablogore", Text: "rename the flag", At: at(1)},
					rejected(0, 1),
					entered("spec", 1),
				})
				after := decide.Apply(job, evs)
				ctx.Expect(after.Stage).ToEqual("spec")
				ctx.Expect(after.SpecRejections).ToEqual(0)
				ctx.Expect(after.Round).ToEqual(job.Round)
			})

			rows := []messageRow{
				{"ignores a person who is not allowed", message(domain.AuthorPerson, false, 1)},
				{"ignores a bot even when marked allowed", message(domain.AuthorBot, true, 1)},
				{"ignores CI even when marked allowed", message(domain.AuthorCI, true, 1)},
				{"ignores an author of unknown kind", message("", true, 1)},
			}
			specs.Table(s, rows, func(r messageRow) string { return r.name }, func(ctx *specs.Context, r messageRow) {
				job := specEscalated()

				evs, err := decide.Decide(job, r.ev, defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).To(specs.BeEmpty())
				ctx.Expect(decide.Apply(job, evs)).ToEqual(specEscalated())
			})

			s.It("refuses the feedback when the pipeline has no spec stage", func(ctx *specs.Context) {
				p := defaultPipeline()
				p.Stages = p.Stages[2:]

				evs, err := decide.Decide(specEscalated(), message(domain.AuthorPerson, true, 1), p)

				ctx.Expect(err).To(specs.Not(specs.BeNil()))
				ctx.Expect(evs).To(specs.BeEmpty())
			})

			s.It("lands in spec at 0 after three rejections and feedback, then gets three more specgate tries", func(ctx *specs.Context) {
				p := defaultPipeline()
				job := newJob()
				job.Stage = "spec"
				step := func(ev domain.Event) {
					evs, err := decide.Decide(job, ev, p)
					ctx.Expect(err).To(specs.BeNil())
					job = decide.Apply(job, evs)
				}
				rejectThrice := func(from int) {
					for k := 0; k < 3; k++ {
						step(result("spec-writer", from+2*k, domain.SpecResult{Title: "S4", Spec: "# S4"}))
						step(specgateFailed(from + 2*k + 1))
					}
				}

				rejectThrice(1)
				ctx.Expect(job.Stage).ToEqual(domain.StageNeedsHuman)
				ctx.Expect(job.SpecRejections).ToEqual(3)

				step(message(domain.AuthorPerson, true, 7))
				ctx.Expect(job.Stage).ToEqual("spec")
				ctx.Expect(job.SpecRejections).ToEqual(0)

				for k := 1; k <= 2; k++ {
					step(result("spec-writer", 6+2*k, domain.SpecResult{Title: "S4", Spec: "# S4"}))
					step(specgateFailed(7 + 2*k))
					ctx.Expect(job.Stage).ToEqual("spec")
					ctx.Expect(job.SpecRejections).ToEqual(k)
				}
				step(result("spec-writer", 12, domain.SpecResult{Title: "S4", Spec: "# S4"}))
				step(specgateFailed(13))

				ctx.Expect(job.Stage).ToEqual(domain.StageNeedsHuman)
				ctx.Expect(job.SpecRejections).ToEqual(3)
				ctx.Expect(job.Round).ToEqual(1)
			})
		})

		s.When("the job is in needs-human for any other reason", func(s *specs.Spec) {
			rows := []needsHumanRow{
				{"goes back to build when specgate never rejected the spec", 0},
				{"goes back to build when specgate rejected the spec fewer than three times", 2},
			}
			specs.Table(s, rows, func(r needsHumanRow) string { return r.name }, func(ctx *specs.Context, r needsHumanRow) {
				job := newJob()
				job.Stage, job.Round, job.SpecRejections = domain.StageNeedsHuman, 3, r.rejected

				evs, err := decide.Decide(job, message(domain.AuthorPerson, true, 1), defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual([]domain.Event{
					domain.ReworkRequested{Job: jobID, By: "pablogore", Text: "rename the flag", At: at(1)},
					entered("build", 1),
				})
				after := decide.Apply(job, evs)
				ctx.Expect(after.Stage).ToEqual("build")
				ctx.Expect(after.Round).ToEqual(3)
				ctx.Expect(after.SpecRejections).ToEqual(r.rejected)
			})

			s.It("sends a reviewer escalation's feedback to build, not spec", func(ctx *specs.Context) {
				job := atSpecgate(2)
				job.Stage, job.Round, job.Reviewers = "review", 3, []string{"reviewer-code"}

				evs, err := decide.Decide(job, changes("reviewer-code", 1), defaultPipeline())
				ctx.Expect(err).To(specs.BeNil())
				job = decide.Apply(job, evs)
				ctx.Expect(job.Stage).ToEqual(domain.StageNeedsHuman)

				evs, err = decide.Decide(job, message(domain.AuthorPerson, true, 2), defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(decide.Apply(job, evs).Stage).ToEqual("build")
			})
		})
	})
}
