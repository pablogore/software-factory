package worker_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/scheduler"
	"github.com/pablogore/software-factory/internal/slices/worker"
)

// agentRoles are kindRoles on two agents: reviewer-* roles run on codex,
// every other on claude.
type agentRoles struct{}

func (agentRoles) Role(ctx context.Context, name string) (domain.Role, string, error) {
	role, prompt, err := kindRoles{}.Role(ctx, name)
	role.Agent = "claude"
	if role.Kind == domain.KindReview {
		role.Agent = "codex"
	}
	return role, prompt, err
}

// movingClock tells the time the test sets.
type movingClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *movingClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *movingClock) set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

// async runs every work in a goroutine of its own, as a scheduler with
// slots to spare.
type async struct{}

func (async) Submit(ctx context.Context, _ domain.JobID, w scheduler.Work) <-chan error {
	done := make(chan error, 1)
	go func() { done <- w(ctx) }()
	return done
}

// pauseWorld is a launcher of jobs following pipeline() on agentRoles, with
// its clock at at and a timer whose every wait the test ends.
type pauseWorld struct {
	world
	agents *launchedAgents
	clock  *movingClock
	timer  eachTimer
	l      *worker.Launcher
}

func newPauseWorld(ctx *specs.Context, sched interface {
	Submit(context.Context, domain.JobID, scheduler.Work) <-chan error
}) pauseWorld {
	w := newWorld(ctx)
	p := pauseWorld{world: w, agents: &launchedAgents{sub: w.sub}, clock: &movingClock{now: at}, timer: eachTimer{asked: make(chan chan time.Time, 8)}}
	p.l = w.launcher(pipeline(), p.agents, &inline{}, &lockedBuffer{})
	p.l.Scheduler, p.l.Roles, p.l.Clock = sched, agentRoles{}, p.timer
	p.l.Attempts = &worker.Attempts{Results: w.sub, Jobs: w.store, Pipelines: pipelines{"default": pipeline()}, Clock: p.clock}
	return p
}

// set changes how the sessions started from now on play.
func (p pauseWorld) set(turns []string, limited int, hang bool) {
	p.agents.mu.Lock()
	defer p.agents.mu.Unlock()
	p.agents.turns, p.agents.limited, p.agents.hang = turns, limited, hang
}

// wait is the next wait the launcher asks its timer for.
func (p pauseWorld) wait(ctx *specs.Context) chan time.Time {
	select {
	case fire := <-p.timer.asked:
		return fire
	case <-time.After(5 * time.Second):
		ctx.Testing().Fatal("the launcher never waited")
		return nil
	}
}

// limit has j-1's builder hit claude's limit, which lets it go on at
// resumeAt. It returns the wait j-1's builder runs again after.
func (p pauseWorld) limit(ctx *specs.Context) chan time.Time {
	p.set(nil, 1, false)
	launch(ctx, p.l, enteredBuild(ctx, p.world, "j-1"))
	// The session's own time limit, which never runs out here.
	p.wait(ctx)
	job, _, err := p.store.Load(context.Background(), "j-1")
	ctx.Expect(err).To(specs.BeNil())
	ctx.Expect(job.Limited).To(specs.Not(specs.BeNil()))
	return p.wait(ctx)
}

func (p pauseWorld) stage(ctx *specs.Context, id domain.JobID) string {
	job, _, err := p.store.Load(context.Background(), id)
	ctx.Expect(err).To(specs.BeNil())
	return job.Stage
}

