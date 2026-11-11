package worker_test

import (
	"context"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
)

// limitedBefore saves j-1 paused on claude's limit until resumeAt, as a
// server that stopped left it, and j-2 entering build, waiting on claude.
// It returns j-2's entry.
func limitedBefore(ctx *specs.Context, p pauseWorld) domain.StageEntered {
	enteredBuild(ctx, p.world, "j-1")
	p.apply(ctx, "j-1", domain.AgentLimited{Job: "j-1", Role: "builder", Agent: "claude", Reason: "rate limited", ResumeAt: resumeAt, At: at})
	return enteredBuild(ctx, p.world, "j-2")
}

func TestLaunchResume(t *testing.T) {
	specs.Describe(t, "worker.Launcher.Resume after a restart", func(s *specs.Spec) {
		bg := context.Background()

		s.It("holds the agent a job is paused on until its Until, then runs every attempt that waited for it", func(ctx *specs.Context) {
			p := newPauseWorld(ctx, &inline{})
			limitedBefore(ctx, p)

			ctx.Expect(p.l.Resume(bg, []domain.JobID{"j-1", "j-2"})).To(specs.BeNil())
			first, second := p.wait(ctx), p.wait(ctx)

			ctx.Expect(p.agents.started()).To(specs.BeEmpty())
			p.set([]string{ready}, 0, false)
			p.clock.set(resumeAt)
			close(first)
			close(second)
			eventually(ctx, func() bool {
				return p.stage(ctx, "j-1") == domain.StageGate && p.stage(ctx, "j-2") == domain.StageGate
			})

			started := p.agents.started()
			ctx.Expect(started).To(specs.HaveLen(2))
			ctx.Expect([]string{started[0].Role, started[1].Role}).ToEqual([]string{"builder", "builder"})
			ctx.Expect(p.world.history(ctx, "j-1")).To(specs.Not(specs.Contain("AttemptFailed")))
		})

		s.It("holds a new launch on that agent too until the limit lets it go on", func(ctx *specs.Context) {
			p := newPauseWorld(ctx, &inline{})
			limitedBefore(ctx, p)
			ctx.Expect(p.l.Resume(bg, []domain.JobID{"j-1"})).To(specs.BeNil())
			p.wait(ctx)

			launch(ctx, p.l, enteredBuild(ctx, p.world, "j-2"))
			p.wait(ctx)

			ctx.Expect(p.agents.started()).To(specs.BeEmpty())
		})

		s.It("runs the attempt at once when its Until passed before the restart", func(ctx *specs.Context) {
			p := newPauseWorld(ctx, &inline{})
			limitedBefore(ctx, p)
			p.set([]string{ready}, 0, false)
			p.clock.set(resumeAt.Add(time.Minute))

			ctx.Expect(p.l.Resume(bg, []domain.JobID{"j-1", "j-2"})).To(specs.BeNil())

			ctx.Expect(p.stage(ctx, "j-1")).ToEqual(domain.StageGate)
			ctx.Expect(p.stage(ctx, "j-2")).ToEqual(domain.StageGate)
			ctx.Expect(p.agents.started()).To(specs.HaveLen(2))
		})

		s.It("hands the bus's pending launch of an attempt it resumed no second time", func(ctx *specs.Context) {
			sched := &inline{}
			p := newPauseWorld(ctx, sched)
			pending := limitedBefore(ctx, p)
			ctx.Expect(p.l.Resume(bg, []domain.JobID{"j-1", "j-2"})).To(specs.BeNil())

			launch(ctx, p.l, pending)

			ctx.Expect(sched.n).ToEqual(2)
		})

		s.It("launches nothing when no job is paused on a limit", func(ctx *specs.Context) {
			sched := &inline{}
			p := newPauseWorld(ctx, sched)
			enteredBuild(ctx, p.world, "j-1")

			ctx.Expect(p.l.Resume(bg, []domain.JobID{"j-1", "j-2"})).To(specs.BeNil())

			ctx.Expect(sched.n).ToEqual(0)
			ctx.Expect(p.agents.started()).To(specs.BeEmpty())
		})
	})
}
