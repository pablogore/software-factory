package decide_test

import (
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/domain/decide"
)

// failedAttempt is a builder's session that ended twice without a result.
func failedAttempt(minute int) domain.AttemptFailed {
	return domain.AttemptFailed{Job: jobID, Role: "builder", Reason: "no result submitted", At: at(minute)}
}

// attemptRetried is the history entry for the role running again in a new
// session, the retry-th time in its stage and round.
func attemptRetried(role string, retry, minute int) domain.AttemptRetried {
	return domain.AttemptRetried{Job: jobID, Role: role, Retry: retry, At: at(minute)}
}

// failedBuilding is a job in build whose builder already failed n attempts
// in this round.
func failedBuilding(round, n int) domain.Job {
	job := building(round)
	job.AttemptsFailed = n
	return job
}

// withAttemptRetries is defaultPipeline with limits.attempt_retries set to n.
func withAttemptRetries(n int) domain.Pipeline {
	p := defaultPipeline()
	p.Limits.AttemptRetries = &n
	return p
}

const attemptWall = "builder failed 2 attempts in stage build: no result submitted"

type attemptRow struct {
	name string
	ev   func() domain.AttemptFailed
	want error
}

type attemptLimitRow struct {
	name   string
	job    func() domain.Job
	p      domain.Pipeline
	retry  int
	failed int
}

type attemptResetRow struct {
	name string
	ev   domain.Event
}

