package wiring_test

import (
	"context"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/acp/acpfake"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/wiring"
)

// limitedRun is factoryd's first life on cfg's store: j-1's builder hits
// claude's limit for an hour and j-2's, behind it for the one slot, waits
// for claude. It stops factoryd once j-1 is paused and returns its pause.
func limitedRun(ctx *specs.Context, cfg wiring.ServeConfig) domain.Limited {
	clk := newTickClock()
	agents := &fakeAgents{dir: ctx.Testing().TempDir(), scripts: []acpfake.Script{
		{Turns: []acpfake.Turn{{Error: &acpfake.Error{Code: -32603, Message: "rate limited, try again in 1h"}}}},
	}}
	cfg.Agents, cfg.Clock, cfg.Concurrency = agents.agents(clk), clk, 1
	stop, done := running(ctx, cfg)
	var job domain.Job
	tickUntil(ctx, clk, func() bool {
		job = load(ctx, cfg.Store, "j-1")
		return job.Limited != nil
	})
	stop()
	ctx.Expect(<-done).To(specs.BeNil())
	ctx.Expect(agents.launched()).ToEqual([]string{"builder"})
	return *job.Limited
}

func load(ctx *specs.Context, path string, id domain.JobID) domain.Job {
	job, _, err := reopen(ctx, path).Load(context.Background(), id)
	ctx.Expect(err).To(specs.BeNil())
	return job
}

// relaunchAgents are factoryd's agents after the restart: both builders
// hand in.
func relaunchAgents(ctx *specs.Context) *fakeAgents {
	return &fakeAgents{dir: ctx.Testing().TempDir(), scripts: []acpfake.Script{
		{Turns: []acpfake.Turn{{Submit: ready}}},
		{Turns: []acpfake.Turn{{Submit: ready}}},
	}}
}

func TestDemoAgentLimitResume(t *testing.T) {
	specs.Describe(t, "factoryd serve with an agent that hit its limit", func(s *specs.Spec) {
		s.It("relaunches the paused stage in a new session once the limit lets it go on, spending no attempt", func(ctx *specs.Context) {
			clk := newTickClock()
			agents := &fakeAgents{dir: ctx.Testing().TempDir(), scripts: []acpfake.Script{
				{Turns: []acpfake.Turn{{Error: &acpfake.Error{Code: -32603, Message: "rate limited, try again in 1h"}}}},
				{Turns: []acpfake.Turn{{Submit: ready}}},
			}}
			cfg := launchConfig(ctx, clk, agents, &syncBuffer{})
			seedBuild(ctx, cfg.Store, "j-1")
			stop, done := running(ctx, cfg)

			tickUntil(ctx, clk, func() bool { return load(ctx, cfg.Store, "j-1").Limited != nil })
			limited := load(ctx, cfg.Store, "j-1").Limited
			ctx.Expect(agents.launched()).ToEqual([]string{"builder"})

			clk.Advance(limited.Until.Sub(clk.Now()))
			tickUntil(ctx, clk, func() bool { return load(ctx, cfg.Store, "j-1").Stage == domain.StageGate })

			ctx.Expect(agents.launched()).ToEqual([]string{"builder", "builder"})
			ctx.Expect(history(ctx, cfg.Store, "j-1")).ToEqual([]string{"StageEntered(build)", "AgentLimited", "StageEntered(gate)"})

			stop()
			ctx.Expect(<-done).To(specs.BeNil())
		})

		s.It("keeps the agent paused across a restart until the same time, then relaunches every stage that waited for it", func(ctx *specs.Context) {
			cfg := launchConfig(ctx, newTickClock(), &fakeAgents{}, &syncBuffer{})
			seedBuild(ctx, cfg.Store, "j-1", "j-2")
			limited := limitedRun(ctx, cfg)

			clk := newTickClock()
			agents := relaunchAgents(ctx)
			cfg.Agents, cfg.Clock = agents.agents(clk), clk
			stop, done := running(ctx, cfg)

			// Restarted before its time, nothing runs on claude.
			for range 5 {
				waitAsked(ctx, clk)
				time.Sleep(20 * time.Millisecond)
				clk.Advance(serveTick)
			}
			ctx.Expect(agents.launched()).To(specs.BeEmpty())
			ctx.Expect(load(ctx, cfg.Store, "j-1").Limited).ToEqual(&limited)
			ctx.Expect(load(ctx, cfg.Store, "j-2").Stage).ToEqual("build")

			clk.Advance(limited.Until.Sub(clk.Now()))
			tickUntil(ctx, clk, func() bool {
				return load(ctx, cfg.Store, "j-1").Stage == domain.StageGate && load(ctx, cfg.Store, "j-2").Stage == domain.StageGate
			})

			ctx.Expect(agents.launched()).ToEqual([]string{"builder", "builder"})
			ctx.Expect(history(ctx, cfg.Store, "j-1")).ToEqual([]string{"StageEntered(build)", "AgentLimited", "StageEntered(gate)"})
			ctx.Expect(history(ctx, cfg.Store, "j-2")).ToEqual([]string{"StageEntered(build)", "StageEntered(gate)"})

			stop()
			ctx.Expect(<-done).To(specs.BeNil())
		})

		s.It("relaunches the stages at once when factoryd restarts past the time", func(ctx *specs.Context) {
			cfg := launchConfig(ctx, newTickClock(), &fakeAgents{}, &syncBuffer{})
			seedBuild(ctx, cfg.Store, "j-1", "j-2")
			limited := limitedRun(ctx, cfg)

			clk := newTickClock()
			clk.Advance(limited.Until.Sub(clk.Now()) + time.Minute)
			agents := relaunchAgents(ctx)
			cfg.Agents, cfg.Clock = agents.agents(clk), clk
			stop, done := running(ctx, cfg)

			tickUntil(ctx, clk, func() bool {
				return load(ctx, cfg.Store, "j-1").Stage == domain.StageGate && load(ctx, cfg.Store, "j-2").Stage == domain.StageGate
			})

			ctx.Expect(agents.launched()).ToEqual([]string{"builder", "builder"})
			ctx.Expect(history(ctx, cfg.Store, "j-1")).ToEqual([]string{"StageEntered(build)", "AgentLimited", "StageEntered(gate)"})

			stop()
			ctx.Expect(<-done).To(specs.BeNil())
		})
	})
}
