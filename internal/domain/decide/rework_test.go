package decide_test

import (
	"slices"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/domain/decide"
)

// reworksPipeline is defaultPipeline with limits.human_reworks set to n.
func reworksPipeline(n int) domain.Pipeline {
	p := defaultPipeline()
	p.Limits.HumanReworks = &n
	return p
}

// feedbackOn is a person's feedback answering the job's current wait.
func feedbackOn(job domain.Job, minute int) domain.MessageReceived {
	return answering(message(domain.AuthorPerson, true, minute), job.WaitSeq)
}

// reworkOnce sends a person's feedback on job in needs-human and applies it.
func reworkOnce(ctx *specs.Context, job domain.Job, p domain.Pipeline) domain.Job {
	evs, err := decide.Decide(job, feedbackOn(job, 2), p)
	ctx.Expect(err).To(specs.BeNil())
	return decide.Apply(job, evs)
}

// escalateAgain walks a job in build through gate, review and approve to an
// approver ESCALATE, so it waits in needs-human again.
func escalateAgain(ctx *specs.Context, job domain.Job, p domain.Pipeline) domain.Job {
	steps := happyPath()
	evs := []domain.Event{}
	for _, st := range steps[3:10] {
		evs = append(evs, st.ev)
	}
	evs = append(evs, result("approver", 11, domain.ApproveResult{Decision: domain.ApproveEscalate, Reasons: []string{"too risky"}}))
	for _, ev := range evs {
		out, err := decide.Decide(job, ev, p)
		ctx.Expect(err).To(specs.BeNil())
		job = decide.Apply(job, out)
	}
	ctx.Expect(job.Stage).ToEqual(domain.StageNeedsHuman)
	return job
}

// usedUp is a slice job waiting in needs-human after an approver ESCALATE and
// the two reworks human_reworks 2 allows.
func usedUp(ctx *specs.Context, p domain.Pipeline) domain.Job {
	job := waitingFor(ctx, domain.CauseEscalated)
	for range 2 {
		job = reworkOnce(ctx, job, p)
		ctx.Expect(job.Stage).ToEqual("build")
		job = escalateAgain(ctx, job, p)
	}
	ctx.Expect(job.HumanReworks).ToEqual(2)
	return job
}

// toSpec reports whether feedback on cause sends the job back to spec.
func toSpec(cause domain.HumanCause) bool {
	return cause == domain.CauseSpecEscalated || cause == domain.CauseSpecException
}

// buildBound is causeRows whose feedback sends the job back to build; after
// a spent budget feedback sends it nowhere.
func buildBound() []causeRow {
	return slices.DeleteFunc(causeRows(), func(r causeRow) bool { return toSpec(r.cause) || r.cause == domain.CauseBudget })
}

// specBound is causeRows whose feedback sends the job back to spec.
func specBound() []causeRow {
	return slices.DeleteFunc(causeRows(), func(r causeRow) bool { return !toSpec(r.cause) })
}

type unlimitedRow struct {
	name string
	p    func() domain.Pipeline
}