func TestLaunchPausedAgent(t *testing.T) {
	specs.Describe(t, "worker.Launcher with an agent paused on its limit", func(s *specs.Spec) {
		s.It("starts no session on that agent for another job until the limit lets it go on, then starts it", func(ctx *specs.Context) {
			p := newPauseWorld(ctx, &inline{})
			relaunch := p.limit(ctx)
			p.set([]string{ready}, 0, false)

			launch(ctx, p.l, enteredBuild(ctx, p.world, "j-2"))
			early := p.wait(ctx)

			ctx.Expect(p.agents.started()).To(specs.HaveLen(1))
			ctx.Expect(p.stage(ctx, "j-2")).ToEqual("build")

			// Woken a minute early, it waits again.
			p.clock.set(resumeAt.Add(-time.Minute))
			close(early)
			again := p.wait(ctx)
			ctx.Expect(p.agents.started()).To(specs.HaveLen(1))

			p.clock.set(resumeAt)
			close(again)
			eventually(ctx, func() bool { return p.stage(ctx, "j-2") == domain.StageGate })

			started := p.agents.started()
			ctx.Expect(started).To(specs.HaveLen(2))
			ctx.Expect(started[1].Job).ToEqual(domain.JobID("j-2"))
			ctx.Expect(started[1].Role).ToEqual("builder")
			// The job that hit the limit waits for its own wait to end.
			ctx.Expect(p.stage(ctx, "j-1")).ToEqual("build")
			close(relaunch)
			eventually(ctx, func() bool { return p.stage(ctx, "j-1") == domain.StageGate })
		})

		s.It("runs the attempt that hit the limit again in a new session once the limit lets it go on, spending no attempt", func(ctx *specs.Context) {
			p := newPauseWorld(ctx, &inline{})
			relaunch := p.limit(ctx)
			p.set([]string{ready}, 0, false)

			// Woken early, it waits again.
			p.clock.set(resumeAt.Add(-time.Minute))
			close(relaunch)
			again := p.wait(ctx)
			ctx.Expect(p.agents.started()).To(specs.HaveLen(1))

			p.clock.set(resumeAt)
			close(again)
			eventually(ctx, func() bool { return p.stage(ctx, "j-1") == domain.StageGate })

			started := p.agents.started()
			ctx.Expect(started).To(specs.HaveLen(2))
			ctx.Expect(started[1].Job).ToEqual(domain.JobID("j-1"))
			ctx.Expect(started[1].Role).ToEqual("builder")
			ctx.Expect(tokenOf(started[1])).To(specs.Not(specs.Equal(tokenOf(started[0]))))
			ctx.Expect(lastEvents(ctx, p.world, "j-1", 3)).ToEqual([]string{"StageEntered", "AgentLimited", "StageEntered"})
			ctx.Expect(p.world.history(ctx, "j-1")).To(specs.Not(specs.Contain("AttemptFailed")))
		})

		s.It("runs it again once more when the new session hits the limit too", func(ctx *specs.Context) {
			p := newPauseWorld(ctx, &inline{})
			relaunch := p.limit(ctx)

			p.clock.set(resumeAt)
			close(relaunch)
			// The new session's time limit, then its own wait on the limit.
			p.wait(ctx)
			again := p.wait(ctx)

			ctx.Expect(p.agents.started()).To(specs.HaveLen(2))
			ctx.Expect(lastEvents(ctx, p.world, "j-1", 2)).ToEqual([]string{"AgentLimited", "AgentLimited"})
			p.set([]string{ready}, 0, false)
			close(again)
			eventually(ctx, func() bool { return p.stage(ctx, "j-1") == domain.StageGate })
		})

		s.It("starts a session at once for a job whose role runs on another agent", func(ctx *specs.Context) {
			p := newPauseWorld(ctx, &inline{})
			p.limit(ctx)
			p.set([]string{`{"verdict":"PASS"}`}, 0, false)
			p.apply(ctx, "j-2", domain.ResultSubmitted{Job: "j-2", Role: "builder", Result: domain.BuildResult{Status: domain.BuildReady}, At: at})
			review := decided[domain.StageEntered](ctx, p.world, pipeline(), "j-2", domain.GatePassed{Job: "j-2", Run: domain.ChecksRun{Command: "make check"}, At: at})

			launch(ctx, p.l, review)

			started := p.agents.started()
			ctx.Expect(started).To(specs.HaveLen(2))
			ctx.Expect(started[1].Job).ToEqual(domain.JobID("j-2"))
			ctx.Expect(started[1].Role).ToEqual("reviewer-code")
			ctx.Expect(p.stage(ctx, "j-2")).To(specs.Not(specs.Equal("review")))
		})

		s.It("does not cut a session on that agent that was running when the limit came", func(ctx *specs.Context) {
			p := newPauseWorld(ctx, async{})
			run, cancel := context.WithCancel(context.Background())
			defer cancel()
			p.set(nil, 0, true)
			ctx.Expect(p.l.Launch(run, enteredBuild(ctx, p.world, "j-1"))).To(specs.BeNil())
			p.wait(ctx)
			eventually(ctx, func() bool { return len(p.agents.started()) == 1 })
			p.set(nil, 1, false)

			ctx.Expect(p.l.Launch(run, enteredBuild(ctx, p.world, "j-2"))).To(specs.BeNil())
			p.wait(ctx)
			eventually(ctx, func() bool {
				job, _, err := p.store.Load(context.Background(), "j-2")
				ctx.Expect(err).To(specs.BeNil())
				return job.Limited != nil
			})

			ctx.Expect(p.agents.cut(0)).To(specs.BeFalse())
			// It still hands in its result, and its job moves on.
			_, err := p.sub.Submit(context.Background(), tokenOf(p.agents.started()[0]), "j-1", []byte(ready))
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(p.stage(ctx, "j-1")).ToEqual(domain.StageGate)
		})
	})
}
