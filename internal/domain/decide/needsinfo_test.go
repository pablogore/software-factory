package decide_test

import (
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/domain/decide"
)

func needsInfo() domain.ResultSubmitted {
	return result("intake", 1, domain.IntakeResult{Decision: domain.IntakeNeedsInfo})
}

// askingJob is a job waiting for an answer to intake's question.
func askingJob() domain.Job {
	job := newJob()
	job.Stage, job.InfoFrom, job.WaitSeq = domain.StageNeedsInfo, "intake", 1
	return job
}

func TestNeedsInfo(t *testing.T) {
	specs.Describe(t, "Decide on a question to a person", func(s *specs.Spec) {
		s.It("moves an intake NEEDS_INFO to needs-info and records intake as the asker", func(ctx *specs.Context) {
			job := newJob()

			evs, err := decide.Decide(job, needsInfo(), defaultPipeline())

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(evs).ToEqual([]domain.Event{domain.NeedsInfo{Job: jobID, From: "intake", At: at(1)}})
			after := decide.Apply(job, evs)
			ctx.Expect(after.Stage).ToEqual(domain.StageNeedsInfo)
			ctx.Expect(after.InfoFrom).ToEqual("intake")
			ctx.Expect(after.Round).ToEqual(1)
		})

		s.When("an allowed person answers", func(s *specs.Spec) {
			s.It("sends the job back to the stage that asked with the answer as input", func(ctx *specs.Context) {
				job := askingJob()

				evs, err := decide.Decide(job, message(domain.AuthorPerson, true, 2), defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual([]domain.Event{
					domain.InfoAnswered{Job: jobID, By: "pablogore", Text: "rename the flag", At: at(2)},
					entered("intake", 2),
				})
			})

			s.It("follows the stage recorded on the job, not always intake", func(ctx *specs.Context) {
				job := askingJob()
				job.InfoFrom = "spec"

				evs, err := decide.Decide(job, message(domain.AuthorPerson, true, 2), defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual([]domain.Event{
					domain.InfoAnswered{Job: jobID, By: "pablogore", Text: "rename the flag", At: at(2)},
					entered("spec", 2),
				})
			})

			s.It("clears the recorded stage when the job leaves needs-info", func(ctx *specs.Context) {
				job := newJob()
				for _, ev := range []domain.Event{needsInfo(), message(domain.AuthorPerson, true, 2)} {
					evs, err := decide.Decide(job, ev, defaultPipeline())
					ctx.Expect(err).To(specs.BeNil())
					job = decide.Apply(job, evs)
				}

				ctx.Expect(job.Stage).ToEqual("intake")
				ctx.Expect(job.InfoFrom).ToEqual("")
				ctx.Expect(job.Round).ToEqual(1)
			})

			s.It("refuses an answer when the recorded stage is not a pipeline stage", func(ctx *specs.Context) {
				for _, from := range []string{"", domain.StageGate, domain.StageDone, "deploy"} {
					job := askingJob()
					job.InfoFrom = from

					evs, err := decide.Decide(job, message(domain.AuthorPerson, true, 2), defaultPipeline())

					ctx.Expect(err).To(specs.MatchError(domain.ErrInvalidValue))
					ctx.Expect(evs).To(specs.BeEmpty())
				}
			})
		})

		s.When("someone else writes while the job waits", func(s *specs.Spec) {
			rows := []messageRow{
				{"ignores a person who is not allowed", message(domain.AuthorPerson, false, 2)},
				{"ignores a bot even when marked allowed", message(domain.AuthorBot, true, 2)},
				{"ignores CI even when marked allowed", message(domain.AuthorCI, true, 2)},
				{"ignores an author of unknown kind", message("", true, 2)},
			}
			specs.Table(s, rows, func(r messageRow) string { return r.name }, func(ctx *specs.Context, r messageRow) {
				job := askingJob()

				evs, err := decide.Decide(job, r.ev, defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).To(specs.BeEmpty())
				ctx.Expect(decide.Apply(job, evs)).ToEqual(askingJob())
			})
		})

		s.When("leaving needs-info another way", func(s *specs.Spec) {
			s.It("clears the recorded stage when a person stops the job", func(ctx *specs.Context) {
				job := askingJob()

				evs, err := decide.Decide(job, stop("pablogore", 2), defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				after := decide.Apply(job, evs)
				ctx.Expect(after.Stage).ToEqual(domain.StageStopped)
				ctx.Expect(after.InfoFrom).ToEqual("")
			})

			rows := []eventRow{
				{"refuses a result", needsInfo()},
				{"refuses a person's approval", domain.HumanApproved{Job: jobID, By: "pablogore", WaitSeq: 1, At: at(2)}},
				{"refuses a gate OK", domain.GatePassed{Job: jobID, Run: passingRun, At: at(2)}},
			}
			specs.Table(s, rows, func(r eventRow) string { return r.name }, func(ctx *specs.Context, r eventRow) {
				evs, err := decide.Decide(askingJob(), r.ev, defaultPipeline())

				ctx.Expect(err).To(specs.MatchError(decide.ErrIllegalTransition))
				ctx.Expect(evs).To(specs.BeEmpty())
			})
		})
	})
}
