package decide_test

import (
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/domain/decide"
)

// agentLimited is a builder's session on claude cut by its rate limit, which
// it may resume half an hour later.
func agentLimited(minute int) domain.AgentLimited {
	return domain.AgentLimited{Job: jobID, Role: "builder", Agent: "claude", Reason: "rate limited", ResumeAt: at(minute + 30), At: at(minute)}
}

type limitedRow struct {
	name string
	ev   domain.AgentLimited
	want error
}

type limitedResetRow struct {
	name string
	ev   domain.Event
}

func TestAgentLimited(t *testing.T) {
	specs.Describe(t, "Decide on an agent's usage or rate limit", func(s *specs.Spec) {
		s.It("pauses the job in its stage with the agent, the reason and when it resumes", func(ctx *specs.Context) {
			job := building(1)

			evs, err := decide.Decide(job, agentLimited(1), defaultPipeline())

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(evs).ToEqual([]domain.Event{agentLimited(1)})
			after := decide.Apply(job, evs)
			ctx.Expect(after.Stage).ToEqual("build")
			ctx.Expect(after.Limited).ToEqual(&domain.Limited{Role: "builder", Agent: "claude", Reason: "rate limited", Until: at(31)})
		})

		s.It("spends no retry and no round, and records no failed attempt", func(ctx *specs.Context) {
			job := failedBuilding(1, 1)

			evs, err := decide.Decide(job, agentLimited(2), defaultPipeline())

			ctx.Expect(err).To(specs.BeNil())
			for _, ev := range evs {
				_, failed := ev.(domain.AttemptFailed)
				ctx.Expect(failed).To(specs.BeFalse())
			}
			after := decide.Apply(job, evs)
			ctx.Expect(after.AttemptsFailed).ToEqual(1)
			ctx.Expect(after.Round).ToEqual(1)
		})

		s.It("pauses a job in triage too, where an agent runs", func(ctx *specs.Context) {
			job := newJob()
			job.Stage, job.Reason, job.Wall = domain.StageTriage, "builder blocked", domain.CauseBlocked
			ev := domain.AgentLimited{Job: jobID, Role: "tech-lead", Reason: "rate limited", ResumeAt: at(30), At: at(1)}

			evs, err := decide.Decide(job, ev, defaultPipeline())

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(evs).ToEqual([]domain.Event{ev})
		})

		rows := []limitedRow{
			{"without a reason", domain.AgentLimited{Job: jobID, Role: "builder", Reason: " ", ResumeAt: at(30), At: at(1)}, domain.ErrMissingField},
			{"without when it resumes", domain.AgentLimited{Job: jobID, Role: "builder", Reason: "rate limited", At: at(1)}, domain.ErrMissingField},
			{"without a valid role", domain.AgentLimited{Job: jobID, Role: "../x", Reason: "rate limited", ResumeAt: at(30), At: at(1)}, domain.ErrInvalidValue},
		}
		specs.Table(s, rows, func(r limitedRow) string { return "is refused " + r.name }, func(ctx *specs.Context, r limitedRow) {
			evs, err := decide.Decide(building(1), r.ev, defaultPipeline())

			ctx.Expect(err).To(specs.MatchError(r.want))
			ctx.Expect(evs).To(specs.BeEmpty())
		})

		s.It("is illegal outside a stage where agents run", func(ctx *specs.Context) {
			job := building(1)
			job.Stage = domain.StageGate

			evs, err := decide.Decide(job, agentLimited(1), defaultPipeline())

			ctx.Expect(err).To(specs.MatchError(decide.ErrIllegalTransition))
			ctx.Expect(evs).To(specs.BeEmpty())
		})

		resets := []limitedResetRow{
			{"entering another stage", entered("gate", 2)},
			{"a new round", domain.RoundStarted{Job: jobID, Round: 2, At: at(2)}},
		}
		specs.Table(s, resets, func(r limitedResetRow) string { return "ends the pause on " + r.name }, func(ctx *specs.Context, r limitedResetRow) {
			job := decide.Apply(building(1), []domain.Event{agentLimited(1)})

			ctx.Expect(decide.Apply(job, []domain.Event{r.ev}).Limited).To(specs.BeNil())
		})
	})
}
