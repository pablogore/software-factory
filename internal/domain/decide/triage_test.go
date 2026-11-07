package decide_test

import (
	"strings"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/domain/decide"
)

const (
	triageBrief    = "rebase on the base and fix the failing test in queue_test.go"
	triageQuestion = "Keep the old flag name? (yes, recommended / no)"
)

func triageRetry(minute int) domain.ResultSubmitted {
	return result("tech-lead", minute, domain.TriageResult{Decision: domain.TriageRetry, Brief: triageBrief})
}

func triageEscalate(minute int) domain.ResultSubmitted {
	return result("tech-lead", minute, domain.TriageResult{Decision: domain.TriageEscalate, Question: triageQuestion})
}

// throughTriage applies evs to job and, when they put it in triage, triage's
// ESCALATE at the same time: the way a job that hits a wall reaches
// needs-human while it has triage retries left. It returns the job and the
// events that put it there.
func throughTriage(ctx *specs.Context, job domain.Job, evs []domain.Event, p domain.Pipeline) (domain.Job, []domain.Event) {
	job = decide.Apply(job, evs)
	if job.Stage != domain.StageTriage {
		return job, evs
	}
	esc := triageEscalate(0)
	for _, ev := range evs {
		if t, ok := ev.(domain.TriageRequested); ok {
			esc.At = t.At
		}
	}
	out, err := decide.Decide(job, esc, p)
	ctx.Expect(err).To(specs.BeNil())
	return decide.Apply(job, out), out
}

// escalatedFrom is the needs-human triage's ESCALATE puts a job in after a
// wall with reason and cause.
func escalatedFrom(reason string, cause domain.HumanCause, minute int) domain.NeedsHuman {
	return domain.NeedsHuman{Job: jobID, Reason: reason, Question: triageQuestion, Cause: cause, At: at(minute)}
}

// triaging is the TriageRequested a wall with reason and cause gives a job
// with triage retries left.
func triaging(reason string, cause domain.HumanCause, minute int) domain.TriageRequested {
	return domain.TriageRequested{Job: jobID, Reason: reason, Cause: cause, At: at(minute)}
}

func mergeFailed(reason string, minute int) domain.MergeFailed {
	return domain.MergeFailed{Job: jobID, Reason: reason, At: at(minute)}
}

// merging is a job in round 2 whose merge was requested.
func merging() domain.Job {
	job := newJob()
	job.Stage, job.Round = domain.StageMerge, 2
	return job
}

// lastReviewOf is a job in the last review round where every reviewer but
// reviewer-code has passed, so its CHANGES is a wall.
func lastReviewOf(round int) domain.Job {
	job := newJob()
	job.Stage, job.Round, job.Passed = "review", round, []string{"reviewer-security", "reviewer-ux", "reviewer-ui", "security-deep"}
	return job
}

// inTriage is a job triage looks at after the gate failed in round 3 of 3,
// with the reviewers of that round.
func inTriage() domain.Job {
	job := atGate(3, []string{"reviewer-code"})
	job.Stage, job.Reason, job.Wall = domain.StageTriage, "gate failed in the last round (3 of 3)", domain.CauseGateFailed
	return job
}

// withTriageRetries is defaultPipeline with limits.triage_retries n.
func withTriageRetries(n int) domain.Pipeline {
	p := defaultPipeline()
	p.Limits.TriageRetries = &n
	return p
}

type wallRow struct {
	name   string
	job    func() domain.Job
	ev     domain.Event
	reason string
	cause  domain.HumanCause
}

func wallRows() []wallRow {
	return []wallRow{
		{"a builder BLOCKED", func() domain.Job { return building(1) }, blockedBuild("cannot reach the payments API", 1),
			"cannot reach the payments API", domain.CauseBlocked},
		{"a gate failing in the last round", func() domain.Job { return atGate(3, nil) }, gateFailed(1),
			"gate failed in the last round (3 of 3)", domain.CauseGateFailed},
		{"CHANGES in the last round", func() domain.Job { return lastReviewOf(3) }, changes("reviewer-code", 1),
			"CHANGES in the last round (3 of 3) from reviewer-code", domain.CauseChanges},
		{"a failed merge", merging, mergeFailed(" conflict in go.sum ", 1),
			"merge failed: conflict in go.sum", domain.CauseMergeFailed},
	}
}

// lastOf returns the last of evs, the one a wall adds.
func lastOf(evs []domain.Event) domain.Event {
	return evs[len(evs)-1]
}

type triageLimitRow struct {
	name    string
	p       domain.Pipeline
	retries int
	triage  bool
}

type triageRefusalRow struct {
	name string
	job  func() domain.Job
	ev   domain.Event
	err  error
}

