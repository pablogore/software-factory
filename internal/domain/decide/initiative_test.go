package decide_test

import (
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/domain/decide"
)

const initiativeID domain.InitiativeID = "017-e01-6-decide-maneja"

// sliceJob is the first of a three-slice initiative's jobs, asking to merge.
func sliceJob() domain.Job {
	job := newJob()
	job.Stage, job.Initiative = domain.StageMerge, initiativeID
	return job
}

// threeSlices is an initiative running its first slice, jobID, of three.
func threeSlices(checkpoint domain.Checkpoint) domain.Initiative {
	return domain.Initiative{ID: initiativeID, Checkpoint: checkpoint, Stage: domain.InitiativeActive, Job: jobID}
}

func continued(minute int) domain.ReplanSubmitted {
	return domain.ReplanSubmitted{Job: jobID, Initiative: initiativeID, Status: domain.ReplanContinue, At: at(minute)}
}

func nextSlice(minute int) domain.NextSliceRequested {
	return domain.NextSliceRequested{Job: jobID, Initiative: initiativeID, At: at(minute)}
}

// mergeSlice merges the first slice and returns the job and the initiative
// after it, both waiting for the replan.
func mergeSlice(ctx *specs.Context, in domain.Initiative) (domain.Job, domain.Initiative) {
	job := sliceJob()
	evs, err := decide.Decide(job, merged(1), defaultPipeline())
	ctx.Expect(err).To(specs.BeNil())
	return decide.Apply(job, evs), decide.ApplyInitiative(in, evs)
}

// replan feeds ev to the initiative and returns it after the events.
func replan(ctx *specs.Context, in domain.Initiative, ev domain.Event, want []domain.Event) domain.Initiative {
	evs, err := decide.Initiative(in, ev)
	ctx.Expect(err).To(specs.BeNil())
	ctx.Expect(evs).ToEqual(want)
	return decide.ApplyInitiative(in, evs)
}

type initiativeRow struct {
	name string
	run  func(ctx *specs.Context)
}

