package decide_test

import (
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/domain/decide"
)

const directionText = "[direction] Better build S2 instead"

// direction is a [direction] comment as the event source marks it.
func direction(kind domain.AuthorKind, allowed bool, minute int) domain.MessageReceived {
	m := message(kind, allowed, minute)
	m.Direction, m.Text = true, directionText
	return m
}

// unmergedSlice is the initiative's first slice job, not merged, in stage,
// with one wait so far: the current one, or one it has left.
func unmergedSlice(stage string) domain.Job {
	job := sliceJob()
	job.Stage, job.WaitSeq = stage, 1
	if stage == domain.StageNeedsInfo {
		job.InfoFrom = "spec"
	}
	return job
}

func droppedBy(minute int) []domain.Event {
	return []domain.Event{
		domain.SliceDropped{Job: jobID, Initiative: initiativeID, By: "pablogore", Text: directionText, At: at(minute)},
		domain.ReplanRequested{Job: jobID, Initiative: initiativeID, At: at(minute)},
	}
}

// dropSlice drops the first slice in needs-human and returns the job and the
// initiative after it, the initiative waiting for the replan.
func dropSlice(ctx *specs.Context, in domain.Initiative) (domain.Job, domain.Initiative) {
	job := unmergedSlice(domain.StageNeedsHuman)
	evs, err := decide.Decide(job, direction(domain.AuthorPerson, true, 1), defaultPipeline())
	ctx.Expect(err).To(specs.BeNil())
	ctx.Expect(evs).ToEqual(droppedBy(1))
	return decide.Apply(job, evs), decide.ApplyInitiative(in, evs)
}

type directionRow struct {
	name string
	run  func(ctx *specs.Context)
}

