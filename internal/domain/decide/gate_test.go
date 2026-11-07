package decide_test

import (
	"strconv"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/domain/decide"
)

type gateRow struct {
	name      string
	round     int
	reviewers []string
	want      []domain.Event
}

func atGate(round int, reviewers []string) domain.Job {
	job := newJob()
	job.Stage, job.Round, job.Reviewers = domain.StageGate, round, reviewers
	return job
}

// passingRun and failingRun are the checks runs the gate events of these
// tests judged.
var (
	passingRun = domain.ChecksRun{Command: "make check", Duration: time.Second, Output: "ok"}
	failingRun = domain.ChecksRun{Command: "make check", ExitCode: 2, Duration: time.Second, Output: "--- FAIL: TestSave"}
)

func ran(round, minute int, run domain.ChecksRun) domain.ChecksRan {
	return domain.ChecksRan{Job: jobID, Round: round, Run: run, At: at(minute)}
}

func gateFailed(minute int) domain.GateFailed {
	return domain.GateFailed{Job: jobID, Run: failingRun, At: at(minute)}
}

func TestDecideGate(t *testing.T) {
	specs.Describe(t, "Decide on a failing gate", func(s *specs.Spec) {
		secCode := []string{"reviewer-security", "reviewer-code"}

		s.When("the gate fails at the gate", func(s *specs.Spec) {
			rows := []gateRow{
				{"in round 1 goes back to build in round 2 with every reviewer", 1, nil,
					[]domain.Event{ran(1, 1, failingRun), started(2, 1), entered("build", 1)}},
				{"in round 2 goes back to build in round 3 with the round's reviewers", 2, secCode,
					[]domain.Event{ran(2, 1, failingRun), started(3, 1, secCode...), entered("build", 1)}},
				{"in round 3 of 3 goes to triage", 3, secCode,
					[]domain.Event{ran(3, 1, failingRun), triaging("gate failed in the last round (3 of 3)", domain.CauseGateFailed, 1)}},
			}
			specs.Table(s, rows, func(r gateRow) string { return r.name }, func(ctx *specs.Context, r gateRow) {
				evs, err := decide.Decide(atGate(r.round, r.reviewers), gateFailed(1), threeReviewers())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual(r.want)
			})
		})

		s.When("the gate fails outside the gate", func(s *specs.Spec) {
			stages := []string{"intake", "spec", domain.StageSpecgate, "build", "review", "approve", domain.StageMerge, domain.StageNeedsHuman, domain.StageTriage}
			specs.Table(s, stages, func(st string) string { return "in " + st + " is refused" }, func(ctx *specs.Context, st string) {
				job, before := atGate(2, secCode), atGate(2, secCode)
				job.Stage, before.Stage = st, st

				evs, err := decide.Decide(job, gateFailed(1), threeReviewers())

				ctx.Expect(err).To(specs.MatchError(decide.ErrIllegalTransition))
				ctx.Expect(evs).To(specs.BeEmpty())
				ctx.Expect(job).ToEqual(before)
				ctx.Expect(decide.Apply(job, evs)).ToEqual(before)
			})
		})

		s.It("runs in the rebuilt round the reviewers the failed round was going to run", func(ctx *specs.Context) {
			p := threeReviewers()
			job := atGate(2, secCode)
			step := func(ev domain.Event) {
				evs, err := decide.Decide(job, ev, p)
				ctx.Expect(err).To(specs.BeNil())
				job = decide.Apply(job, evs)
			}

			step(gateFailed(1))
			ctx.Expect(job.Stage).ToEqual("build")
			ctx.Expect(job.Round).ToEqual(3)
			ctx.Expect(job.Reviewers).ToEqual(secCode)

			step(result("builder", 2, domain.BuildResult{Status: domain.BuildReady}))
			step(domain.GatePassed{Job: jobID, Run: passingRun, At: at(3)})
			ctx.Expect(job.Stage).ToEqual("review")

			evs, err := decide.Decide(job, pass("reviewer-ux", 4), p)
			ctx.Expect(err).To(specs.MatchError(decide.ErrIllegalTransition))
			ctx.Expect(evs).To(specs.BeEmpty())

			step(pass("reviewer-code", 5))
			step(pass("reviewer-security", 6))
			ctx.Expect(job.Stage).ToEqual("approve")
		})

		s.It("runs every reviewer in round 2 after a failing gate in round 1", func(ctx *specs.Context) {
			p := threeReviewers()
			job := atGate(1, nil)
			for _, ev := range []domain.Event{gateFailed(1), result("builder", 2, domain.BuildResult{Status: domain.BuildReady}), domain.GatePassed{Job: jobID, Run: passingRun, At: at(3)}, pass("reviewer-security", 4), pass("reviewer-ux", 5)} {
				evs, err := decide.Decide(job, ev, p)
				ctx.Expect(err).To(specs.BeNil())
				job = decide.Apply(job, evs)
			}
			ctx.Expect(job.Round).ToEqual(2)
			ctx.Expect(job.Stage).ToEqual("review")

			evs, err := decide.Decide(job, pass("reviewer-code", 6), p)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(evs).ToEqual([]domain.Event{passed("reviewer-code", 6), entered("approve", 6)})
		})

		s.When("the gate's verdict carries no checks run", func(s *specs.Spec) {
			rows := []domain.Event{
				domain.GatePassed{Job: jobID, At: at(1)},
				domain.GateFailed{Job: jobID, At: at(1)},
				domain.GateFailed{Job: jobID, Run: domain.ChecksRun{Command: " "}, At: at(1)},
			}
			specs.Table(s, rows, func(ev domain.Event) string {
				return ev.EventName() + " with command " + strconv.Quote(gateRun(ev).Command) + " is refused"
			}, func(ctx *specs.Context, ev domain.Event) {
				job := atGate(1, nil)

				evs, err := decide.Decide(job, ev, threeReviewers())

				ctx.Expect(err).To(specs.MatchError(domain.ErrMissingField))
				ctx.Expect(evs).To(specs.BeEmpty())
			})
		})

		s.It("refuses a run with a negative duration", func(ctx *specs.Context) {
			run := domain.ChecksRun{Command: "make check", Duration: -time.Second}

			evs, err := decide.Decide(atGate(1, nil), domain.GatePassed{Job: jobID, Run: run, At: at(1)}, threeReviewers())

			ctx.Expect(err).To(specs.MatchError(domain.ErrInvalidValue))
			ctx.Expect(evs).To(specs.BeEmpty())
		})
	})
}

func gateRun(ev domain.Event) domain.ChecksRun {
	if e, ok := ev.(domain.GatePassed); ok {
		return e.Run
	}
	return ev.(domain.GateFailed).Run
}