func TestHumanReworks(t *testing.T) {
	specs.Describe(t, "Decide on limits.human_reworks", func(s *specs.Spec) {
		s.When("a person sends a job back from needs-human", func(s *specs.Spec) {
			specs.Table(s, buildBound(), func(r causeRow) string { return "counts one rework back to build after " + string(r.cause) }, func(ctx *specs.Context, r causeRow) {
				job := waitingFor(ctx, r.cause)

				after := reworkOnce(ctx, job, defaultPipeline())

				ctx.Expect(after.Stage).ToEqual("build")
				ctx.Expect(after.HumanReworks).ToEqual(1)
			})

			specs.Table(s, specBound(), func(r causeRow) string { return "keeps the count on feedback back to spec after " + string(r.cause) }, func(ctx *specs.Context, r causeRow) {
				job := waitingFor(ctx, r.cause)
				job.HumanReworks = 1

				after := reworkOnce(ctx, job, defaultPipeline())

				ctx.Expect(after.Stage).ToEqual("spec")
				ctx.Expect(after.HumanReworks).ToEqual(1)
			})

			s.It("keeps the count when the spec is rewritten and its rejections start again", func(ctx *specs.Context) {
				job := waitingFor(ctx, domain.CauseSpecEscalated)
				job.HumanReworks = 1

				after := reworkOnce(ctx, job, defaultPipeline())

				ctx.Expect(after.Stage).ToEqual("spec")
				ctx.Expect(after.SpecRejections).ToEqual(0)
				ctx.Expect(after.HumanReworks).ToEqual(1)
			})

			s.It("replays a recorded rework back to spec without counting it", func(ctx *specs.Context) {
				job := waitingFor(ctx, domain.CauseSpecException)
				history := []domain.Event{
					domain.ReworkRequested{Job: jobID, By: "pablogore", Text: "shorter", At: at(2)},
					domain.StageEntered{Job: jobID, Stage: "spec", At: at(2)},
				}

				after := decide.Apply(job, history)

				ctx.Expect(after.Stage).ToEqual("spec")
				ctx.Expect(after.HumanReworks).ToEqual(0)
			})
		})

		s.When("the pipeline sets human_reworks 2", func(s *specs.Spec) {
			s.It("accepts two reworks and refuses the third, naming the limit", func(ctx *specs.Context) {
				p := reworksPipeline(2)
				job := usedUp(ctx, p)

				evs, err := decide.Decide(job, feedbackOn(job, 12), p)

				ctx.Expect(err).To(specs.MatchError(decide.ErrReworkRefused))
				ctx.Expect(err.Error()).ToEqual("rework refused: 2 of human_reworks 2 used")
				ctx.Expect(evs).To(specs.BeEmpty())
				ctx.Expect(decide.Apply(job, evs)).ToEqual(job)
			})

			s.It("still merges on a person's OK once the reworks are used up", func(ctx *specs.Context) {
				p := reworksPipeline(2)
				job := usedUp(ctx, p)

				evs, err := decide.Decide(job, domain.HumanApproved{Job: jobID, By: "pablogore", WaitSeq: job.WaitSeq, At: at(12)}, p)

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual([]domain.Event{domain.MergeRequested{Job: jobID, At: at(12)}})
			})

			s.It("still stops the job once the reworks are used up", func(ctx *specs.Context) {
				p := reworksPipeline(2)
				job := usedUp(ctx, p)

				evs, err := decide.Decide(job, stop("pablogore", 12), p)

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual([]domain.Event{domain.JobStopped{Job: jobID, By: "pablogore", At: at(12)}})
			})

			s.It("still drops the slice on a direction once the reworks are used up", func(ctx *specs.Context) {
				p := reworksPipeline(2)
				job := usedUp(ctx, p)

				evs, err := decide.Decide(job, answering(direction(domain.AuthorPerson, true, 12), job.WaitSeq), p)

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual(droppedBy(12))
			})

			specs.Table(s, buildBound(), func(r causeRow) string { return "refuses a third rework back to build after " + string(r.cause) }, func(ctx *specs.Context, r causeRow) {
				job := waitingFor(ctx, r.cause)
				job.HumanReworks = 2

				evs, err := decide.Decide(job, feedbackOn(job, 2), reworksPipeline(2))

				ctx.Expect(err).To(specs.MatchError(decide.ErrReworkRefused))
				ctx.Expect(evs).To(specs.BeEmpty())
			})

			specs.Table(s, specBound(), func(r causeRow) string { return "still accepts feedback back to spec after " + string(r.cause) }, func(ctx *specs.Context, r causeRow) {
				job := waitingFor(ctx, r.cause)
				job.HumanReworks = 2

				after := reworkOnce(ctx, job, reworksPipeline(2))

				ctx.Expect(after.Stage).ToEqual("spec")
				ctx.Expect(after.HumanReworks).ToEqual(2)
			})

			s.It("refuses feedback with an unknown cause before the cap", func(ctx *specs.Context) {
				job := waitingFor(ctx, domain.CauseEscalated)
				job.Cause, job.HumanReworks = "mystery", 2

				evs, err := decide.Decide(job, feedbackOn(job, 2), reworksPipeline(2))

				ctx.Expect(err).To(specs.MatchError(domain.ErrInvalidValue))
				ctx.Expect(err).To(specs.Not(specs.MatchError(decide.ErrReworkRefused)))
				ctx.Expect(evs).To(specs.BeEmpty())
			})
		})

		s.When("a person reopens a merged job from done", func(s *specs.Spec) {
			s.It("leaves the count unchanged", func(ctx *specs.Context) {
				job := doneJob()
				job.HumanReworks = 1

				evs, err := decide.Decide(job, message(domain.AuthorPerson, true, 1), reworksPipeline(2))

				ctx.Expect(err).To(specs.BeNil())
				after := decide.Apply(job, evs)
				ctx.Expect(after.Stage).ToEqual("build")
				ctx.Expect(after.HumanReworks).ToEqual(1)
			})

			s.It("reopens it even once the reworks are used up", func(ctx *specs.Context) {
				job := doneJob()
				job.HumanReworks = 2

				evs, err := decide.Decide(job, message(domain.AuthorPerson, true, 1), reworksPipeline(2))

				ctx.Expect(err).To(specs.BeNil())
				after := decide.Apply(job, evs)
				ctx.Expect(after.Stage).ToEqual("build")
				ctx.Expect(after.HumanReworks).ToEqual(2)
			})
		})

		s.When("the pipeline does not cap reworks", func(s *specs.Spec) {
			rows := []unlimitedRow{
				{"accepts a rework with no limits block", defaultPipeline},
				{"accepts a rework with limits but no human_reworks", func() domain.Pipeline {
					p := defaultPipeline()
					one := 1
					p.Limits.SpecRejections = &one
					return p
				}},
			}
			specs.Table(s, rows, func(r unlimitedRow) string { return r.name }, func(ctx *specs.Context, r unlimitedRow) {
				job := waitingFor(ctx, domain.CauseBlocked)
				job.HumanReworks = 50

				after := reworkOnce(ctx, job, r.p())

				ctx.Expect(after.Stage).ToEqual("build")
				ctx.Expect(after.HumanReworks).ToEqual(51)
			})
		})
	})
}
