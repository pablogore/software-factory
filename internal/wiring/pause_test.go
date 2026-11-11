package wiring_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/acp/acpfake"
	"github.com/pablogore/software-factory/internal/adapters/out/sqlite"
	"github.com/pablogore/software-factory/internal/domain"
)

// pausePipeline is the pipeline of pauseConfig as decide needs it to seed a
// job: its builder runs on claude, its reviewer and approver on codex.
var pausePipeline = domain.Pipeline{Name: "default", Stages: []domain.Stage{
	{Name: "spec", Kind: domain.KindSpec, Roles: []string{"spec-writer"}},
	{Name: "build", Kind: domain.KindBuild, Roles: []string{"builder"}},
	{Name: "review", Kind: domain.KindReview, Roles: []string{"reviewer-code"}, OnChanges: "build"},
	{Name: "approve", Kind: domain.KindApprove, Roles: []string{"approver"}},
}}

// pauseConfig is a configuration root with two agents, claude and codex,
// the spec-writer and the builder on claude, the reviewer and the approver
// on codex.
func pauseConfig(ctx *specs.Context) string {
	dir := ctx.Testing().TempDir()
	write(ctx, dir, "roles.yaml", "agents:\n  claude:\n    command: claude-code-acp\n    env:\n      ANTHROPIC_MODEL: \"{model}\"\n  codex:\n    command: codex-acp\n    args: [--model, \"{model}\"]\n")
	for _, r := range []struct{ name, kind, agent string }{
		{"spec-writer", "spec", "claude"}, {"builder", "build", "claude"}, {"reviewer-code", "review", "codex"}, {"approver", "approve", "codex"},
	} {
		write(ctx, dir, "roles/"+r.name+".md", "Do the "+r.kind+".\n")
		write(ctx, dir, "roles/"+r.name+".yaml", "name: "+r.name+"\nkind: "+r.kind+"\nprompt: roles/"+r.name+".md\nmode: blocking\nagent: "+r.agent+"\nmodel: m\ntools: [submit_result]\n")
	}
	write(ctx, dir, "pipelines/default.yaml", "name: default\nstages:\n  - spec\n  - build\n  - review:\n      parallel: [reviewer-code]\n      on_changes: build\n  - approve\n")
	return dir
}

// seedPaused saves builds a and b past specgate and review r past the
// gate, in that order, so the launcher hands them to the scheduler so.
func seedPaused(ctx *specs.Context, path string, a, b, r domain.JobID) {
	bg := context.Background()
	st, err := sqlite.Open(bg, path)
	ctx.Expect(err).To(specs.BeNil())
	defer func() { ctx.Expect(st.Close()).To(specs.BeNil()) }()
	apply := func(id domain.JobID, stage string, ev domain.Event) {
		ctx.Expect(st.Create(bg, domain.Job{ID: id, Title: "Job", Pipeline: "default", Stage: stage, Round: 1})).To(specs.BeNil())
		_, v, err := st.Load(bg, id)
		ctx.Expect(err).To(specs.BeNil())
		_, _, err = st.Apply(bg, id, v, pausePipeline, ev)
		ctx.Expect(err).To(specs.BeNil())
	}
	apply(a, domain.StageSpecgate, domain.SpecgatePassed{Job: a, At: serveEpoch})
	apply(b, domain.StageSpecgate, domain.SpecgatePassed{Job: b, At: serveEpoch})
	apply(r, domain.StageGate, domain.GatePassed{Job: r, Run: domain.ChecksRun{Command: "make check"}, At: serveEpoch})
}

func TestDemoAgentPause(t *testing.T) {
	specs.Describe(t, "factoryd serve with one of two agents paused on its limit", func(s *specs.Spec) {
		s.It("launches no session on it until the limit lets it go on, while the other agent's jobs go on", func(ctx *specs.Context) {
			clk := newTickClock()
			agents := &fakeAgents{dir: ctx.Testing().TempDir(), scripts: []acpfake.Script{
				// j-1's builder, on claude, hits its limit for an hour.
				{Turns: []acpfake.Turn{{Error: &acpfake.Error{Code: -32603, Message: "rate limited, try again in 1h"}}}},
				// j-3's reviewer and approver, on codex, pass the branch.
				{Turns: []acpfake.Turn{{Submit: json.RawMessage(`{"verdict":"PASS"}`)}}},
				{Turns: []acpfake.Turn{{Submit: json.RawMessage(`{"decision":"APPROVE"}`)}}},
				// j-2's builder, on claude once its hour passed.
				{Turns: []acpfake.Turn{{Submit: ready}}},
			}}
			cfg := launchConfig(ctx, clk, agents, &syncBuffer{})
			// One slot: j-2 waits for it behind j-1, and j-3 behind j-2.
			cfg.Config, cfg.Concurrency = pauseConfig(ctx), 1
			seedPaused(ctx, cfg.Store, "j-1", "j-2", "j-3")
			stop, done := running(ctx, cfg)
			load := func(id domain.JobID) domain.Job {
				job, _, err := reopen(ctx, cfg.Store).Load(context.Background(), id)
				ctx.Expect(err).To(specs.BeNil())
				return job
			}

			tickUntil(ctx, clk, func() bool { return load("j-3").Stage == domain.StageMerge })

			limited := load("j-1").Limited
			ctx.Expect(limited).To(specs.Not(specs.BeNil()))
			ctx.Expect(limited.Agent).ToEqual("claude")
			ctx.Expect(clk.Now().Before(limited.Until)).To(specs.BeTrue())
			// j-2 had its turn at the slot before j-3, and gave it up.
			ctx.Expect(agents.launched()).ToEqual([]string{"builder", "reviewer-code", "approver"})
			ctx.Expect(history(ctx, cfg.Store, "j-2")).ToEqual([]string{"StageEntered(build)"})

			clk.Advance(limited.Until.Sub(clk.Now()))
			tickUntil(ctx, clk, func() bool { return load("j-2").Stage == domain.StageGate })

			ctx.Expect(agents.launched()).ToEqual([]string{"builder", "reviewer-code", "approver", "builder"})
			ctx.Expect(history(ctx, cfg.Store, "j-2")).ToEqual([]string{"StageEntered(build)", "StageEntered(gate)"})
			// The job that hit the limit stays paused: relaunching it is not
			// this slice's.
			ctx.Expect(load("j-1").Stage).ToEqual("build")
			ctx.Expect(load("j-1").Limited).To(specs.Not(specs.BeNil()))

			stop()
			ctx.Expect(<-done).To(specs.BeNil())
		})
	})
}