func TestDirection(t *testing.T) {
	specs.Describe(t, "Decide on a [direction] comment", func(s *specs.Spec) {
		s.When("an allowed person sends it on an unmerged slice", func(s *specs.Spec) {
			stages := []string{"intake", "spec", domain.StageSpecgate, "build", domain.StageGate, "review", "approve",
				domain.StageNeedsHuman, domain.StageNeedsInfo}
			specs.Table(s, stages, func(st string) string { return "in " + st + " ends the job rejected and asks for a replan" }, func(ctx *specs.Context, st string) {
				job := unmergedSlice(st)
				job.Passed, job.Reviewers = []string{"reviewer-ux"}, []string{"reviewer-ux", "reviewer-code"}

				evs, err := decide.Decide(job, direction(domain.AuthorPerson, true, 1), defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual(droppedBy(1))
				after := decide.Apply(job, evs)
				ctx.Expect(after.Stage).ToEqual(domain.StageRejected)
				ctx.Expect(after.InfoFrom).ToEqual("")
				ctx.Expect(after.Reviewers).To(specs.BeEmpty())
				ctx.Expect(after.Passed).To(specs.BeEmpty())
				in := decide.ApplyInitiative(threeSlices(domain.CheckpointEverySlice), evs)
				ctx.Expect(in.Stage).ToEqual(domain.InitiativeReplan)
				ctx.Expect(in.Job).ToEqual(jobID)
			})
		})

		s.When("the replan follows the dropped slice", func(s *specs.Spec) {
			rows := []directionRow{
				{"a replan CONTINUE with checkpoint every-slice waits for a person and starts the next slice, never the dropped one", func(ctx *specs.Context) {
					job, in := dropSlice(ctx, threeSlices(domain.CheckpointEverySlice))

					in = replan(ctx, in, continued(2), []domain.Event{
						domain.CheckpointReached{Job: jobID, Initiative: initiativeID, At: at(2)},
					})
					in = replan(ctx, in, domain.HumanApproved{Job: jobID, By: "pablogore", WaitSeq: 1, At: at(3)}, []domain.Event{nextSlice(3)})

					ctx.Expect(in.Stage).ToEqual(domain.InitiativeActive)
					ctx.Expect(job.Stage).ToEqual(domain.StageRejected)
				}},
				{"a replan CONTINUE with checkpoint auto starts the next slice right away", func(ctx *specs.Context) {
					job, in := dropSlice(ctx, threeSlices(domain.CheckpointAuto))

					in = replan(ctx, in, continued(2), []domain.Event{nextSlice(2)})

					ctx.Expect(in.Stage).ToEqual(domain.InitiativeActive)
					ctx.Expect(job.Stage).ToEqual(domain.StageRejected)
				}},
				{"a replan DONE closes the initiative", func(ctx *specs.Context) {
					job, in := dropSlice(ctx, threeSlices(domain.CheckpointAuto))

					in = replan(ctx, in, doneReplan(2), []domain.Event{
						domain.InitiativeEnded{Job: jobID, Initiative: initiativeID, At: at(2)},
					})

					ctx.Expect(in.Stage).ToEqual(domain.InitiativeClosed)
					ctx.Expect(job.Stage).ToEqual(domain.StageRejected)
				}},
				{"a replan NEEDS-HUMAN stops the initiative for a person with the replan's reason", func(ctx *specs.Context) {
					const reason = "the new direction needs a product decision"
					_, in := dropSlice(ctx, threeSlices(domain.CheckpointEverySlice))

					in = replan(ctx, in, needsHumanReplan(2, reason), []domain.Event{
						domain.InitiativeNeedsHumanRaised{Job: jobID, Initiative: initiativeID, Reason: reason, At: at(2)},
					})

					ctx.Expect(in.Stage).ToEqual(domain.InitiativeNeedsHuman)
					ctx.Expect(in.Reason).ToEqual(reason)
				}},
			}
			specs.Table(s, rows, func(r directionRow) string { return r.name }, func(ctx *specs.Context, r directionRow) {
				r.run(ctx)
			})
		})

		s.When("the slice was dropped", func(s *specs.Spec) {
			approval := domain.HumanApproved{Job: jobID, By: "pablogore", WaitSeq: 1, At: at(2)}
			rows := []eventRow{
				{"refuses the build starting again", result("builder", 2, domain.BuildResult{Status: domain.BuildReady})},
				{"refuses a passing gate", domain.GatePassed{Job: jobID, At: at(2)}},
				{"refuses a person's approval", approval},
				{"refuses a completed merge", merged(2)},
				{"refuses a person's feedback", message(domain.AuthorPerson, true, 2)},
				{"refuses a second direction", direction(domain.AuthorPerson, true, 2)},
				{"refuses a stop", stop("pablogore", 2)},
			}
			specs.Table(s, rows, func(r eventRow) string { return r.name }, func(ctx *specs.Context, r eventRow) {
				job, _ := dropSlice(ctx, threeSlices(domain.CheckpointAuto))

				evs, err := decide.Decide(job, r.ev, defaultPipeline())

				ctx.Expect(err).To(specs.MatchError(decide.ErrIllegalTransition))
				ctx.Expect(evs).To(specs.BeEmpty())
				ctx.Expect(decide.Apply(job, evs)).ToEqual(job)
			})
		})

		s.When("it does not come from an allowed person", func(s *specs.Spec) {
			rows := []messageRow{
				{"ignores a person who is not allowed", direction(domain.AuthorPerson, false, 1)},
				{"ignores an allowed bot", direction(domain.AuthorBot, true, 1)},
				{"ignores a bot that is not allowed", direction(domain.AuthorBot, false, 1)},
				{"ignores CI", direction(domain.AuthorCI, true, 1)},
				{"ignores an author of no kind", direction("", true, 1)},
			}
			specs.Table(s, rows, func(r messageRow) string { return r.name }, func(ctx *specs.Context, r messageRow) {
				for _, st := range []string{"build", domain.StageNeedsHuman, domain.StageNeedsInfo} {
					job := unmergedSlice(st)
					in := threeSlices(domain.CheckpointAuto)

					evs, err := decide.Decide(job, r.ev, defaultPipeline())

					ctx.Expect(err).To(specs.BeNil())
					ctx.Expect(evs).To(specs.BeEmpty())
					ctx.Expect(decide.Apply(job, evs)).ToEqual(job)
					ctx.Expect(decide.ApplyInitiative(in, evs)).ToEqual(in)
				}
			})
		})

		s.When("the comment is not marked as a direction", func(s *specs.Spec) {
			unmarked := func() domain.MessageReceived {
				m := message(domain.AuthorPerson, true, 1)
				m.Text = directionText
				return m
			}
			s.It("treats it in needs-human as ordinary rework feedback", func(ctx *specs.Context) {
				job := unmergedSlice(domain.StageNeedsHuman)

				evs, err := decide.Decide(job, unmarked(), defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual([]domain.Event{
					domain.ReworkRequested{Job: jobID, By: "pablogore", Text: directionText, At: at(1)},
					entered("build", 1),
				})
			})

			s.It("treats it in needs-info as the answer", func(ctx *specs.Context) {
				job := unmergedSlice(domain.StageNeedsInfo)

				evs, err := decide.Decide(job, unmarked(), defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual([]domain.Event{
					domain.InfoAnswered{Job: jobID, By: "pablogore", Text: directionText, At: at(1)},
					entered("spec", 1),
				})
			})

			s.It("does not drop a slice in build, as for any message there", func(ctx *specs.Context) {
				evs, err := decide.Decide(unmergedSlice("build"), unmarked(), defaultPipeline())

				ctx.Expect(err).To(specs.MatchError(decide.ErrUnsupported))
				ctx.Expect(evs).To(specs.BeEmpty())
			})
		})

		s.When("the job is not an unmerged slice", func(s *specs.Spec) {
			s.It("reopens a merged slice in build as ordinary feedback", func(ctx *specs.Context) {
				evs, err := decide.Decide(unmergedSlice(domain.StageDone), direction(domain.AuthorPerson, true, 1), defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual([]domain.Event{
					domain.ReworkRequested{Job: jobID, By: "pablogore", Text: directionText, At: at(1)},
					entered("build", 1),
				})
			})

			s.It("does not drop a slice whose merge is under way", func(ctx *specs.Context) {
				evs, err := decide.Decide(unmergedSlice(domain.StageMerge), direction(domain.AuthorPerson, true, 1), defaultPipeline())

				ctx.Expect(err).To(specs.MatchError(decide.ErrUnsupported))
				ctx.Expect(evs).To(specs.BeEmpty())
			})

			s.It("treats it as ordinary feedback on a job outside any initiative", func(ctx *specs.Context) {
				job := newJob()
				job.Stage, job.WaitSeq = domain.StageNeedsHuman, 1

				evs, err := decide.Decide(job, direction(domain.AuthorPerson, true, 1), defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual([]domain.Event{
					domain.ReworkRequested{Job: jobID, By: "pablogore", Text: directionText, At: at(1)},
					entered("build", 1),
				})
			})
		})
	})
}
