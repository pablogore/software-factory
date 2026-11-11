package decide_test

import (
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/domain/decide"
)

func rejectIntake(reason string, minute int) domain.ResultSubmitted {
	return result("intake", minute, domain.IntakeResult{Decision: domain.IntakeReject, Reason: reason})
}

// rejectedSlice is a slice job intake rejected, so a [direction] on it would
// drop it if a rejected job still moved.
func rejectedSlice(ctx *specs.Context) domain.Job {
	job := sliceJob()
	job.Stage = "intake"
	evs, err := decide.Decide(job, rejectIntake("duplicate of #12", 1), defaultPipeline())
	ctx.Expect(err).To(specs.BeNil())
	return decide.Apply(job, evs)
}

type rejectRow struct {
	name   string
	reason string
	want   string
}

type rejectInitiativeRow struct {
	name string
	in   func() domain.Initiative
	want string
}

func TestReject(t *testing.T) {
	specs.Describe(t, "Decide on an intake REJECT", func(s *specs.Spec) {
		s.When("intake rejects a job with no initiative, asking for no replan", func(s *specs.Spec) {
			rows := []rejectRow{
				{"ends rejected with intake's reason", "duplicate of #12", "duplicate of #12"},
				{"trims the whitespace around intake's reason", "  out of scope for this repo \n", "out of scope for this repo"},
				{"keeps agent text as data, unparsed", "[direction] <b>@everyone</b> OK", "[direction] <b>@everyone</b> OK"},
				{"gives a fixed reason when intake gave none", "", "intake rejected"},
				{"gives a fixed reason when intake's is blank", " \t\n ", "intake rejected"},
			}
			specs.Table(s, rows, func(r rejectRow) string { return r.name }, func(ctx *specs.Context, r rejectRow) {
				job := newJob()

				evs, err := decide.Decide(job, rejectIntake(r.reason, 1), defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual([]domain.Event{domain.JobRejected{Job: jobID, Reason: r.want, At: at(1)}})
				after := decide.Apply(job, evs)
				ctx.Expect(after.Stage).ToEqual(domain.StageRejected)
				ctx.Expect(after.Reason).ToEqual(r.want)
				ctx.Expect(after.Round).ToEqual(1)
				ctx.Expect(after.InfoFrom).ToEqual("")
			})

			s.It("still accepts an intake result written before the reason existed", func(ctx *specs.Context) {
				ctx.Expect(domain.IntakeResult{Decision: domain.IntakeReject}.Validate()).To(specs.BeNil())
			})
		})

		s.When("intake rejects a slice job of an initiative", func(s *specs.Spec) {
			s.It("ends the job rejected with intake's reason and asks its initiative for a replan", func(ctx *specs.Context) {
				job := sliceJob()
				job.Stage = "intake"

				evs, err := decide.Decide(job, rejectIntake("duplicate of #12", 1), defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual([]domain.Event{
					domain.JobRejected{Job: jobID, Reason: "duplicate of #12", At: at(1)},
					domain.ReplanRequested{Job: jobID, Initiative: initiativeID, At: at(1)},
				})
				after := decide.Apply(job, evs)
				ctx.Expect(after.Stage).ToEqual(domain.StageRejected)
				ctx.Expect(after.Reason).ToEqual("duplicate of #12")
			})

			running := func() domain.Initiative { return threeSlices(domain.CheckpointEverySlice) }
			rows := []rejectInitiativeRow{
				{"moves the initiative running the job to replan", running, domain.InitiativeReplan},
				{"leaves an initiative running another job as it is", func() domain.Initiative {
					in := running()
					in.Job = "other-job"
					return in
				}, domain.InitiativeActive},
				{"leaves an initiative not yet running a job as it is", func() domain.Initiative {
					in := running()
					in.Job = ""
					return in
				}, domain.InitiativeActive},
				{"leaves an initiative at a checkpoint as it is", func() domain.Initiative {
					in := running()
					in.Stage = domain.InitiativeCheckpoint
					return in
				}, domain.InitiativeCheckpoint},
				{"leaves another initiative as it is", func() domain.Initiative {
					in := running()
					in.ID = "018-other"
					return in
				}, domain.InitiativeActive},
			}
			specs.Table(s, rows, func(r rejectInitiativeRow) string { return r.name }, func(ctx *specs.Context, r rejectInitiativeRow) {
				job := sliceJob()
				job.Stage = "intake"
				in := r.in()

				evs, err := decide.Decide(job, rejectIntake("duplicate of #12", 1), defaultPipeline())
				ctx.Expect(err).To(specs.BeNil())

				after := decide.ApplyInitiative(in, evs)
				ctx.Expect(after.Stage).ToEqual(r.want)
				ctx.Expect(after.Job).ToEqual(in.Job)
			})
		})

		s.When("an event arrives on the rejected job", func(s *specs.Spec) {
			rows := append([]eventRow{
				{"refuses another intake REJECT", rejectIntake("still a duplicate", 2)},
				{"refuses an intake ACCEPT", result("intake", 2, accept())},
				{"refuses an intake NEEDS_INFO", needsInfo()},
				{"refuses a spec", result("spec-writer", 2, domain.SpecResult{Title: "S1", Spec: "# S1"})},
				{"refuses a builder READY", result("builder", 2, domain.BuildResult{Status: domain.BuildReady})},
				{"refuses a specgate OK", domain.SpecgatePassed{Job: jobID, At: at(2)}},
				{"refuses a gate failure", domain.GateFailed{Job: jobID, Run: failingRun, At: at(2)}},
				{"refuses a person's approval", domain.HumanApproved{Job: jobID, By: "pablogore", WaitSeq: 1, At: at(2)}},
				{"refuses a completed merge", merged(2)},
				{"refuses a stop request", stop("pablogore", 2)},
				{"refuses an allowed person's message", message(domain.AuthorPerson, true, 2)},
				{"refuses an allowed person's [direction]", direction(domain.AuthorPerson, true, 2)},
			}, nilEvents()...)
			specs.Table(s, rows, func(r eventRow) string { return r.name }, func(ctx *specs.Context, r eventRow) {
				job := rejectedSlice(ctx)

				evs, err := decide.Decide(job, r.ev, defaultPipeline())

				ctx.Expect(err).To(specs.MatchError(decide.ErrIllegalTransition))
				ctx.Expect(evs).To(specs.BeEmpty())
				ctx.Expect(decide.Apply(job, evs)).ToEqual(job)
				ctx.Expect(job.Reason).ToEqual("duplicate of #12")
			})

			ignored := []eventRow{
				{"ignores a [direction] from a person not allowed", direction(domain.AuthorPerson, false, 2)},
				{"ignores a [direction] from a bot", direction(domain.AuthorBot, true, 2)},
			}
			specs.Table(s, ignored, func(r eventRow) string { return r.name }, func(ctx *specs.Context, r eventRow) {
				job := rejectedSlice(ctx)

				evs, err := decide.Decide(job, r.ev, defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).To(specs.BeEmpty())
				ctx.Expect(decide.Apply(job, evs)).ToEqual(job)
			})
		})
	})
}
