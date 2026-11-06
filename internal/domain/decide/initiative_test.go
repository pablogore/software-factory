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

func doneReplan(minute int) domain.ReplanSubmitted {
	e := continued(minute)
	e.Status = domain.ReplanDone
	return e
}

func needsHumanReplan(minute int, reason string) domain.ReplanSubmitted {
	e := continued(minute)
	e.Status, e.Reason = domain.ReplanNeedsHuman, reason
	return e
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

				in = replan(ctx, in, domain.HumanApproved{Job: jobID, By: "pablogore", WaitSeq: 1, At: at(3)}, []domain.Event{nextSlice(3)})
				ctx.Expect(in.Stage).ToEqual(domain.InitiativeActive)
				ctx.Expect(job.Stage).ToEqual(domain.StageDone)
			}},
			{"a replan CONTINUE with checkpoint auto starts the next slice with no person", func(ctx *specs.Context) {
				job, in := mergeSlice(ctx, threeSlices(domain.CheckpointAuto))

				in = replan(ctx, in, continued(2), []domain.Event{nextSlice(2)})

				ctx.Expect(in.Stage).ToEqual(domain.InitiativeActive)
				ctx.Expect(job.Stage).ToEqual(domain.StageDone)
			}},
			{"a replan DONE closes the initiative: no next slice starts and nobody is asked for an OK", func(ctx *specs.Context) {
				for _, checkpoint := range []domain.Checkpoint{domain.CheckpointEverySlice, domain.CheckpointAuto} {
					job, in := mergeSlice(ctx, threeSlices(checkpoint))

					in = replan(ctx, in, doneReplan(2), []domain.Event{
						domain.InitiativeEnded{Job: jobID, Initiative: initiativeID, At: at(2)},
					})

					ctx.Expect(in.Stage).ToEqual(domain.InitiativeClosed)
					ctx.Expect(job.Stage).ToEqual(domain.StageDone)
				}
			}},
			{"a replan NEEDS-HUMAN puts the initiative in needs-human, keeping the replan's reason, and starts no slice", func(ctx *specs.Context) {
				const reason = "S3 needs a decision on the API shape before it can be cut"
				for _, checkpoint := range []domain.Checkpoint{domain.CheckpointEverySlice, domain.CheckpointAuto} {
					_, in := mergeSlice(ctx, threeSlices(checkpoint))

					in = replan(ctx, in, needsHumanReplan(2, reason), []domain.Event{
						domain.InitiativeNeedsHumanRaised{Job: jobID, Initiative: initiativeID, Reason: reason, At: at(2)},
					})

					ctx.Expect(in.Stage).ToEqual(domain.InitiativeNeedsHuman)
					ctx.Expect(in.Reason).ToEqual(reason)
				}
			}},
			{"a closed initiative stays closed: a later replan or a person's OK starts no slice", func(ctx *specs.Context) {
				// It waited once before, so the OK names that wait and only
				// the closed stage refuses it.
				start := threeSlices(domain.CheckpointAuto)
				start.WaitSeq = 1
				_, in := mergeSlice(ctx, start)
				closed := replan(ctx, in, doneReplan(2), []domain.Event{
					domain.InitiativeEnded{Job: jobID, Initiative: initiativeID, At: at(2)},
				})

				for _, ev := range []domain.Event{
					continued(3),
					doneReplan(3),
					domain.HumanApproved{Job: jobID, By: "pablogore", WaitSeq: 1, At: at(3)},
				} {
					evs, err := decide.Initiative(closed, ev)
					ctx.Expect(err).To(specs.MatchError(decide.ErrIllegalTransition))
					ctx.Expect(evs).To(specs.BeEmpty())
				}

				// Not even a fold that would restart it reopens a closed initiative.
				reopening := []domain.Event{
					domain.ReplanRequested{Job: jobID, Initiative: initiativeID, At: at(4)},
					nextSlice(4),
				}
				ctx.Expect(decide.ApplyInitiative(closed, reopening)).ToEqual(closed)
			}},
			{"a replan CONTINUE still behaves as in S1, for both checkpoints", func(ctx *specs.Context) {
				_, in := mergeSlice(ctx, threeSlices(domain.CheckpointEverySlice))
				in = replan(ctx, in, continued(2), []domain.Event{
					domain.CheckpointReached{Job: jobID, Initiative: initiativeID, At: at(2)},
				})
				ctx.Expect(in.Stage).ToEqual(domain.InitiativeCheckpoint)

				_, in = mergeSlice(ctx, threeSlices(domain.CheckpointAuto))
				in = replan(ctx, in, continued(2), []domain.Event{nextSlice(2)})
				ctx.Expect(in.Stage).ToEqual(domain.InitiativeActive)
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
			// Each has had one wait, so a person's OK names it and only the
			// stage refuses it.
			replanning := func() domain.Initiative {
				in := threeSlices(domain.CheckpointEverySlice)
				in.Stage, in.WaitSeq = domain.InitiativeReplan, 1
				return in
			}
			running := func() domain.Initiative {
				in := threeSlices(domain.CheckpointEverySlice)
				in.WaitSeq = 1
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
			unknown := continued(2)
			unknown.Status = "MAYBE"
			badCheckpoint := replanning()
			badCheckpoint.Checkpoint = "weekly"
			approval := domain.HumanApproved{Job: jobID, By: "pablogore", WaitSeq: 1, At: at(2)}
			rows := []refusedRow{
				{"refuses a replan for another initiative", replanning(), otherInitiative, decide.ErrOtherInitiative},
				{"refuses a replan about another slice job", replanning(), otherJob, decide.ErrOtherInitiative},
				{"refuses a replan while a slice runs", threeSlices(domain.CheckpointAuto), continued(2), decide.ErrIllegalTransition},
				{"refuses a second replan at the checkpoint", waiting(), continued(2), decide.ErrIllegalTransition},
				{"refuses a person's OK before the replan", replanning(), approval, decide.ErrIllegalTransition},
				{"refuses a person's OK while a slice runs", running(), approval, decide.ErrIllegalTransition},
				{"refuses a missing event", replanning(), nil, decide.ErrIllegalTransition},
				{"refuses a replan DONE while a slice runs", threeSlices(domain.CheckpointAuto), doneReplan(2), decide.ErrIllegalTransition},
				{"refuses a replan NEEDS-HUMAN at the checkpoint", waiting(), needsHumanReplan(2, "why"), decide.ErrIllegalTransition},
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
