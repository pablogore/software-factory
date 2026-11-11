package wiring_test

import (
	"context"
	"slices"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/acp"
	"github.com/pablogore/software-factory/internal/adapters/out/acp/acpfake"
	"github.com/pablogore/software-factory/internal/domain"
)

func TestDemoAgentLimit(t *testing.T) {
	specs.Describe(t, "factoryd serve with an agent that hits its limit", func(s *specs.Spec) {
		s.It("pauses the job in build with the agent, the reason and when it resumes, spending no retry and no round", func(ctx *specs.Context) {
			clk := newTickClock()
			agents := &fakeAgents{dir: ctx.Testing().TempDir(), scripts: []acpfake.Script{
				{Turns: []acpfake.Turn{{Error: &acpfake.Error{Code: -32603, Message: "rate limited"}}}},
			}}
			cfg := launchConfig(ctx, clk, agents, &syncBuffer{})
			seedBuild(ctx, cfg.Store, "j-1")
			stop, done := running(ctx, cfg)

			var job domain.Job
			tickUntil(ctx, clk, func() bool {
				var err error
				job, _, err = reopen(ctx, cfg.Store).Load(context.Background(), "j-1")
				ctx.Expect(err).To(specs.BeNil())
				return job.Limited != nil
			})

			ctx.Expect(job.Stage).ToEqual("build")
			ctx.Expect(job.Round).ToEqual(1)
			ctx.Expect(job.AttemptsFailed).ToEqual(0)
			ctx.Expect(job.Limited.Role).ToEqual("builder")
			ctx.Expect(job.Limited.Agent).ToEqual("claude")
			ctx.Expect(job.Limited.Reason).ToEqual("rate limited")
			ctx.Expect(job.Limited.Until.Before(serveEpoch.Add(acp.DefaultLimitWait))).To(specs.BeFalse())
			ctx.Expect(history(ctx, cfg.Store, "j-1")).ToEqual([]string{"StageEntered(build)", "AgentLimited"})
			hist, err := reopen(ctx, cfg.Store).History(context.Background(), "j-1")
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(hist[1].Event.(domain.AgentLimited).ResumeAt).ToEqual(job.Limited.Until)
			// Not asked again in the session, nor relaunched in a new one.
			ctx.Expect(agents.launched()).ToEqual([]string{"builder"})
			ctx.Expect(agents.received(ctx, 0).Prompts).To(specs.HaveLen(1))

			stop()
			ctx.Expect(<-done).To(specs.BeNil())
		})

		s.It("retries an error that is no limit and records the failed attempt, as before", func(ctx *specs.Context) {
			clk := newTickClock()
			agents := &fakeAgents{dir: ctx.Testing().TempDir(), scripts: []acpfake.Script{
				{Turns: []acpfake.Turn{{Error: &acpfake.Error{Code: -32603, Message: "tool failed: exit status 1"}}}},
				{Turns: []acpfake.Turn{{Submit: ready}}},
			}}
			cfg := launchConfig(ctx, clk, agents, &syncBuffer{})
			seedBuild(ctx, cfg.Store, "j-1")
			stop, done := running(ctx, cfg)

			tickUntil(ctx, clk, func() bool {
				job, _, err := reopen(ctx, cfg.Store).Load(context.Background(), "j-1")
				ctx.Expect(err).To(specs.BeNil())
				return job.Stage == domain.StageGate
			})

			got := history(ctx, cfg.Store, "j-1")
			ctx.Expect(got).ToEqual([]string{"StageEntered(build)", "AttemptFailed", "AttemptRetried", "StageEntered(gate)"})
			ctx.Expect(slices.Contains(got, "AgentLimited")).To(specs.BeFalse())
			// The error turn was asked once more in its session, then failed.
			ctx.Expect(agents.received(ctx, 0).Prompts).To(specs.HaveLen(2))
			ctx.Expect(agents.launched()).ToEqual([]string{"builder", "builder"})

			stop()
			ctx.Expect(<-done).To(specs.BeNil())
		})
	})
}
