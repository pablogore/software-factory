package decide_test

import (
	"slices"
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

// okRow is a person's OK on a job that waits for one cause: the events it
// leads to, or the error it is refused with.
type okRow struct {
	name  string
	cause domain.HumanCause
	evs   []domain.Event
	err   error
}

type causeOKRow struct {
	name  string
	cause domain.HumanCause
	err   error
}

// waitingFor puts a slice job in needs-human for cause, the way the
// causeRow that records it does.
func waitingFor(ctx *specs.Context, cause domain.HumanCause) domain.Job {
	rows := causeRows()
	i := slices.IndexFunc(rows, func(r causeRow) bool { return r.cause == cause })
	ctx.Expect(i).To(specs.Not(specs.Equal(-1)))
	return stoppedFor(ctx, rows[i])
}

// okMoves reports whether a person's OK moves a job that waits for cause.
func okMoves(cause domain.HumanCause) bool {
	return cause == domain.CauseEscalated || cause == domain.CauseChanges || cause == domain.CauseSpecException
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

				ctx.Expect(causes).ToEqual(domain.HumanCauses())
			})
		})

		s.When("a person moves the job out of needs-human", func(s *specs.Spec) {
			for _, r := range causeRows() {
				exits := []exitRow{
					{"feedback", message(domain.AuthorPerson, true, 2)},
					{"a stop", stop("pablogore", 2)},
					{"a direction", direction(domain.AuthorPerson, true, 2)},
				}
				if okMoves(r.cause) {
					exits = append(exits, exitRow{"an OK", domain.HumanApproved{Job: jobID, By: "pablogore", WaitSeq: 1, At: at(2)}})
				}
				if r.cause == domain.CauseChanges {
					exits = append(exits, exitRow{"an override", override(domain.AuthorPerson, true, 2)})
				}
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

		s.When("a person gives an OK in needs-human", func(s *specs.Spec) {
			ok := domain.HumanApproved{Job: jobID, By: "pablogore", WaitSeq: 1, At: at(2)}
			rows := []okRow{
				{"after a builder BLOCKED it is refused, naming the cause", domain.CauseBlocked, nil, decide.ErrOKRefused},
				{"after an approver ESCALATE it requests the merge", domain.CauseEscalated, []domain.Event{domain.MergeRequested{Job: jobID, At: at(2)}}, nil},
				{"after CHANGES in the last round it sends the job back to build in the same round", domain.CauseChanges, []domain.Event{entered("build", 2)}, nil},
				{"after a failed gate in the last round it is refused, naming the cause", domain.CauseGateFailed, nil, decide.ErrOKRefused},
				{"after specgate's escalation it is refused, naming the cause", domain.CauseSpecEscalated, nil, decide.ErrOKRefused},
				{"on a length exception it sends the job on to build", domain.CauseSpecException, []domain.Event{entered("build", 2)}, nil},
			}
			specs.Table(s, rows, func(r okRow) string { return r.name }, func(ctx *specs.Context, r okRow) {
				job := waitingFor(ctx, r.cause)

				evs, err := decide.Decide(job, ok, defaultPipeline())

				if r.err != nil {
					ctx.Expect(err).To(specs.MatchError(r.err))
					// The cause, never the free-text reason, names why.
					ctx.Expect(err.Error()).ToEqual(`OK refused: cause "` + string(r.cause) + `"`)
					ctx.Expect(evs).To(specs.BeEmpty())
					ctx.Expect(decide.Apply(job, evs).Stage).ToEqual(domain.StageNeedsHuman)
					return
				}
				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual(r.evs)
				ctx.Expect(decide.Apply(job, evs).Round).ToEqual(job.Round)
			})

			s.It("has one case for each cause", func(ctx *specs.Context) {
				var causes []domain.HumanCause
				for _, r := range rows {
					causes = append(causes, r.cause)
				}

				ctx.Expect(causes).ToEqual(domain.HumanCauses())
			})

			bad := []causeOKRow{
				{"with no recorded cause it is refused and nothing moves", "", domain.ErrMissingField},
				{"with an unknown cause it is refused and nothing moves", "approved", domain.ErrInvalidValue},
				{"with a cause that only looks like escalated it is refused and nothing moves", "Escalated", domain.ErrInvalidValue},
			}
			specs.Table(s, bad, func(r causeOKRow) string { return r.name }, func(ctx *specs.Context, r causeOKRow) {
				job := waitingFor(ctx, domain.CauseEscalated)
				job.Cause = r.cause

				evs, err := decide.Decide(job, ok, defaultPipeline())

				ctx.Expect(err).To(specs.MatchError(r.err))
				ctx.Expect(evs).To(specs.BeEmpty())
				ctx.Expect(decide.Apply(job, evs)).ToEqual(job)
			})

			s.It("without who gave it is refused for the missing name before the cause", func(ctx *specs.Context) {
				job := waitingFor(ctx, domain.CauseBlocked)

				evs, err := decide.Decide(job, domain.HumanApproved{Job: jobID, By: " ", WaitSeq: 1, At: at(2)}, defaultPipeline())

				ctx.Expect(err).To(specs.MatchError(domain.ErrMissingField))
				ctx.Expect(err).To(specs.Not(specs.MatchError(decide.ErrOKRefused)))
				ctx.Expect(evs).To(specs.BeEmpty())
			})
		})
	})
}
