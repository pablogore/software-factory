package decide_test

import (
	"strings"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/domain/decide"
)

// progressed is a builder's progress message.
func progressed(text string, minute int) domain.AgentProgressed {
	return domain.AgentProgressed{Job: jobID, Role: "builder", Kind: domain.ProgressMessage, Text: text, At: at(minute)}
}

type progressRow struct {
	name string
	ev   func() domain.AgentProgressed
	want error
}

func TestAgentProgressed(t *testing.T) {
	specs.Describe(t, "Decide on an agent's progress", func(s *specs.Spec) {
		s.It("records the message in the job's history and leaves the job where it is", func(ctx *specs.Context) {
			job := building(1)

			evs, err := decide.Decide(job, progressed("running the tests", 1), defaultPipeline())

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(evs).ToEqual([]domain.Event{progressed("running the tests", 1)})
			ctx.Expect(decide.Apply(job, evs)).ToEqual(job)
		})

		s.It("takes a message of the longest length", func(ctx *specs.Context) {
			evs, err := decide.Decide(building(1), progressed(strings.Repeat("é", domain.MaxProgressText), 1), defaultPipeline())

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(len(evs)).ToEqual(1)
		})

		s.It("is refused on a stopped job", func(ctx *specs.Context) {
			job := building(1)
			job.Stage = domain.StageStopped

			evs, err := decide.Decide(job, progressed("running the tests", 1), defaultPipeline())

			ctx.Expect(err).To(specs.MatchError(decide.ErrStopped))
			ctx.Expect(evs).To(specs.BeEmpty())
		})

		s.It("records the message in every stage of a sound job without moving it", func(ctx *specs.Context) {
			stages := []string{"intake", "spec", domain.StageSpecgate, "build", domain.StageGate, "review", "approve", domain.StageMerge, domain.StageDone, domain.StageNeedsHuman}
			for _, stage := range stages {
				job := building(1)
				job.Stage = stage

				evs, err := decide.Decide(job, progressed("running the tests", 1), defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual([]domain.Event{progressed("running the tests", 1)})
				ctx.Expect(decide.Apply(job, evs)).ToEqual(job)
			}
			asked := building(1)
			asked.Stage, asked.InfoFrom = domain.StageNeedsInfo, "spec"
			for _, job := range []domain.Job{asked, inTriage()} {
				evs, err := decide.Decide(job, progressed("running the tests", 1), defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual([]domain.Event{progressed("running the tests", 1)})
				ctx.Expect(decide.Apply(job, evs)).ToEqual(job)
			}
		})

		s.It("is refused on a job in needs-info that records no asker", func(ctx *specs.Context) {
			job := building(1)
			job.Stage, job.InfoFrom = domain.StageNeedsInfo, ""

			evs, err := decide.Decide(job, progressed("running the tests", 1), defaultPipeline())

			ctx.Expect(err).To(specs.MatchError(domain.ErrMissingField))
			ctx.Expect(evs).To(specs.BeEmpty())
		})

		s.It("is refused on a job in triage that records no wall", func(ctx *specs.Context) {
			job := inTriage()
			job.Wall = ""

			evs, err := decide.Decide(job, progressed("running the tests", 1), defaultPipeline())

			ctx.Expect(err).To(specs.MatchError(domain.ErrMissingField))
			ctx.Expect(evs).To(specs.BeEmpty())
		})

		s.It("is refused on a job in triage with an unknown wall", func(ctx *specs.Context) {
			job := inTriage()
			job.Wall = "approved"

			evs, err := decide.Decide(job, progressed("running the tests", 1), defaultPipeline())

			ctx.Expect(err).To(specs.MatchError(domain.ErrInvalidValue))
			ctx.Expect(evs).To(specs.BeEmpty())
		})

		rows := []progressRow{
			{"without a message", func() domain.AgentProgressed { return progressed("", 1) }, domain.ErrMissingField},
			{"with a blank message", func() domain.AgentProgressed { return progressed(" \n\t", 1) }, domain.ErrMissingField},
			{"with a message over the longest length", func() domain.AgentProgressed {
				return progressed(strings.Repeat("x", domain.MaxProgressText+1), 1)
			}, domain.ErrInvalidValue},
			{"without a role", func() domain.AgentProgressed { e := progressed("running the tests", 1); e.Role = ""; return e }, domain.ErrInvalidValue},
			{"when it is not a message", func() domain.AgentProgressed {
				e := progressed("", 1)
				e.Kind = domain.ProgressEnded
				e.StopReason = "end_turn"
				return e
			}, domain.ErrInvalidValue},
		}
		specs.Table(s, rows, func(r progressRow) string { return "is refused " + r.name }, func(ctx *specs.Context, r progressRow) {
			evs, err := decide.Decide(building(1), r.ev(), defaultPipeline())

			ctx.Expect(err).To(specs.MatchError(r.want))
			ctx.Expect(evs).To(specs.BeEmpty())
		})
	})
}