func TestAttemptFailed(t *testing.T) {
	specs.Describe(t, "Decide on a failed attempt", func(s *specs.Spec) {
		s.It("on the first one relaunches the role in a new session and leaves the job in its stage", func(ctx *specs.Context) {
			job := building(1)

			evs, err := decide.Decide(job, failedAttempt(1), defaultPipeline())

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(evs).ToEqual([]domain.Event{failedAttempt(1), attemptRetried("builder", 1, 1)})
			after := decide.Apply(job, evs)
			ctx.Expect(after.Stage).ToEqual("build")
			ctx.Expect(after.Round).ToEqual(1)
			ctx.Expect(after.AttemptsFailed).ToEqual(1)
		})

		s.It("with the retries spent hits a wall that triage looks at, with the attempt's reason", func(ctx *specs.Context) {
			job := failedBuilding(1, 1)

			evs, err := decide.Decide(job, failedAttempt(2), defaultPipeline())

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(evs).ToEqual([]domain.Event{failedAttempt(2), triaging(attemptWall, domain.CauseAttemptFailed, 2)})
			after := decide.Apply(job, evs)
			ctx.Expect(after.Stage).ToEqual(domain.StageTriage)
			ctx.Expect(after.Wall).ToEqual(domain.CauseAttemptFailed)
			ctx.Expect(after.AttemptsFailed).ToEqual(0)
		})

		s.It("reaches needs-human with the attempt's reason when triage escalates", func(ctx *specs.Context) {
			job := failedBuilding(1, 1)
			evs, err := decide.Decide(job, failedAttempt(2), defaultPipeline())
			ctx.Expect(err).To(specs.BeNil())

			after, out := throughTriage(ctx, job, evs, defaultPipeline())

			ctx.Expect(out).ToEqual([]domain.Event{escalatedFrom(attemptWall, domain.CauseAttemptFailed, 2)})
			ctx.Expect(after.Stage).ToEqual(domain.StageNeedsHuman)
			ctx.Expect(after.Cause).ToEqual(domain.CauseAttemptFailed)
			ctx.Expect(after.Reason).ToEqual(attemptWall)
		})

		s.It("sends the job back to build for another round when triage retries it", func(ctx *specs.Context) {
			job := decide.Apply(failedBuilding(1, 1), []domain.Event{failedAttempt(2), triaging(attemptWall, domain.CauseAttemptFailed, 2)})

			evs, err := decide.Decide(job, triageRetry(3), defaultPipeline())

			ctx.Expect(err).To(specs.BeNil())
			after := decide.Apply(job, evs)
			ctx.Expect(after.Stage).ToEqual("build")
			ctx.Expect(after.Round).ToEqual(2)
			ctx.Expect(after.AttemptsFailed).ToEqual(0)
		})

		s.It("with the retries and triage spent waits for a person with a closed question", func(ctx *specs.Context) {
			job := failedBuilding(2, 1)
			job.TriageRetries = 1

			evs, err := decide.Decide(job, failedAttempt(2), defaultPipeline())

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(evs).ToEqual([]domain.Event{failedAttempt(2), domain.NeedsHuman{Job: jobID, Reason: attemptWall,
				Question: "Triage already retried this job 1 of 1 times. Send it back to build with your feedback, or stop it?",
				Cause:    domain.CauseAttemptFailed, At: at(2)}})
		})

		s.It("in triage with the retries spent goes straight to a person, since triage is what failed", func(ctx *specs.Context) {
			job := newJob()
			job.Stage, job.Reason, job.Wall, job.AttemptsFailed = domain.StageTriage, "builder blocked", domain.CauseBlocked, 1
			ev := domain.AttemptFailed{Job: jobID, Role: "tech-lead", Reason: "no result submitted", At: at(2)}

			evs, err := decide.Decide(job, ev, defaultPipeline())

			ctx.Expect(err).To(specs.BeNil())
			reason := "tech-lead failed 2 attempts in stage triage: no result submitted"
			ctx.Expect(evs).ToEqual([]domain.Event{ev, domain.NeedsHuman{Job: jobID, Reason: reason, Cause: domain.CauseAttemptFailed, At: at(2)}})
			ctx.Expect(decide.Apply(job, evs).Stage).ToEqual(domain.StageNeedsHuman)
		})

		s.When("the limit is configured", func(s *specs.Spec) {
			override := func(n int) func() domain.Job {
				return func() domain.Job {
					job := failedBuilding(1, n)
					job.Limits.AttemptRetries = new(3)
					return job
				}
			}
			rows := []attemptLimitRow{
				{"the pipeline's attempt_retries 2 relaunches the role a second time", func() domain.Job { return failedBuilding(1, 1) }, withAttemptRetries(2), 2, 2},
				{"the pipeline's attempt_retries 2 hits the wall on the third failure", func() domain.Job { return failedBuilding(1, 2) }, withAttemptRetries(2), 0, 0},
				{"a person's override of 3 for the job wins over the pipeline's 2", override(2), withAttemptRetries(2), 3, 3},
				{"a person's override of 3 hits the wall on the fourth failure", override(3), withAttemptRetries(2), 0, 0},
			}
			specs.Table(s, rows, func(r attemptLimitRow) string { return r.name }, func(ctx *specs.Context, r attemptLimitRow) {
				job := r.job()

				evs, err := decide.Decide(job, failedAttempt(2), r.p)

				ctx.Expect(err).To(specs.BeNil())
				after := decide.Apply(job, evs)
				ctx.Expect(after.AttemptsFailed).ToEqual(r.failed)
				if r.retry > 0 {
					ctx.Expect(evs).ToEqual([]domain.Event{failedAttempt(2), attemptRetried("builder", r.retry, 2)})
					ctx.Expect(after.Stage).ToEqual("build")
					return
				}
				ctx.Expect(after.Stage).ToEqual(domain.StageTriage)
				ctx.Expect(after.Wall).ToEqual(domain.CauseAttemptFailed)
			})

			s.It("a person may override attempt_retries for the job up to its cap of 5", func(ctx *specs.Context) {
				job := waitingFor(ctx, domain.CauseAttemptFailed)

				evs, err := decide.Decide(job, limitOverride(domain.AuthorPerson, true, "attempt_retries", "5", job.WaitSeq, 3), defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual([]domain.Event{domain.LimitOverridden{Job: jobID, By: "pablogore", Limit: "attempt_retries", Value: 5, At: at(3)}})
				ctx.Expect(*decide.Apply(job, evs).Limits.AttemptRetries).ToEqual(5)
			})

			s.It("a person's override of attempt_retries above its cap of 5 is refused", func(ctx *specs.Context) {
				job := waitingFor(ctx, domain.CauseAttemptFailed)

				evs, err := decide.Decide(job, limitOverride(domain.AuthorPerson, true, "attempt_retries", "6", job.WaitSeq, 3), defaultPipeline())

				ctx.Expect(err).To(specs.MatchError(domain.ErrInvalidValue))
				ctx.Expect(err.Error()).ToEqual(`decide: limit override: invalid value: attempt_retries "6", want at most 5`)
				ctx.Expect(evs).To(specs.BeEmpty())
			})
		})

		resets := []attemptResetRow{
			{"entering another stage", entered("gate", 2)},
			{"a new round", domain.RoundStarted{Job: jobID, Round: 2, At: at(2)}},
			{"a wait for a person", domain.NeedsHuman{Job: jobID, Reason: "budget exhausted", Cause: domain.CauseBudget, Spent: 1, At: at(2)}},
		}
		specs.Table(s, resets, func(r attemptResetRow) string { return "starts the count again on " + r.name }, func(ctx *specs.Context, r attemptResetRow) {
			ctx.Expect(decide.Apply(failedBuilding(1, 1), []domain.Event{r.ev}).AttemptsFailed).ToEqual(0)
		})

		s.It("is refused on a stopped job", func(ctx *specs.Context) {
			job := building(1)
			job.Stage = domain.StageStopped

			evs, err := decide.Decide(job, failedAttempt(1), defaultPipeline())

			ctx.Expect(err).To(specs.MatchError(decide.ErrStopped))
			ctx.Expect(evs).To(specs.BeEmpty())
		})

		s.It("is refused in a stage that runs no agent", func(ctx *specs.Context) {
			evs, err := decide.Decide(atGate(1, nil), failedAttempt(1), defaultPipeline())

			ctx.Expect(err).To(specs.MatchError(decide.ErrIllegalTransition))
			ctx.Expect(evs).To(specs.BeEmpty())
		})

		s.It("is refused in triage when the job records no valid wall", func(ctx *specs.Context) {
			job := newJob()
			job.Stage = domain.StageTriage

			evs, err := decide.Decide(job, failedAttempt(1), defaultPipeline())

			ctx.Expect(err).To(specs.MatchError(domain.ErrMissingField))
			ctx.Expect(evs).To(specs.BeEmpty())
		})

		rows := []attemptRow{
			{"without a reason", func() domain.AttemptFailed { e := failedAttempt(1); e.Reason = " "; return e }, domain.ErrMissingField},
			{"without a role", func() domain.AttemptFailed { e := failedAttempt(1); e.Role = ""; return e }, domain.ErrInvalidValue},
		}
		specs.Table(s, rows, func(r attemptRow) string { return "is refused " + r.name }, func(ctx *specs.Context, r attemptRow) {
			evs, err := decide.Decide(building(1), r.ev(), defaultPipeline())

			ctx.Expect(err).To(specs.MatchError(r.want))
			ctx.Expect(evs).To(specs.BeEmpty())
		})
	})
}
