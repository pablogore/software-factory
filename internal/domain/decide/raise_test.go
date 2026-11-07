package decide_test

import (
	"math"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/domain/decide"
)

// raiseBudget is a person's raise of the job's budget to usd for wait seq,
// as the event source marks it.
func raiseBudget(kind domain.AuthorKind, allowed bool, usd float64, seq, minute int) domain.CapRaiseRequested {
	return domain.CapRaiseRequested{Job: jobID, By: "pablogore", AuthorKind: kind, Allowed: allowed, Cap: domain.CapBudget, Value: usd, WaitSeq: seq, At: at(minute)}
}

// outOfBudget is a job that spent USD 5 of its budget in stage st, round 2,
// and waits in needs-human for it.
func outOfBudget(ctx *specs.Context, st string) domain.Job {
	job := newJob()
	job.Stage, job.Round = st, 2
	if st == domain.StageTriage {
		job = inTriage()
	}
	evs, err := decide.Decide(job, budgetSpent(1), defaultPipeline())
	ctx.Expect(err).To(specs.BeNil())
	return decide.Apply(job, evs)
}

type raiseRefusalRow struct {
	name string
	job  func(*specs.Context) domain.Job
	ev   func(domain.Job) domain.CapRaiseRequested
	code domain.RaiseRefusal
}

type raiseIgnoredRow struct {
	name    string
	kind    domain.AuthorKind
	allowed bool
}

