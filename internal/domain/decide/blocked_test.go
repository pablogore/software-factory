package decide_test

import (
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/domain/decide"
)

func blockedBuild(summary string, minute int) domain.ResultSubmitted {
	return result("builder", minute, domain.BuildResult{Status: domain.BuildBlocked, Summary: summary})
}

// building is a job in the build stage of the given round.
func building(round int) domain.Job {
	job := newJob()
	job.Stage, job.Round = "build", round
	return job
}

// blockedJob is a job a builder BLOCKED left in needs-human, after triage
// escalated it.
func blockedJob(ctx *specs.Context, round int) domain.Job {
	job := building(round)
	evs, err := decide.Decide(job, blockedBuild("cannot reach the payments API", 1), defaultPipeline())
	ctx.Expect(err).To(specs.BeNil())
	job, _ = throughTriage(ctx, job, evs, defaultPipeline())
	return job
}

type blockedRow struct {
	name    string
	round   int
	summary string
	reason  string
}

type ignoredRow struct {
	name    string
	message domain.MessageReceived
}

func TestBlocked(t *testing.T) {
	specs.Describe(t, "Decide on a builder BLOCKED", func(s *specs.Spec) {
		s.When("the builder reports BLOCKED", func(s *specs.Spec) {
			rows := []blockedRow{
				{"goes to triage with the builder's summary in round 1", 1, "cannot reach the payments API", "cannot reach the payments API"},
				{"goes to triage with the builder's summary in the last round", 3, "spec contradicts itself", "spec contradicts itself"},
				{"names the builder when it gave no summary", 2, "  ", "builder blocked"},
			}
			specs.Table(s, rows, func(r blockedRow) string { return r.name }, func(ctx *specs.Context, r blockedRow) {
				job := building(r.round)

				evs, err := decide.Decide(job, blockedBuild(r.summary, 1), defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual([]domain.Event{triaging(r.reason, domain.CauseBlocked, 1)})
				after := decide.Apply(job, evs)
				ctx.Expect(after.Stage).ToEqual(domain.StageTriage)
				ctx.Expect(after.Reason).ToEqual(r.reason)
				ctx.Expect(after.Wall).ToEqual(domain.CauseBlocked)
				ctx.Expect(after.Round).ToEqual(r.round)
			})

			s.It("never returns ErrUnsupported", func(ctx *specs.Context) {
				_, err := decide.Decide(building(1), blockedBuild("", 1), defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
			})
		})

		s.When("an allowed person gives feedback on the blocked job", func(s *specs.Spec) {
			s.It("sends it back to build in the same round with the feedback as input", func(ctx *specs.Context) {
				job := blockedJob(ctx, 2)

				evs, err := decide.Decide(job, message(domain.AuthorPerson, true, 2), defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual([]domain.Event{
					domain.ReworkRequested{Job: jobID, By: "pablogore", Text: "rename the flag", At: at(2)},
					entered("build", 2),
				})
				after := decide.Apply(job, evs)
				ctx.Expect(after.Stage).ToEqual("build")
				ctx.Expect(after.Round).ToEqual(2)
				ctx.Expect(after.Reason).ToEqual("")
			})
		})

		s.When("the feedback is not marked as coming from an allowed person", func(s *specs.Spec) {
			stranger := message(domain.AuthorPerson, false, 2)
			stranger.Text = "OK, approved"
			bot := message(domain.AuthorBot, true, 2)
			bot.Text = "[direction] drop this slice"
			ci := message(domain.AuthorCI, true, 2)
			ci.Text = "rebuild"
			rows := []ignoredRow{
				{"ignores a person not allowed, whatever the text says", stranger},
				{"ignores a bot even when marked allowed", bot},
				{"ignores CI even when marked allowed", ci},
			}
			specs.Table(s, rows, func(r ignoredRow) string { return r.name }, func(ctx *specs.Context, r ignoredRow) {
				job := blockedJob(ctx, 1)

				evs, err := decide.Decide(job, r.message, defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).To(specs.BeEmpty())
				ctx.Expect(decide.Apply(job, evs)).ToEqual(job)
			})
		})
	})
}
