package decide_test

import (
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/domain/decide"
)

const secondSlice domain.JobID = "026-s2-second-slice"

func replanAsked(job domain.JobID, minute int) domain.ReplanRequested {
	return domain.ReplanRequested{Job: job, Initiative: initiativeID, At: at(minute)}
}

// walker feeds events to one job in the default pipeline, failing on any error.
func walker(ctx *specs.Context, job *domain.Job) func(domain.Event) []domain.Event {
	return func(ev domain.Event) []domain.Event {
		evs, err := decide.Decide(*job, ev, defaultPipeline())
		ctx.Expect(err).To(specs.BeNil())
		*job = decide.Apply(*job, evs)
		return evs
	}
}

type foldRow struct {
	name string
	in   domain.Initiative
	evs  []domain.Event
}

type reasonRow struct {
	name      string
	job       domain.Job
	ev        domain.Event
	reason    string
	escalated bool
	exception bool
}

type stateRow struct {
	name string
	run  func(ctx *specs.Context)
}

func TestSliceState(t *testing.T) {
	specs.Describe(t, "Decide on the slice-state rules", func(s *specs.Spec) {
		s.When("a slice asks for a replan", func(s *specs.Spec) {
			initiativeIn := func(stage string) domain.Initiative {
				in := threeSlices(domain.CheckpointEverySlice)
				in.Stage = stage
				return in
			}
			waitingStart := threeSlices(domain.CheckpointAuto)
			waitingStart.Job = ""
			refused := []foldRow{
				{"refuses a replan from another slice job while active", initiativeIn(domain.InitiativeActive), []domain.Event{replanAsked(secondSlice, 1)}},
				{"refuses a replan from another initiative's slice", initiativeIn(domain.InitiativeActive), []domain.Event{
					domain.ReplanRequested{Job: jobID, Initiative: "018-other", At: at(0)},
				}},
				{"refuses a replan while the initiative is already replanning", initiativeIn(domain.InitiativeReplan), []domain.Event{replanAsked(jobID, 1)}},
				{"refuses a replan at the checkpoint", initiativeIn(domain.InitiativeCheckpoint), []domain.Event{replanAsked(jobID, 1)}},
				{"refuses a replan while the initiative needs a person", initiativeIn(domain.InitiativeNeedsHuman), []domain.Event{replanAsked(jobID, 1)}},
				{"refuses a replan before the next slice started", waitingStart, []domain.Event{replanAsked(jobID, 1)}},
				{"refuses a replan from a job named empty before the next slice started", waitingStart, []domain.Event{replanAsked("", 1)}},
				{"refuses the start of a slice while another one runs", initiativeIn(domain.InitiativeActive), []domain.Event{
					domain.SliceStarted{Job: secondSlice, Initiative: initiativeID, At: at(0)},
				}},
				{"refuses the start of a slice at the checkpoint", func() domain.Initiative {
					in := initiativeIn(domain.InitiativeCheckpoint)
					in.Job = ""
					return in
				}(), []domain.Event{domain.SliceStarted{Job: secondSlice, Initiative: initiativeID, At: at(0)}}},
			}
			specs.Table(s, refused, func(r foldRow) string { return r.name }, func(ctx *specs.Context, r foldRow) {
				ctx.Expect(decide.ApplyInitiative(r.in, r.evs)).ToEqual(r.in)
			})

			rows := []stateRow{
				{"an active initiative goes to replan on its own slice's merge", func(ctx *specs.Context) {
					_, in := mergeSlice(ctx, threeSlices(domain.CheckpointEverySlice))

					ctx.Expect(in.Stage).ToEqual(domain.InitiativeReplan)
					ctx.Expect(in.Job).ToEqual(jobID)
				}},
				{"a direction on a stale slice job leaves an initiative in needs-human alone", func(ctx *specs.Context) {
					_, in := mergeSlice(ctx, threeSlices(domain.CheckpointAuto))
					in = replan(ctx, in, needsHumanReplan(2, "which API shape?"), []domain.Event{
						domain.InitiativeNeedsHumanRaised{Job: jobID, Initiative: initiativeID, Reason: "which API shape?", At: at(2)},
					})
					stale := unmergedSlice("build")
					stale.ID = secondSlice
					d := direction(domain.AuthorPerson, true, 3)
					d.Job = secondSlice

					evs, err := decide.Decide(stale, d, defaultPipeline())

					ctx.Expect(err).To(specs.BeNil())
					ctx.Expect(decide.Apply(stale, evs).Stage).ToEqual(domain.StageRejected)
					ctx.Expect(decide.ApplyInitiative(in, evs)).ToEqual(in)
				}},
				{"walks three slices: each merge replans only for the slice that runs, and DONE closes it", func(ctx *specs.Context) {
					in := threeSlices(domain.CheckpointAuto)
					slices := []domain.JobID{jobID, secondSlice, "027-s3-third-slice"}
					for k, id := range slices {
						if k > 0 {
							in = decide.ApplyInitiative(in, []domain.Event{domain.SliceStarted{Job: id, Initiative: initiativeID, At: at(10 * k)}})
							ctx.Expect(in.Job).ToEqual(id)
							// The slice before it merged long ago: its replan request is refused.
							ctx.Expect(decide.ApplyInitiative(in, []domain.Event{replanAsked(slices[k-1], 10*k)})).ToEqual(in)
						}
						job := sliceJob()
						job.ID = id
						evs, err := decide.Decide(job, domain.MergeCompleted{Job: id, At: at(10*k + 1)}, defaultPipeline())
						ctx.Expect(err).To(specs.BeNil())
						in = decide.ApplyInitiative(in, evs)
						ctx.Expect(in.Stage).ToEqual(domain.InitiativeReplan)
						ctx.Expect(in.Job).ToEqual(id)

						status := domain.ReplanContinue
						if k == len(slices)-1 {
							status = domain.ReplanDone
						}
						evs, err = decide.Initiative(in, domain.ReplanSubmitted{Job: id, Initiative: initiativeID, Status: status, At: at(10*k + 2)})
						ctx.Expect(err).To(specs.BeNil())
						in = decide.ApplyInitiative(in, evs)
					}
					ctx.Expect(in.Stage).ToEqual(domain.InitiativeClosed)
				}},
				{"a person's OK at the checkpoint asks for the next slice, which no slice runs yet", func(ctx *specs.Context) {
					_, in := mergeSlice(ctx, threeSlices(domain.CheckpointEverySlice))
					in = replan(ctx, in, continued(2), []domain.Event{
						domain.CheckpointReached{Job: jobID, Initiative: initiativeID, At: at(2)},
					})

					in = replan(ctx, in, domain.HumanApproved{Job: jobID, By: "pablogore", At: at(3)}, []domain.Event{nextSlice(3)})

					ctx.Expect(in.Stage).ToEqual(domain.InitiativeActive)
					ctx.Expect(in.Job).ToEqual(domain.JobID(""))
				}},
			}
			specs.Table(s, rows, func(r stateRow) string { return r.name }, func(ctx *specs.Context, r stateRow) {
				r.run(ctx)
			})
		})

		s.When("a job goes to needs-human", func(s *specs.Spec) {
			inStage := func(stage string, round, rejected int) domain.Job {
				job := newJob()
				job.Stage, job.Round, job.SpecRejections = stage, round, rejected
				job.Reviewers = []string{"reviewer-code"}
				return job
			}
			rows := []reasonRow{
				{"records specgate's escalation and its reason", atSpecgate(2), specgateFailed(1), "spec rejected by specgate 3 times", true, false},
				{"records a length exception and its reason", atSpecgate(2), exceptionAsked(1), "spec asks for a length exception", false, true},
				{"records CHANGES in the last round, not as specgate's escalation, even after three rejections", inStage("review", 3, 3), changes("reviewer-code", 1),
					"CHANGES in the last round (3 of 3) from reviewer-code", false, false},
				{"records a gate failing in the last round", inStage(domain.StageGate, 3, 3), domain.GateFailed{Job: jobID, At: at(1)},
					"gate failed in the last round (3 of 3)", false, false},
				{"records an approver escalation, not as specgate's escalation, even after three rejections", inStage("approve", 2, 3),
					result("approver", 1, domain.ApproveResult{Decision: domain.ApproveEscalate, Reasons: []string{"needs a product decision"}}),
					"approver escalated: needs a product decision", false, false},
			}
			specs.Table(s, rows, func(r reasonRow) string { return r.name }, func(ctx *specs.Context, r reasonRow) {
				evs, err := decide.Decide(r.job, r.ev, defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				after := decide.Apply(r.job, evs)
				ctx.Expect(after.Stage).ToEqual(domain.StageNeedsHuman)
				ctx.Expect(after.Reason).ToEqual(r.reason)
				ctx.Expect(after.SpecEscalated).ToEqual(r.escalated)
				ctx.Expect(after.SpecException).ToEqual(r.exception)
			})

			leaving := []step{
				{"forgets the reason on a person's feedback", message(domain.AuthorPerson, true, 2), nil},
				{"forgets the reason on a person's OK", domain.HumanApproved{Job: jobID, By: "pablogore", At: at(2)}, nil},
				{"forgets the reason on a stop", stop("pablogore", 2), nil},
			}
			specs.Table(s, leaving, func(r step) string { return r.name }, func(ctx *specs.Context, r step) {
				job := specEscalated()

				evs, err := decide.Decide(job, r.ev, defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				after := decide.Apply(job, evs)
				ctx.Expect(after.Reason).ToEqual("")
				ctx.Expect(after.SpecEscalated).To(specs.BeFalse())
			})

			s.It("forgets a length exception if the job is ever asked a question", func(ctx *specs.Context) {
				after := decide.Apply(waitingJob(), []domain.Event{domain.NeedsInfo{Job: jobID, From: "intake", At: at(2)}})

				ctx.Expect(after.SpecException).To(specs.BeFalse())
				ctx.Expect(after.Reason).ToEqual("")
			})
		})

		s.When("an allowed person gives feedback in needs-human with an old rejection count of 3", func(s *specs.Spec) {
			rows := []stateRow{
				{"sends feedback after an approver escalation to build, not spec", func(ctx *specs.Context) {
					job := newJob()
					job.Stage, job.Round, job.SpecRejections = "approve", 2, 3
					step := walker(ctx, &job)
					step(result("approver", 1, domain.ApproveResult{Decision: domain.ApproveEscalate, Reasons: []string{"why"}}))

					evs := step(message(domain.AuthorPerson, true, 2))

					ctx.Expect(evs).ToEqual([]domain.Event{
						domain.ReworkRequested{Job: jobID, By: "pablogore", Text: "rename the flag", At: at(2)},
						entered("build", 2),
					})
					ctx.Expect(job.SpecRejections).ToEqual(3)
				}},
				{"sends feedback to build on a job stopped by specgate, approved out of band, merged, reopened and escalated", func(ctx *specs.Context) {
					job := newJob()
					job.Stage = "spec"
					step := walker(ctx, &job)
					for k := 0; k < 3; k++ {
						step(result("spec-writer", 2*k, domain.SpecResult{Title: "S6", Spec: "# S6"}))
						step(specgateFailed(2*k + 1))
					}
					ctx.Expect(job.SpecEscalated).To(specs.BeTrue())
					step(domain.HumanApproved{Job: jobID, By: "pablogore", At: at(10)})
					step(merged(11))
					step(message(domain.AuthorPerson, true, 12))
					ctx.Expect(job.Stage).ToEqual("build")
					step(result("builder", 13, domain.BuildResult{Status: domain.BuildReady}))
					step(domain.GatePassed{Job: jobID, At: at(14)})
					for _, role := range reviewers {
						step(pass(role, 15))
					}
					step(result("approver", 16, domain.ApproveResult{Decision: domain.ApproveEscalate, Reasons: []string{"why"}}))
					ctx.Expect(job.Stage).ToEqual(domain.StageNeedsHuman)
					ctx.Expect(job.SpecRejections).ToEqual(3)

					step(message(domain.AuthorPerson, true, 17))

					ctx.Expect(job.Stage).ToEqual("build")
					ctx.Expect(job.SpecRejections).ToEqual(3)
				}},
				{"sends feedback after specgate's escalation to spec", func(ctx *specs.Context) {
					job := specEscalated()
					step := walker(ctx, &job)

					step(message(domain.AuthorPerson, true, 1))

					ctx.Expect(job.Stage).ToEqual("spec")
					ctx.Expect(job.SpecRejections).ToEqual(0)
				}},
			}
			specs.Table(s, rows, func(r stateRow) string { return r.name }, func(ctx *specs.Context, r stateRow) {
				r.run(ctx)
			})

			s.It("refuses a build while the job waits after specgate's escalation", func(ctx *specs.Context) {
				evs, err := decide.Decide(specEscalated(), result("builder", 1, domain.BuildResult{Status: domain.BuildReady}), defaultPipeline())

				ctx.Expect(err).To(specs.MatchError(decide.ErrIllegalTransition))
				ctx.Expect(evs).To(specs.BeEmpty())
			})
		})

		s.When("a job waits on a length exception", func(s *specs.Spec) {
			waitingSlice := func() domain.Job {
				job := waitingJob()
				job.Initiative, job.Reason = initiativeID, "spec asks for a length exception"
				return job
			}
			rows := []stateRow{
				{"a stop ends it stopped with the exception forgotten", func(ctx *specs.Context) {
					job := waitingJob()

					evs, err := decide.Decide(job, stop("pablogore", 2), defaultPipeline())

					ctx.Expect(err).To(specs.BeNil())
					ctx.Expect(evs).ToEqual([]domain.Event{domain.JobStopped{Job: jobID, By: "pablogore", At: at(2)}})
					after := decide.Apply(job, evs)
					ctx.Expect(after.Stage).ToEqual(domain.StageStopped)
					ctx.Expect(after.SpecException).To(specs.BeFalse())
				}},
				{"a stop without who asked is still refused", func(ctx *specs.Context) {
					evs, err := decide.Decide(waitingJob(), stop("", 2), defaultPipeline())

					ctx.Expect(err).To(specs.MatchError(domain.ErrMissingField))
					ctx.Expect(evs).To(specs.BeEmpty())
				}},
				{"a direction drops the slice and asks its initiative for a replan", func(ctx *specs.Context) {
					job := waitingSlice()

					evs, err := decide.Decide(job, direction(domain.AuthorPerson, true, 2), defaultPipeline())

					ctx.Expect(err).To(specs.BeNil())
					ctx.Expect(evs).ToEqual(droppedBy(2))
					after := decide.Apply(job, evs)
					ctx.Expect(after.Stage).ToEqual(domain.StageRejected)
					ctx.Expect(after.SpecException).To(specs.BeFalse())
					ctx.Expect(after.Reason).ToEqual("")
					in := decide.ApplyInitiative(threeSlices(domain.CheckpointEverySlice), evs)
					ctx.Expect(in.Stage).ToEqual(domain.InitiativeReplan)
				}},
				{"a direction from someone not allowed changes nothing", func(ctx *specs.Context) {
					evs, err := decide.Decide(waitingSlice(), direction(domain.AuthorPerson, false, 2), defaultPipeline())

					ctx.Expect(err).To(specs.BeNil())
					ctx.Expect(evs).To(specs.BeEmpty())
				}},
			}
			specs.Table(s, rows, func(r stateRow) string { return r.name }, func(ctx *specs.Context, r stateRow) {
				r.run(ctx)
			})
		})

		s.When("an OK does not say who gave it", func(s *specs.Spec) {
			checkpoint := threeSlices(domain.CheckpointEverySlice)
			checkpoint.Stage = domain.InitiativeCheckpoint
			type okRow struct {
				name string
				by   string
				run  func(domain.HumanApproved) ([]domain.Event, error)
			}
			onJob := func(job domain.Job) func(domain.HumanApproved) ([]domain.Event, error) {
				return func(ok domain.HumanApproved) ([]domain.Event, error) {
					return decide.Decide(job, ok, defaultPipeline())
				}
			}
			onInitiative := func(ok domain.HumanApproved) ([]domain.Event, error) { return decide.Initiative(checkpoint, ok) }
			rows := []okRow{
				{"refuses an empty name on a length exception", "", onJob(waitingJob())},
				{"refuses a name of only spaces on a length exception", " \t", onJob(waitingJob())},
				{"refuses an empty name in needs-human", "", onJob(specEscalated())},
				{"refuses a name of only spaces in needs-human", " \t", onJob(specEscalated())},
				{"refuses an empty name at the initiative's checkpoint", "", onInitiative},
				{"refuses a name of only spaces at the initiative's checkpoint", " \t", onInitiative},
			}
			specs.Table(s, rows, func(r okRow) string { return r.name }, func(ctx *specs.Context, r okRow) {
				evs, err := r.run(domain.HumanApproved{Job: jobID, By: r.by, At: at(2)})

				ctx.Expect(err).To(specs.MatchError(domain.ErrMissingField))
				ctx.Expect(evs).To(specs.BeEmpty())
			})
		})
	})
}