func TestCapRaise(t *testing.T) {
	specs.Describe(t, "Decide on a person's raise of the job's budget", func(s *specs.Spec) {
		s.When("the job waits in needs-human for its spent budget", func(s *specs.Spec) {
			stages := []string{"intake", "spec", "build", "review", "approve"}
			specs.Table(s, stages, func(st string) string { return "stopped in " + st + " it goes back to " + st }, func(ctx *specs.Context, st string) {
				job := outOfBudget(ctx, st)
				ctx.Expect(job.Paused).ToEqual(&domain.Paused{Stage: st, Spent: 5})

				evs, err := decide.Decide(job, raiseBudget(domain.AuthorPerson, true, 8, job.WaitSeq, 12), defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual([]domain.Event{
					domain.CapRaised{Job: jobID, By: "pablogore", Cap: domain.CapBudget, Value: 8, At: at(12)},
					domain.StageEntered{Job: jobID, Stage: st, At: at(12)},
				})
				after := decide.Apply(job, evs)
				ctx.Expect(after.Stage).ToEqual(st)
				ctx.Expect(after.Round).ToEqual(2)
				ctx.Expect(after.Budget).ToEqual(8.0)
				ctx.Expect(after.Cause).ToEqual(domain.HumanCause(""))
				ctx.Expect(after.Reason).ToEqual("")
				ctx.Expect(after.Paused).To(specs.BeNil())
				ctx.Expect(after.Validate()).To(specs.BeNil())
			})

			s.It("stopped in triage it goes back to triage with the wall it had hit", func(ctx *specs.Context) {
				job := outOfBudget(ctx, domain.StageTriage)

				evs, err := decide.Decide(job, raiseBudget(domain.AuthorPerson, true, 8, job.WaitSeq, 12), defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual([]domain.Event{
					domain.CapRaised{Job: jobID, By: "pablogore", Cap: domain.CapBudget, Value: 8, At: at(12)},
					domain.TriageRequested{Job: jobID, Reason: "gate failed in the last round (3 of 3)", Cause: domain.CauseGateFailed, At: at(12)},
				})
				after := decide.Apply(job, evs)
				ctx.Expect(after.Stage).ToEqual(domain.StageTriage)
				ctx.Expect(after.Wall).ToEqual(domain.CauseGateFailed)
				ctx.Expect(after.Validate()).To(specs.BeNil())
			})

			s.It("does not act again when the raise is delivered again after the job stopped once more", func(ctx *specs.Context) {
				job := outOfBudget(ctx, "build")
				raise := raiseBudget(domain.AuthorPerson, true, 8, job.WaitSeq, 12)
				evs, err := decide.Decide(job, raise, defaultPipeline())
				ctx.Expect(err).To(specs.BeNil())
				job = decide.Apply(job, evs)
				evs, err = decide.Decide(job, domain.BudgetExhausted{Job: jobID, Spent: 8, At: at(13)}, defaultPipeline())
				ctx.Expect(err).To(specs.BeNil())
				job = decide.Apply(job, evs)

				evs, err = decide.Decide(job, raise, defaultPipeline())

				code, ok := domain.RaiseRefusalOf(err)
				ctx.Expect(ok).To(specs.BeTrue())
				ctx.Expect(code).ToEqual(domain.RaiseStaleWait)
				ctx.Expect(evs).To(specs.BeEmpty())
			})
		})

		s.When("no raise comes", func(s *specs.Spec) {
			rows := []exitRow{
				{"an OK", domain.HumanApproved{Job: jobID, By: "pablogore", WaitSeq: 1, At: at(2)}},
				{"feedback", message(domain.AuthorPerson, true, 2)},
				{"an override", override(domain.AuthorPerson, true, 2)},
				{"a limit override", limitOverride(domain.AuthorPerson, true, "max_rounds", "4", 1, 2)},
			}
			specs.Table(s, rows, func(r exitRow) string { return r.name + " does not resume it" }, func(ctx *specs.Context, r exitRow) {
				job := outOfBudget(ctx, "build")

				evs, _ := decide.Decide(job, r.ev, defaultPipeline())

				after := decide.Apply(job, evs)
				ctx.Expect(after.Stage).ToEqual(domain.StageNeedsHuman)
				ctx.Expect(after.Cause).ToEqual(domain.CauseBudget)
			})
		})

		s.When("the raise is not from an allowed person", func(s *specs.Spec) {
			rows := []raiseIgnoredRow{
				{"a person not allowed", domain.AuthorPerson, false},
				{"a bot", domain.AuthorBot, true},
				{"CI", domain.AuthorCI, true},
			}
			specs.Table(s, rows, func(r raiseIgnoredRow) string { return "from " + r.name + " it changes nothing and is not an error" }, func(ctx *specs.Context, r raiseIgnoredRow) {
				job := outOfBudget(ctx, "build")

				evs, err := decide.Decide(job, raiseBudget(r.kind, r.allowed, 8, job.WaitSeq, 12), defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).To(specs.BeEmpty())
			})
		})

		s.When("the raise is invalid", func(s *specs.Spec) {
			stopped := func(ctx *specs.Context) domain.Job { return outOfBudget(ctx, "build") }
			valid := func(job domain.Job) domain.CapRaiseRequested {
				return raiseBudget(domain.AuthorPerson, true, 8, job.WaitSeq, 12)
			}
			with := func(change func(*domain.CapRaiseRequested)) func(domain.Job) domain.CapRaiseRequested {
				return func(job domain.Job) domain.CapRaiseRequested {
					e := valid(job)
					change(&e)
					return e
				}
			}
			rows := []raiseRefusalRow{
				{"without who asked", stopped, with(func(e *domain.CapRaiseRequested) { e.By = " " }), domain.RaiseNoAuthor},
				{"for an unknown cap", stopped, with(func(e *domain.CapRaiseRequested) { e.Cap = "budget" }), domain.RaiseUnknownCap},
				{"without a cap", stopped, with(func(e *domain.CapRaiseRequested) { e.Cap = "" }), domain.RaiseUnknownCap},
				{"to USD 0", stopped, with(func(e *domain.CapRaiseRequested) { e.Value = 0 }), domain.RaiseBadValue},
				{"to USD -8", stopped, with(func(e *domain.CapRaiseRequested) { e.Value = -8 }), domain.RaiseBadValue},
				{"to NaN", stopped, with(func(e *domain.CapRaiseRequested) { e.Value = math.NaN() }), domain.RaiseBadValue},
				{"to infinity", stopped, with(func(e *domain.CapRaiseRequested) { e.Value = math.Inf(1) }), domain.RaiseBadValue},
				{"without the wait it answers", stopped, with(func(e *domain.CapRaiseRequested) { e.WaitSeq = 0 }), domain.RaiseStaleWait},
				{"for an earlier wait", stopped, with(func(e *domain.CapRaiseRequested) { e.WaitSeq-- }), domain.RaiseStaleWait},
				{"for the issue's slices", stopped, with(func(e *domain.CapRaiseRequested) { e.Cap = domain.CapSlices }), domain.RaiseNotStopped},
				{"for the issue's USD", stopped, with(func(e *domain.CapRaiseRequested) { e.Cap = domain.CapUSD }), domain.RaiseNotStopped},
				{"to the USD 5 spent", stopped, with(func(e *domain.CapRaiseRequested) { e.Value = 5 }), domain.RaiseNotAbove},
				{"to USD 4.99", stopped, with(func(e *domain.CapRaiseRequested) { e.Value = 4.99 }), domain.RaiseNotAbove},
				{"on a job that waits for another cause", func(ctx *specs.Context) domain.Job { return waitingFor(ctx, domain.CauseEscalated) }, valid, domain.RaiseNotStopped},
				{"on a job that runs", func(*specs.Context) domain.Job {
					job := building(2)
					job.WaitSeq = 1
					return job
				}, valid, domain.RaiseNotStopped},
			}
			specs.Table(s, rows, func(r raiseRefusalRow) string { return r.name + " it resumes nothing, with code " + string(r.code) }, func(ctx *specs.Context, r raiseRefusalRow) {
				job := r.job(ctx)

				evs, err := decide.Decide(job, r.ev(job), defaultPipeline())

				ctx.Expect(err).To(specs.MatchError(domain.ErrRaiseRefused))
				code, ok := domain.RaiseRefusalOf(err)
				ctx.Expect(ok).To(specs.BeTrue())
				ctx.Expect(code).ToEqual(r.code)
				ctx.Expect(code.Validate()).To(specs.BeNil())
				ctx.Expect(evs).To(specs.BeEmpty())
				ctx.Expect(decide.Apply(job, evs)).ToEqual(job)
			})

			s.It("on a stopped job it is refused as for any event", func(ctx *specs.Context) {
				evs, err := decide.Decide(stoppedJob(), raiseBudget(domain.AuthorPerson, true, 8, 1, 12), defaultPipeline())

				ctx.Expect(err).To(specs.MatchError(decide.ErrStopped))
				ctx.Expect(evs).To(specs.BeEmpty())
			})
		})

		s.When("a spent budget comes without what the job spent", func(s *specs.Spec) {
			s.It("is refused, so no raise can pass an unknown spend", func(ctx *specs.Context) {
				evs, err := decide.Decide(building(2), domain.BudgetExhausted{Job: jobID, At: at(1)}, defaultPipeline())

				ctx.Expect(err).To(specs.MatchError(domain.ErrMissingField))
				ctx.Expect(evs).To(specs.BeEmpty())
			})
		})

		s.When("the job leaves needs-human another way", func(s *specs.Spec) {
			s.It("forgets where it was paused", func(ctx *specs.Context) {
				job := outOfBudget(ctx, "build")

				evs, err := decide.Decide(job, refused(2), defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(decide.Apply(job, evs).Paused).To(specs.BeNil())
			})
		})
	})
}
