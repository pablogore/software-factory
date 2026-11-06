package decide_test

import (
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/domain/decide"
)

// causeRow is one way into needs-human: a job and the event that stops it.
type causeRow struct {
	name  string
	job   func() domain.Job
	ev    domain.Event
	cause domain.HumanCause
}

func causeRows() []causeRow {
	approving := func() domain.Job {
		job := newJob()
		job.Stage, job.Round = "approve", 2
		return job
	}
	lastReview := func() domain.Job {
		job := newJob()
		job.Stage, job.Round, job.Passed = "review", 3, []string{"reviewer-security", "reviewer-ux", "reviewer-ui", "security-deep"}
		return job
	}
	return []causeRow{
		{"a builder BLOCKED records blocked", func() domain.Job { return building(2) }, blockedBuild("cannot reach the payments API", 1), domain.CauseBlocked},
		{"an approver ESCALATE records escalated", approving,
			result("approver", 1, domain.ApproveResult{Decision: domain.ApproveEscalate, Reasons: []string{"too risky"}}), domain.CauseEscalated},
		{"CHANGES in the last round records changes", lastReview, changes("reviewer-code", 1), domain.CauseChanges},
		{"a failed gate in the last round records gate-failed", func() domain.Job { return atGate(3, nil) }, gateFailed(1), domain.CauseGateFailed},
		{"specgate's last rejection records spec-escalated", func() domain.Job { return atSpecgate(2) }, specgateFailed(1), domain.CauseSpecEscalated},
		{"a length exception records spec-exception", func() domain.Job { return atSpecgate(0) }, exceptionAsked(1), domain.CauseSpecException},
	}
}

// stoppedFor puts a slice job in needs-human the way r does.
func stoppedFor(ctx *specs.Context, r causeRow) domain.Job {
	job := r.job()
	job.Initiative = initiativeID
	evs, err := decide.Decide(job, r.ev, defaultPipeline())
	ctx.Expect(err).To(specs.BeNil())
	return decide.Apply(job, evs)
}

type exitRow struct {
	name string
	ev   domain.Event
}

type applyExitRow struct {
	name   string
	ev     domain.Event
	reason string
}

func TestNeedsHumanCause(t *testing.T) {
	specs.Describe(t, "the cause of needs-human", func(s *specs.Spec) {
		s.When("a job enters needs-human", func(s *specs.Spec) {
			specs.Table(s, causeRows(), func(r causeRow) string { return r.name }, func(ctx *specs.Context, r causeRow) {
				job := r.job()

				evs, err := decide.Decide(job, r.ev, defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				nh, ok := evs[len(evs)-1].(domain.NeedsHuman)
				ctx.Expect(ok).To(specs.BeTrue())
				ctx.Expect(nh.Cause).ToEqual(r.cause)
				ctx.Expect(nh.Cause.Validate()).To(specs.BeNil())
				after := decide.Apply(job, evs)
				ctx.Expect(after.Stage).ToEqual(domain.StageNeedsHuman)
				ctx.Expect(after.Cause).ToEqual(r.cause)
				ctx.Expect(after.Reason).ToEqual(nh.Reason)
				// The marks stay alongside the cause until S4 folds them in.
				ctx.Expect(after.SpecException).ToEqual(r.cause == domain.CauseSpecException)
				ctx.Expect(after.SpecEscalated).ToEqual(r.cause == domain.CauseSpecEscalated)
			})

			s.It("has one way in for each cause", func(ctx *specs.Context) {
				var causes []domain.HumanCause
				for _, r := range causeRows() {
					causes = append(causes, r.cause)
				}

				ctx.Expect(causes).ToEqual(domain.HumanCauses)
			})
		})

		s.When("a person moves the job out of needs-human", func(s *specs.Spec) {
			exits := []exitRow{
				{"an OK", domain.HumanApproved{Job: jobID, By: "pablogore", At: at(2)}},
				{"feedback", message(domain.AuthorPerson, true, 2)},
				{"a stop", stop("pablogore", 2)},
				{"a direction", direction(domain.AuthorPerson, true, 2)},
			}
			for _, r := range causeRows() {
				s.When("the job is there because "+r.name, func(s *specs.Spec) {
					specs.Table(s, exits, func(x exitRow) string { return x.name + " clears the cause, the reason and both marks" }, func(ctx *specs.Context, x exitRow) {
						job := stoppedFor(ctx, r)

						evs, err := decide.Decide(job, x.ev, defaultPipeline())

						ctx.Expect(err).To(specs.BeNil())
						after := decide.Apply(job, evs)
						ctx.Expect(after.Stage).To(specs.Not(specs.Equal(domain.StageNeedsHuman)))
						ctx.Expect(after.Cause).ToEqual(domain.HumanCause(""))
						ctx.Expect(after.Reason).ToEqual("")
						ctx.Expect(after.SpecException).To(specs.BeFalse())
						ctx.Expect(after.SpecEscalated).To(specs.BeFalse())
					})
				})
			}
		})

		s.When("any event that leaves needs-human is applied", func(s *specs.Spec) {
			rows := []applyExitRow{
				{"entering a stage clears it", entered("build", 2), ""},
				{"needs-info clears it", domain.NeedsInfo{Job: jobID, From: "spec", At: at(2)}, ""},
				{"a merge request clears it", domain.MergeRequested{Job: jobID, At: at(2)}, ""},
				{"a stop clears it", domain.JobStopped{Job: jobID, By: "pablogore", At: at(2)}, ""},
				{"a dropped slice clears it", domain.SliceDropped{Job: jobID, Initiative: initiativeID, By: "pablogore", Text: directionText, At: at(2)}, ""},
				{"a rejection clears it and keeps only its own reason", domain.JobRejected{Job: jobID, Reason: "duplicate of #12", At: at(2)}, "duplicate of #12"},
			}
			specs.Table(s, rows, func(r applyExitRow) string { return r.name }, func(ctx *specs.Context, r applyExitRow) {
				job := newJob()
				job.Stage, job.Reason, job.Cause = domain.StageNeedsHuman, "spec asks for a length exception", domain.CauseSpecException
				job.SpecException, job.SpecEscalated = true, true

				after := decide.Apply(job, []domain.Event{r.ev})

				ctx.Expect(after.Cause).ToEqual(domain.HumanCause(""))
				ctx.Expect(after.Reason).ToEqual(r.reason)
				ctx.Expect(after.SpecException).To(specs.BeFalse())
				ctx.Expect(after.SpecEscalated).To(specs.BeFalse())
			})
		})

		// Pins today's behaviour: S2 makes an OK read the cause and replaces
		// this case, since a BLOCKED build never passed gate and review.
		s.When("a person gives an OK after a builder BLOCKED", func(s *specs.Spec) {
			s.It("still requests the merge", func(ctx *specs.Context) {
				job := blockedJob(ctx, 2)
				ctx.Expect(job.Cause).ToEqual(domain.CauseBlocked)

				evs, err := decide.Decide(job, domain.HumanApproved{Job: jobID, By: "pablogore", At: at(2)}, defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual([]domain.Event{domain.MergeRequested{Job: jobID, At: at(2)}})
			})
		})
	})
}
