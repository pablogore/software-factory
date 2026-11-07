//go:build unix

package acp_test

import (
	"context"
	"errors"
	"syscall"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/acp"
	"github.com/pablogore/software-factory/internal/adapters/out/acp/acpfake"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/ports"
)

// stoppable starts a session of the fake agent playing sc, launched with
// timeout and grace, and returns it with the pids of the child processes
// the fake launched, all running.
func stoppable(ctx *specs.Context, sc acpfake.Script, timeout, grace time.Duration) (ports.Session, []int) {
	sc.Child = true
	record := recordIn(ctx)
	cfg, err := acpfake.Config(sc, record)
	ctx.Expect(err).To(specs.BeNil())
	cfg.Timeout, cfg.Grace = timeout, grace
	sess, err := acp.New(cfg, stopped{}).Start(context.Background(), ports.SessionSpec{Job: jobID, Role: role, Dir: ctx.Testing().TempDir()})
	ctx.Expect(err).To(specs.BeNil())
	ctx.Cleanup(func() { _ = sess.Close() })
	got, err := acpfake.Load(record)
	ctx.Expect(err).To(specs.BeNil())
	ctx.Expect(got.Children).To(specs.HaveLen(1))
	ctx.Testing().Logf("fake agent launched child process %d", got.Children[0])
	ctx.Expect(syscall.Kill(got.Children[0], 0)).To(specs.BeNil())
	return sess, got.Children
}

// gone tells whether pid is no longer a process. A killed child is reaped
// by init a moment later, and a zombie still answers, so it waits for that.
func gone(pid int) bool {
	until := time.Now().Add(5 * time.Second)
	for {
		if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
			return true
		}
		if time.Now().After(until) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// noneAlive checks that no child of the fake agent outlived its session.
func noneAlive(ctx *specs.Context, pids []int) {
	for _, pid := range pids {
		ctx.Expect(gone(pid)).ToEqual(true)
		ctx.Testing().Logf("child process %d is gone", pid)
	}
}

// hanging is a turn that runs until the agent is told to stop.
var hanging = acpfake.Script{Turns: []acpfake.Turn{{Hang: true}}}

func TestAgentStop(t *testing.T) {
	specs.Describe(t, "acp.Agent stopping a session", func(s *specs.Spec) {
		bg := context.Background()

		s.It("ends a cancelled turn as cancelled and leaves no child process alive", func(ctx *specs.Context) {
			sess, children := stoppable(ctx, hanging, 0, 0)
			ch, err := sess.Prompt(bg, "build")
			ctx.Expect(err).To(specs.BeNil())

			ctx.Expect(sess.Cancel()).To(specs.BeNil())

			ctx.Expect(drain(ctx, ch)).ToEqual([]domain.AgentProgressed{
				progress(domain.AgentProgressed{Kind: domain.ProgressEnded, StopReason: domain.StopCancelled}),
			})
			noneAlive(ctx, children)
		})

		s.It("ends a turn past the time limit as timed out and leaves no child process alive", func(ctx *specs.Context) {
			sess, children := stoppable(ctx, hanging, 300*time.Millisecond, 0)
			ch, err := sess.Prompt(bg, "build")
			ctx.Expect(err).To(specs.BeNil())

			ctx.Expect(drain(ctx, ch)).ToEqual([]domain.AgentProgressed{
				progress(domain.AgentProgressed{Kind: domain.ProgressEnded, StopReason: domain.StopTimedOut}),
			})
			noneAlive(ctx, children)
			_, err = sess.Prompt(bg, "again")
			ctx.Expect(err).To(specs.MatchError(ports.ErrSessionClosed))
		})

		s.It("kills an agent that ignores the cancel, with its children, after the grace period", func(ctx *specs.Context) {
			sc := hanging
			sc.IgnoreCancel = true
			grace := 300 * time.Millisecond
			sess, children := stoppable(ctx, sc, 0, grace)
			ch, err := sess.Prompt(bg, "build")
			ctx.Expect(err).To(specs.BeNil())

			began := time.Now()
			ctx.Expect(sess.Cancel()).To(specs.BeNil())
			took := time.Since(began)

			ctx.Expect(took >= grace).ToEqual(true)
			ctx.Expect(took < acp.DefaultGrace).ToEqual(true)
			ctx.Expect(drain(ctx, ch)).ToEqual([]domain.AgentProgressed{
				progress(domain.AgentProgressed{Kind: domain.ProgressEnded, StopReason: domain.StopCancelled}),
			})
			noneAlive(ctx, children)
		})

		s.It("kills an agent that ignores the time limit, with its children, after the grace period", func(ctx *specs.Context) {
			sc := hanging
			sc.IgnoreCancel = true
			sess, children := stoppable(ctx, sc, 200*time.Millisecond, 200*time.Millisecond)
			ch, err := sess.Prompt(bg, "build")
			ctx.Expect(err).To(specs.BeNil())

			ctx.Expect(drain(ctx, ch)).ToEqual([]domain.AgentProgressed{
				progress(domain.AgentProgressed{Kind: domain.ProgressEnded, StopReason: domain.StopTimedOut}),
			})
			noneAlive(ctx, children)
		})

		s.It("leaves no child process alive when the session is closed", func(ctx *specs.Context) {
			sess, children := stoppable(ctx, acpfake.Script{}, 0, 0)

			ctx.Expect(sess.Close()).To(specs.BeNil())

			noneAlive(ctx, children)
		})
	})
}
