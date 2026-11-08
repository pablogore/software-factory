package decide_test

import (
	"strings"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/domain/decide"
)

const askedText = "Dark mode for every user, or only in beta?"

// asks is role's question with two options, for the product person.
func asks(role string, minute int) domain.HumanAsked {
	return domain.HumanAsked{Job: jobID, Role: role, Question: askedText, Options: []string{"everyone", "beta only"}, To: domain.AudienceProduct, At: at(minute)}
}

func approverAsks(minute int) domain.HumanAsked {
	return asks("approver", minute)
}

// inStage is a new job in stage, in round 1.
func inStage(stage string) domain.Job {
	job := newJob()
	job.Stage = stage
	return job
}

type askedRow struct {
	name string
	job  domain.Job
	ev   domain.HumanAsked
	err  error
}

// askAnswerRow is a question from role in stage that a person answers.
type askAnswerRow struct {
	name  string
	stage string
	role  string
}

func TestHumanAsked(t *testing.T) {
	specs.Describe(t, "Decide on an agent's question to a person", func(s *specs.Spec) {
		s.It("records a spec-writer's question and waits in needs-info, with spec as the asker", func(ctx *specs.Context) {
			job := inStage("spec")
			ev := asks("spec-writer", 1)

			evs, err := decide.Decide(job, ev, defaultPipeline())

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(evs).ToEqual([]domain.Event{ev, domain.NeedsInfo{Job: jobID, From: "spec", Question: askedText, At: at(1)}})
			after := decide.Apply(job, evs)
			ctx.Expect(after.Stage).ToEqual(domain.StageNeedsInfo)
			ctx.Expect(after.InfoFrom).ToEqual("spec")
			ctx.Expect(after.Question).ToEqual(askedText)
			ctx.Expect(after.WaitSeq).ToEqual(1)
			ctx.Expect(after.Validate()).To(specs.BeNil())
		})

		s.It("records an approver's question and waits in needs-human for it, with approve as the asker", func(ctx *specs.Context) {
			job := inStage("approve")
			ev := approverAsks(1)

			evs, err := decide.Decide(job, ev, defaultPipeline())

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(evs).ToEqual([]domain.Event{ev, domain.NeedsHuman{Job: jobID, Reason: "approver asks the product person a question",
				Question: askedText, From: "approve", Cause: domain.CauseAsked, At: at(1)}})
			after := decide.Apply(job, evs)
			ctx.Expect(after.Stage).ToEqual(domain.StageNeedsHuman)
			ctx.Expect(after.Cause).ToEqual(domain.CauseAsked)
			ctx.Expect(after.InfoFrom).ToEqual("approve")
			ctx.Expect(after.Question).ToEqual(askedText)
			ctx.Expect(after.Validate()).To(specs.BeNil())
		})

		s.When("a person answers", func(s *specs.Spec) {
			rows := []askAnswerRow{
				{"a spec-writer's question sends the job back to spec with the answer", "spec", "spec-writer"},
				{"an approver's question sends the job back to approve with the answer", "approve", "approver"},
			}
			specs.Table(s, rows, func(r askAnswerRow) string { return r.name }, func(ctx *specs.Context, r askAnswerRow) {
				job := inStage(r.stage)
				evs, err := decide.Decide(job, asks(r.role, 1), defaultPipeline())
				ctx.Expect(err).To(specs.BeNil())
				job = decide.Apply(job, evs)

				evs, err = decide.Decide(job, message(domain.AuthorPerson, true, 2), defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual([]domain.Event{
					domain.InfoAnswered{Job: jobID, By: "pablogore", Text: "rename the flag", At: at(2)},
					entered(r.stage, 2),
				})
				after := decide.Apply(job, evs)
				ctx.Expect(after.Stage).ToEqual(r.stage)
				ctx.Expect(after.Round).ToEqual(1)
				ctx.Expect(after.InfoFrom).ToEqual("")
				ctx.Expect(after.Question).ToEqual("")
				ctx.Expect(after.Cause).ToEqual(domain.HumanCause(""))
				ctx.Expect(after.HumanReworks).ToEqual(0)
			})
		})

		long := strings.Repeat("a", domain.MaxQuestionText+1)
		rows := []askedRow{
			{"refuses a builder's question in build", inStage("build"), asks("builder", 1), decide.ErrIllegalTransition},
			{"refuses a reviewer's question in review", inStage("review"), asks("reviewer-code", 1), decide.ErrIllegalTransition},
			{"refuses intake's question in intake", inStage("intake"), asks("intake", 1), decide.ErrIllegalTransition},
			{"refuses a question from a role the stage does not run", inStage("spec"), asks("approver", 1), decide.ErrIllegalTransition},
			{"refuses a question outside a pipeline stage", inStage(domain.StageGate), asks("spec-writer", 1), decide.ErrIllegalTransition},
			{"refuses a question while the job already waits", askingJob(), asks("spec-writer", 1), decide.ErrIllegalTransition},
			{"refuses a blank question", inStage("spec"), func() domain.HumanAsked { e := asks("spec-writer", 1); e.Question = " "; return e }(), domain.ErrMissingField},
			{"refuses a question over the limit", inStage("spec"), func() domain.HumanAsked { e := asks("spec-writer", 1); e.Question = long; return e }(), domain.ErrInvalidValue},
			{"refuses a blank option", inStage("spec"), func() domain.HumanAsked { e := asks("spec-writer", 1); e.Options = []string{"yes", ""}; return e }(), domain.ErrMissingField},
			{"refuses too many options", inStage("spec"), func() domain.HumanAsked {
				e := asks("spec-writer", 1)
				e.Options = strings.Split(strings.Repeat("x,", domain.MaxOptions)+"x", ",")
				return e
			}(), domain.ErrInvalidValue},
			{"refuses a question with no audience", inStage("spec"), func() domain.HumanAsked { e := asks("spec-writer", 1); e.To = ""; return e }(), domain.ErrMissingField},
			{"refuses a question for an unknown audience", inStage("approve"), func() domain.HumanAsked { e := approverAsks(1); e.To = "legal"; return e }(), domain.ErrInvalidValue},
		}
		specs.Table(s, rows, func(r askedRow) string { return r.name + " and nothing changes" }, func(ctx *specs.Context, r askedRow) {
			evs, err := decide.Decide(r.job, r.ev, defaultPipeline())

			ctx.Expect(err).To(specs.MatchError(r.err))
			ctx.Expect(evs).To(specs.BeEmpty())
			ctx.Expect(decide.Apply(r.job, evs)).ToEqual(r.job)
		})

		s.It("takes a question with no options", func(ctx *specs.Context) {
			ev := asks("spec-writer", 1)
			ev.Options = nil

			evs, err := decide.Decide(inStage("spec"), ev, defaultPipeline())

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(evs).To(specs.HaveLen(2))
		})
	})
}