func TestInitiative(t *testing.T) {
	specs.Describe(t, "Decide on an initiative", func(s *specs.Spec) {
		rows := []initiativeRow{
			{"a merged slice of a three-slice initiative ends done and asks for a replan", func(ctx *specs.Context) {
				job := sliceJob()

				evs, err := decide.Decide(job, merged(1), defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual([]domain.Event{
					entered(domain.StageDone, 1),
					domain.ReplanRequested{Job: jobID, Initiative: initiativeID, At: at(1)},
				})
				ctx.Expect(decide.Apply(job, evs).Stage).ToEqual(domain.StageDone)
				in := decide.ApplyInitiative(threeSlices(domain.CheckpointEverySlice), evs)
				ctx.Expect(in.Stage).ToEqual(domain.InitiativeReplan)
				ctx.Expect(in.Job).ToEqual(jobID)
			}},
			{"a replan CONTINUE with checkpoint every-slice waits for a person whose OK starts the next slice", func(ctx *specs.Context) {
				job, in := mergeSlice(ctx, threeSlices(domain.CheckpointEverySlice))

				in = replan(ctx, in, continued(2), []domain.Event{
					domain.CheckpointReached{Job: jobID, Initiative: initiativeID, At: at(2)},
				})
				ctx.Expect(in.Stage).ToEqual(domain.InitiativeCheckpoint)

				in = replan(ctx, in, domain.HumanApproved{Job: jobID, By: "pablogore", At: at(3)}, []domain.Event{nextSlice(3)})
				ctx.Expect(in.Stage).ToEqual(domain.InitiativeActive)
				ctx.Expect(job.Stage).ToEqual(domain.StageDone)
			}},
			{"a replan CONTINUE with checkpoint auto starts the next slice with no person", func(ctx *specs.Context) {
				job, in := mergeSlice(ctx, threeSlices(domain.CheckpointAuto))

				in = replan(ctx, in, continued(2), []domain.Event{nextSlice(2)})

				ctx.Expect(in.Stage).ToEqual(domain.InitiativeActive)
				ctx.Expect(job.Stage).ToEqual(domain.StageDone)
			}},
			{"a job outside any initiative ends done after its merge with no replan", func(ctx *specs.Context) {
				job := newJob()
				job.Stage = domain.StageMerge

				evs, err := decide.Decide(job, merged(1), defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual([]domain.Event{entered(domain.StageDone, 1)})
				ctx.Expect(decide.Apply(job, evs).Stage).ToEqual(domain.StageDone)
			}},
		}
		specs.Table(s, rows, func(r initiativeRow) string { return r.name }, func(ctx *specs.Context, r initiativeRow) {
			r.run(ctx)
		})

		s.It("waits for a person when the initiative has no checkpoint set, the factory's every-slice", func(ctx *specs.Context) {
			_, in := mergeSlice(ctx, threeSlices(""))

			in = replan(ctx, in, continued(2), []domain.Event{
				domain.CheckpointReached{Job: jobID, Initiative: initiativeID, At: at(2)},
			})

			ctx.Expect(in.Stage).ToEqual(domain.InitiativeCheckpoint)
		})

		s.It("leaves another initiative alone when a slice merges", func(ctx *specs.Context) {
			other := threeSlices(domain.CheckpointAuto)
			other.ID, other.Job = "018-other", "018-other-s1"

			evs, err := decide.Decide(sliceJob(), merged(1), defaultPipeline())

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(decide.ApplyInitiative(other, evs)).ToEqual(other)
		})

		s.When("the event does not fit", func(s *specs.Spec) {
			replanning := func() domain.Initiative {
				in := threeSlices(domain.CheckpointEverySlice)
				in.Stage = domain.InitiativeReplan
				return in
			}
			waiting := func() domain.Initiative {
				in := replanning()
				in.Stage = domain.InitiativeCheckpoint
				return in
			}
			type refusedRow struct {
				name string
				in   domain.Initiative
				ev   domain.Event
				want error
			}
			otherInitiative := continued(2)
			otherInitiative.Initiative = "018-other"
			otherJob := continued(2)
			otherJob.Job = "018-other-s1"
			done, human, unknown := continued(2), continued(2), continued(2)
			done.Status, human.Status, unknown.Status = domain.ReplanDone, domain.ReplanNeedsHuman, "MAYBE"
			badCheckpoint := replanning()
			badCheckpoint.Checkpoint = "weekly"
			approval := domain.HumanApproved{Job: jobID, By: "pablogore", At: at(2)}
			rows := []refusedRow{
				{"refuses a replan for another initiative", replanning(), otherInitiative, decide.ErrOtherInitiative},
				{"refuses a replan about another slice job", replanning(), otherJob, decide.ErrOtherInitiative},
				{"refuses a replan while a slice runs", threeSlices(domain.CheckpointAuto), continued(2), decide.ErrIllegalTransition},
				{"refuses a second replan at the checkpoint", waiting(), continued(2), decide.ErrIllegalTransition},
				{"refuses a person's OK before the replan", replanning(), approval, decide.ErrIllegalTransition},
				{"refuses a person's OK while a slice runs", threeSlices(domain.CheckpointEverySlice), approval, decide.ErrIllegalTransition},
				{"refuses a missing event", replanning(), nil, decide.ErrIllegalTransition},
				{"leaves a replan DONE unsupported", replanning(), done, decide.ErrUnsupported},
				{"leaves a replan NEEDS-HUMAN unsupported", replanning(), human, decide.ErrUnsupported},
				{"rejects an unknown replan status", replanning(), unknown, domain.ErrInvalidValue},
				{"rejects an unknown checkpoint", badCheckpoint, continued(2), domain.ErrInvalidValue},
			}
			specs.Table(s, rows, func(r refusedRow) string { return r.name }, func(ctx *specs.Context, r refusedRow) {
				evs, err := decide.Initiative(r.in, r.ev)

				ctx.Expect(err).To(specs.MatchError(r.want))
				ctx.Expect(evs).To(specs.BeEmpty())
			})
		})
	})
}