func TestTriage(t *testing.T) {
	specs.Describe(t, "Decide on a job that hits a wall", func(s *specs.Spec) {
		s.When("it has triage retries left", func(s *specs.Spec) {
			specs.Table(s, wallRows(), func(r wallRow) string { return "sends " + r.name + " to triage, not to a person" }, func(ctx *specs.Context, r wallRow) {
				job := r.job()

				evs, err := decide.Decide(job, r.ev, defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(lastOf(evs)).ToEqual(triaging(r.reason, r.cause, 1))
				after := decide.Apply(job, evs)
				ctx.Expect(after.Stage).ToEqual(domain.StageTriage)
				ctx.Expect(after.Wall).ToEqual(r.cause)
				ctx.Expect(after.Reason).ToEqual(r.reason)
				ctx.Expect(after.Cause).ToEqual(domain.HumanCause(""))
				ctx.Expect(after.WaitSeq).ToEqual(job.WaitSeq)
				ctx.Expect(after.Validate()).To(specs.BeNil())
			})

			s.It("leaves an approver ESCALATE to a person: the approver already judged the job", func(ctx *specs.Context) {
				job := newJob()
				job.Stage, job.Round = "approve", 2

				evs, err := decide.Decide(job, result("approver", 1, domain.ApproveResult{Decision: domain.ApproveEscalate}), defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual([]domain.Event{domain.NeedsHuman{Job: jobID, Reason: "approver escalated", Cause: domain.CauseEscalated, At: at(1)}})
			})
		})

		s.When("triage answers RETRY", func(s *specs.Spec) {
			s.It("sends the job back to build for a new round with the brief, running the same reviewers", func(ctx *specs.Context) {
				job := inTriage()

				evs, err := decide.Decide(job, triageRetry(2), defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual([]domain.Event{
					domain.TriageRetried{Job: jobID, Role: "tech-lead", Brief: triageBrief, At: at(2)},
					started(4, 2, "reviewer-code"),
					entered("build", 2),
				})
				after := decide.Apply(job, evs)
				ctx.Expect(after.Stage).ToEqual("build")
				ctx.Expect(after.Round).ToEqual(4)
				ctx.Expect(after.TriageRetries).ToEqual(1)
				ctx.Expect(after.Reviewers).ToEqual([]string{"reviewer-code"})
				ctx.Expect(after.Wall).ToEqual(domain.HumanCause(""))
				ctx.Expect(after.Reason).ToEqual("")
			})

			s.It("gives the retried round a gate failure of its own before a person sees it", func(ctx *specs.Context) {
				job := inTriage()
				for _, ev := range []domain.Event{triageRetry(2), result("builder", 3, domain.BuildResult{Status: domain.BuildReady})} {
					evs, err := decide.Decide(job, ev, defaultPipeline())
					ctx.Expect(err).To(specs.BeNil())
					job = decide.Apply(job, evs)
				}

				evs, err := decide.Decide(job, gateFailed(4), defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual([]domain.Event{domain.NeedsHuman{Job: jobID, Reason: "gate failed in the last round (4 of 4)",
					Question: "Triage already retried this job 1 of 1 times. Send it back to build with your feedback, or stop it?",
					Cause:    domain.CauseGateFailed, At: at(4)}})
			})
		})

		s.When("triage answers ESCALATE", func(s *specs.Spec) {
			s.It("puts the job in needs-human with triage's question and the wall's cause", func(ctx *specs.Context) {
				job := inTriage()

				evs, err := decide.Decide(job, triageEscalate(2), defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual([]domain.Event{escalatedFrom("gate failed in the last round (3 of 3)", domain.CauseGateFailed, 2)})
				after := decide.Apply(job, evs)
				ctx.Expect(after.Stage).ToEqual(domain.StageNeedsHuman)
				ctx.Expect(after.Question).ToEqual(triageQuestion)
				ctx.Expect(after.Cause).ToEqual(domain.CauseGateFailed)
				ctx.Expect(after.Wall).ToEqual(domain.HumanCause(""))
				ctx.Expect(after.TriageRetries).ToEqual(0)
				ctx.Expect(after.WaitSeq).ToEqual(job.WaitSeq + 1)
				ctx.Expect(after.Validate()).To(specs.BeNil())
			})

			s.It("lets a person's feedback send it back to build, forgetting the question", func(ctx *specs.Context) {
				job := inTriage()
				evs, err := decide.Decide(job, triageEscalate(2), defaultPipeline())
				ctx.Expect(err).To(specs.BeNil())
				job = decide.Apply(job, evs)

				evs, err = decide.Decide(job, answering(message(domain.AuthorPerson, true, 3), job.WaitSeq), defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				after := decide.Apply(job, evs)
				ctx.Expect(after.Stage).ToEqual("build")
				ctx.Expect(after.Question).ToEqual("")
			})
		})

		s.When("the job's triage retries are used up", func(s *specs.Spec) {
			specs.Table(s, wallRows(), func(r wallRow) string { return "sends " + r.name + " to a person with a closed question" }, func(ctx *specs.Context, r wallRow) {
				job := r.job()
				job.TriageRetries = 1
				// The retried round is the job's last: max_rounds plus the retry.
				job.Round++
				reason := strings.Replace(r.reason, "(3 of 3)", "(4 of 4)", 1)

				evs, err := decide.Decide(job, r.ev, defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(lastOf(evs)).ToEqual(domain.NeedsHuman{Job: jobID, Reason: reason,
					Question: "Triage already retried this job 1 of 1 times. Send it back to build with your feedback, or stop it?", Cause: r.cause, At: at(1)})
			})

			rows := []triageLimitRow{
				{"with triage_retries 0 escalates at once, without triage", withTriageRetries(0), 0, false},
				{"with triage_retries 2 still triages after one retry", withTriageRetries(2), 1, true},
				{"with triage_retries 2 escalates after two retries", withTriageRetries(2), 2, false},
				{"with no triage_retries triages a job never retried, as by default 1", defaultPipeline(), 0, true},
			}
			specs.Table(s, rows, func(r triageLimitRow) string { return r.name }, func(ctx *specs.Context, r triageLimitRow) {
				job := building(1)
				job.TriageRetries = r.retries

				evs, err := decide.Decide(job, blockedBuild("cannot reach the payments API", 1), r.p)

				ctx.Expect(err).To(specs.BeNil())
				_, triaged := lastOf(evs).(domain.TriageRequested)
				ctx.Expect(triaged).ToEqual(r.triage)
			})

			s.It("says triage is off with triage_retries 0", func(ctx *specs.Context) {
				evs, err := decide.Decide(building(1), blockedBuild("", 1), withTriageRetries(0))

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual([]domain.Event{domain.NeedsHuman{Job: jobID, Reason: "builder blocked",
					Question: "Triage is off for this job (triage_retries 0). Send it back to build with your feedback, or stop it?", Cause: domain.CauseBlocked, At: at(1)}})
			})

			s.It("follows a person's triage_retries for this job over the pipeline's", func(ctx *specs.Context) {
				job := building(1)
				job.TriageRetries, job.Limits.TriageRetries = 1, new(2)

				evs, err := decide.Decide(job, blockedBuild("", 1), defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual([]domain.Event{triaging("builder blocked", domain.CauseBlocked, 1)})
			})

			s.It("walks a wall, a retry and the same wall again into needs-human", func(ctx *specs.Context) {
				job := building(1)
				for i, ev := range []domain.Event{blockedBuild("spec contradicts itself", 1), triageRetry(2)} {
					evs, err := decide.Decide(job, ev, defaultPipeline())
					ctx.Expect(err).To(specs.BeNil())
					job = decide.Apply(job, evs)
					ctx.Expect(job.Stage).ToEqual([]string{domain.StageTriage, "build"}[i])
				}

				evs, err := decide.Decide(job, blockedBuild("spec contradicts itself", 3), defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				after := decide.Apply(job, evs)
				ctx.Expect(after.Stage).ToEqual(domain.StageNeedsHuman)
				ctx.Expect(after.Cause).ToEqual(domain.CauseBlocked)
				ctx.Expect(after.Round).ToEqual(2)
				ctx.Expect(after.Question).ToEqual("Triage already retried this job 1 of 1 times. Send it back to build with your feedback, or stop it?")
			})
		})

		s.When("an event does not fit triage", func(s *specs.Spec) {
			unwalled := func() domain.Job {
				job := inTriage()
				job.Wall = ""
				return job
			}
			rows := []triageRefusalRow{
				{"refuses a triage result from a role other than tech-lead", inTriage,
					result("builder", 2, domain.TriageResult{Decision: domain.TriageRetry, Brief: triageBrief}), decide.ErrIllegalTransition},
				{"refuses another kind's result in triage", inTriage,
					result("tech-lead", 2, domain.BuildResult{Status: domain.BuildReady}), decide.ErrIllegalTransition},
				{"refuses a triage result outside triage", func() domain.Job { return building(1) }, triageRetry(2), decide.ErrIllegalTransition},
				{"refuses a RETRY without a brief", inTriage,
					result("tech-lead", 2, domain.TriageResult{Decision: domain.TriageRetry, Brief: " "}), domain.ErrMissingField},
				{"refuses an ESCALATE without a question", inTriage,
					result("tech-lead", 2, domain.TriageResult{Decision: domain.TriageEscalate}), domain.ErrMissingField},
				{"refuses a result for a job in triage that records no wall", unwalled, triageEscalate(2), domain.ErrMissingField},
				{"refuses a failed merge outside the merge", func() domain.Job { return building(1) }, mergeFailed("conflict", 1), decide.ErrIllegalTransition},
			}
			specs.Table(s, rows, func(r triageRefusalRow) string { return r.name }, func(ctx *specs.Context, r triageRefusalRow) {
				job := r.job()

				evs, err := decide.Decide(job, r.ev, defaultPipeline())

				ctx.Expect(err).To(specs.MatchError(r.err))
				ctx.Expect(evs).To(specs.BeEmpty())
			})

			s.It("stops a job in triage whose budget is spent", func(ctx *specs.Context) {
				evs, err := decide.Decide(inTriage(), budgetSpent(2), defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual([]domain.Event{domain.NeedsHuman{Job: jobID, Reason: "budget exhausted", Cause: domain.CauseBudget, Spent: 5, At: at(2)}})
			})
		})
	})
}
