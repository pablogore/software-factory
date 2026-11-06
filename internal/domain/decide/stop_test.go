package decide_test

import (
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/domain/decide"
)

func stop(by string, minute int) domain.StopRequested {
	return domain.StopRequested{Job: jobID, By: by, At: at(minute)}
}

// stoppedJob is a job a person stopped in review, round 2.
func stoppedJob() domain.Job {
	job := newJob()
	job.Stage, job.Round = domain.StageStopped, 2
	return job
}

type stopRow struct {
	name  string
	stage string
}

type eventRow struct {
	name string
	ev   domain.Event
}

func TestStop(t *testing.T) {
	specs.Describe(t, "Decide on a stop", func(s *specs.Spec) {
		s.When("a person stops a job", func(s *specs.Spec) {
			rows := []stopRow{
				{"stops a job in intake", "intake"},
				{"stops a job in spec", "spec"},
				{"stops a job in specgate", domain.StageSpecgate},
				{"stops a job in build", "build"},
				{"stops a job in gate", domain.StageGate},
				{"stops a job in review", "review"},
				{"stops a job in approve", "approve"},
				{"stops a job in merge", domain.StageMerge},
				{"stops a job in needs-human", domain.StageNeedsHuman},
				{"stops a job in a stage the pipeline lacks", "deploy"},
			}
			specs.Table(s, rows, func(r stopRow) string { return r.name }, func(ctx *specs.Context, r stopRow) {
				job := newJob()
				job.Stage, job.Round = r.stage, 2
				job.Reviewers, job.Passed, job.Changes = []string{"reviewer-code", "reviewer-ux"}, []string{"reviewer-ux"}, []string{"reviewer-code"}

				evs, err := decide.Decide(job, stop("pablogore", 7), defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual([]domain.Event{domain.JobStopped{Job: jobID, By: "pablogore", At: at(7)}})
				after := decide.Apply(job, evs)
				ctx.Expect(after.Stage).ToEqual(domain.StageStopped)
				ctx.Expect(after.Round).ToEqual(2)
				ctx.Expect(after.Reviewers).To(specs.BeEmpty())
				ctx.Expect(after.Passed).To(specs.BeEmpty())
				ctx.Expect(after.Changes).To(specs.BeEmpty())
			})

			s.It("stops a job walking the happy path and nothing of the path moves it afterwards", func(ctx *specs.Context) {
				steps := happyPath()
				job := walk(ctx, steps, 5)

				evs, err := decide.Decide(job, stop("pablogore", 30), defaultPipeline())
				ctx.Expect(err).To(specs.BeNil())
				job = decide.Apply(job, evs)
				ctx.Expect(job.Stage).ToEqual(domain.StageStopped)

				for _, st := range steps[5:] {
					evs, err := decide.Decide(job, st.ev, defaultPipeline())

					ctx.Expect(err).To(specs.MatchError(decide.ErrStopped))
					ctx.Expect(evs).To(specs.BeEmpty())
				}
			})
		})

		s.When("the stop request does not say who asked", func(s *specs.Spec) {
			rows := []eventRow{
				{"rejects an empty name", stop("", 1)},
				{"rejects a name of only spaces", stop(" \t", 1)},
			}
			specs.Table(s, rows, func(r eventRow) string { return r.name }, func(ctx *specs.Context, r eventRow) {
				job := newJob()
				job.Stage = "review"
				before := newJob()
				before.Stage = "review"

				evs, err := decide.Decide(job, r.ev, defaultPipeline())

				ctx.Expect(err).To(specs.MatchError(domain.ErrMissingField))
				ctx.Expect(evs).To(specs.BeEmpty())
				ctx.Expect(job).ToEqual(before)
				ctx.Expect(decide.Apply(job, evs)).ToEqual(before)
			})
		})

		s.When("the job is stopped", func(s *specs.Spec) {
			rows := []eventRow{
				{"another stop changes nothing", stop("pablogore", 1)},
				{"a stop without a name changes nothing", stop("", 1)},
				{"an intake ACCEPT changes nothing", result("intake", 1, domain.IntakeResult{Decision: domain.IntakeAccept})},
				{"a build READY changes nothing", result("builder", 1, domain.BuildResult{Status: domain.BuildReady})},
				{"a review PASS changes nothing", pass("reviewer-code", 1)},
				{"an APPROVE changes nothing", result("approver", 1, domain.ApproveResult{Decision: domain.ApproveApprove})},
				{"a specgate OK changes nothing", domain.SpecgatePassed{Job: jobID, At: at(1)}},
				{"a gate OK changes nothing", domain.GatePassed{Job: jobID, At: at(1)}},
				{"a failing gate changes nothing", domain.GateFailed{Job: jobID, At: at(1)}},
				{"a person's approval changes nothing", domain.HumanApproved{Job: jobID, By: "pablogore", At: at(1)}},
				{"an allowed person's message changes nothing", message(domain.AuthorPerson, true, 1)},
				{"a stage entered changes nothing", entered("build", 1)},
			}
			specs.Table(s, rows, func(r eventRow) string { return r.name }, func(ctx *specs.Context, r eventRow) {
				job := stoppedJob()

				evs, err := decide.Decide(job, r.ev, defaultPipeline())

				ctx.Expect(err).To(specs.MatchError(decide.ErrStopped))
				ctx.Expect(evs).To(specs.BeEmpty())
				ctx.Expect(job).ToEqual(stoppedJob())
				ctx.Expect(decide.Apply(job, evs)).ToEqual(stoppedJob())
			})

			s.It("still ignores a bot's message without error", func(ctx *specs.Context) {
				job := stoppedJob()

				evs, err := decide.Decide(job, message(domain.AuthorBot, true, 1), defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).To(specs.BeEmpty())
				ctx.Expect(decide.Apply(job, evs)).ToEqual(stoppedJob())
			})
		})

		s.When("the event is addressed to another job", func(s *specs.Spec) {
			const other domain.JobID = "004-other"
			bot := message(domain.AuthorBot, false, 1)
			bot.Job = other
			person := message(domain.AuthorPerson, true, 1)
			person.Job = other
			type otherRow struct {
				name  string
				stage string
				ev    domain.Event
			}
			rows := []otherRow{
				{"rejects a stop in review", "review", domain.StopRequested{Job: other, By: "pablogore", At: at(1)}},
				{"rejects a stop in a stopped job", domain.StageStopped, domain.StopRequested{Job: other, By: "pablogore", At: at(1)}},
				{"rejects a stop without a job", "build", domain.StopRequested{By: "pablogore", At: at(1)}},
				{"rejects an intake ACCEPT in intake", "intake", domain.ResultSubmitted{Job: other, Role: "intake", Result: domain.IntakeResult{Decision: domain.IntakeAccept}, At: at(1)}},
				{"rejects a gate OK in gate", domain.StageGate, domain.GatePassed{Job: other, At: at(1)}},
				{"rejects a failing gate in gate", domain.StageGate, domain.GateFailed{Job: other, At: at(1)}},
				{"rejects a specgate OK in specgate", domain.StageSpecgate, domain.SpecgatePassed{Job: other, At: at(1)}},
				{"rejects a person's approval in needs-human", domain.StageNeedsHuman, domain.HumanApproved{Job: other, By: "pablogore", At: at(1)}},
				{"rejects an allowed person's message in needs-human", domain.StageNeedsHuman, person},
				{"rejects a bot's message in needs-human", domain.StageNeedsHuman, bot},
			}
			specs.Table(s, rows, func(r otherRow) string { return r.name }, func(ctx *specs.Context, r otherRow) {
				job := newJob()
				job.Stage = r.stage
				before := newJob()
				before.Stage = r.stage

				evs, err := decide.Decide(job, r.ev, defaultPipeline())

				ctx.Expect(err).To(specs.MatchError(decide.ErrOtherJob))
				ctx.Expect(evs).To(specs.BeEmpty())
				ctx.Expect(job).ToEqual(before)
				ctx.Expect(decide.Apply(job, evs)).ToEqual(before)
			})
		})
	})
}
